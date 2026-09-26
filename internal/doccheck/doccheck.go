// Package doccheck reads markdown just well enough for tests that keep documentation in step with
// the code: sections, table columns, code blocks, links and heading anchors. Only tests import it.
package doccheck

import (
	"regexp"
	"strings"
)

// Section returns the text under heading (for example "## Tools"), up to the next heading of the
// same or a higher level.
func Section(md, heading string) (string, bool) {
	level := len(heading) - len(strings.TrimLeft(heading, "#"))
	lines := strings.Split(md, "\n")
	inCode := false
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			continue
		}
		if inCode || !strings.HasPrefix(line, "#") {
			continue
		}
		if start < 0 {
			if strings.TrimSpace(line) == heading {
				start = i + 1
			}
			continue
		}
		if headingLevel(line) <= level {
			return strings.Join(lines[start:i], "\n"), true
		}
	}
	if start < 0 {
		return "", false
	}
	return strings.Join(lines[start:], "\n"), true
}

func headingLevel(line string) int {
	return len(line) - len(strings.TrimLeft(line, "#"))
}

// TableFirstColumn returns the first cell of every body row of the first table in md, without the
// backticks around it.
func TableFirstColumn(md string) []string {
	var cells []string
	seenSeparator := false
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			if seenSeparator {
				break // the table ended
			}
			continue
		}
		if strings.HasPrefix(strings.ReplaceAll(line, " ", ""), "|---") {
			seenSeparator = true
			continue
		}
		if !seenSeparator {
			continue // the header row
		}
		cell, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), " | ")
		cells = append(cells, strings.Trim(strings.TrimSpace(cell), "`"))
	}
	return cells
}

// CodeBlocks returns the contents of the fenced blocks with the given language tag.
func CodeBlocks(md, lang string) []string {
	var blocks []string
	var current []string
	in, match := false, false
	for _, line := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if in {
				if match {
					blocks = append(blocks, strings.Join(current, "\n"))
				}
				in, match, current = false, false, nil
			} else {
				in, match = true, strings.TrimPrefix(trimmed, "```") == lang
			}
			continue
		}
		if in && match {
			current = append(current, line)
		}
	}
	return blocks
}

var linkRE = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// Links returns the targets of inline links, ignoring code blocks.
func Links(md string) []string {
	var text []string
	in := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if !in {
			text = append(text, line)
		}
	}
	var links []string
	for _, m := range linkRE.FindAllStringSubmatch(strings.Join(text, "\n"), -1) {
		links = append(links, m[1])
	}
	return links
}

var nonSlug = regexp.MustCompile(`[^a-z0-9 \-]`)

// Anchors returns the anchor of every heading, the way GitHub builds them.
func Anchors(md string) map[string]bool {
	anchors := map[string]bool{}
	in := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if in || !strings.HasPrefix(line, "#") {
			continue
		}
		anchors[Slug(strings.TrimLeft(line, "# "))] = true
	}
	return anchors
}

func Slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = nonSlug.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}
