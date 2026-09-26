package agent

import (
	"fmt"
	"os"
	"slices"
	"strings"
)

// Definition is trusted admin content, unlike customer messages and tool arguments.
type Definition struct {
	Name     string
	Model    string // empty means the configured default
	Language string
	Tools    []string
	Prompt   string
}

func LoadDefinition(path string) (Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("read agent definition: %w", err)
	}
	def, err := ParseDefinition(data)
	if err != nil {
		return Definition{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return def, nil
}

// ParseDefinition reads:
//
//	---
//	name: Support
//	model: gpt-4o-mini
//	language: id
//	tools: current_time, handoff
//	---
//	You are ...
//
// Unknown or repeated keys are errors, so typos do not silently change behavior.
func ParseDefinition(data []byte) (Definition, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Definition{}, fmt.Errorf(`line 1: expected "---" to start the frontmatter`)
	}

	end := slices.IndexFunc(lines[1:], func(l string) bool { return strings.TrimSpace(l) == "---" }) + 1
	if end == 0 {
		return Definition{}, fmt.Errorf(`frontmatter is not closed with "---"`)
	}

	var def Definition
	seen := map[string]bool{}
	for i := 1; i < end; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Definition{}, fmt.Errorf(`line %d: expected "key: value"`, i+1)
		}
		key = strings.TrimSpace(key)
		if seen[key] {
			return Definition{}, fmt.Errorf("line %d: duplicate key %q", i+1, key)
		}
		seen[key] = true

		value = unquote(strings.TrimSpace(value))
		switch key {
		case "name":
			def.Name = value
		case "model":
			def.Model = value
		case "language":
			def.Language = value
		case "tools":
			def.Tools = splitList(value)
		default:
			return Definition{}, fmt.Errorf("line %d: unknown key %q", i+1, key)
		}
	}

	def.Prompt = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if def.Name == "" {
		return Definition{}, fmt.Errorf("name is required")
	}
	if def.Prompt == "" {
		return Definition{}, fmt.Errorf("the prompt after the frontmatter is empty")
	}
	return def, nil
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// splitList accepts "a, b" and "[a, b]".
func splitList(s string) []string {
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")

	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = unquote(strings.TrimSpace(item)); item != "" {
			out = append(out, item)
		}
	}
	return out
}
