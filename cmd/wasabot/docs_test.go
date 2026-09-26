package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/androsyz/wasabot/internal/config"
	"github.com/androsyz/wasabot/internal/doccheck"
)

const repoRoot = "../.."

// markdownFiles lists the documentation: the README, the docs folder and the examples.
func markdownFiles(t *testing.T) []string {
	t.Helper()
	files := []string{filepath.Join(repoRoot, "README.md")}
	for _, dir := range []string{"docs", "examples"} {
		err := filepath.WalkDir(filepath.Join(repoRoot, dir), func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".md") {
				files = append(files, path)
			}
			return err
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return files
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Every agent snippet in the docs must work as written, or the docs teach something that fails.
func TestAgentSnippetsInTheDocsAreValid(t *testing.T) {
	found := 0
	for _, file := range markdownFiles(t) {
		for i, block := range doccheck.CodeBlocks(readFile(t, file), "markdown") {
			if !strings.HasPrefix(block, "---") {
				continue
			}
			found++
			cfg := config.Config{LLMBaseURL: "http://localhost:1/v1", LLMAPIKey: "k", LLMModel: "m", AgentFile: writeAgentFile(t, block)}
			if _, _, err := newResponder(cfg, discardLog, nil); err != nil {
				t.Errorf("%s, snippet %d does not load: %v\n%s", file, i+1, err, block)
			}
		}
	}
	if found < 2 {
		t.Fatalf("expected the docs to contain agent snippets, found %d", found)
	}
}

func TestReferenceListsExactlyTheToolsThatExist(t *testing.T) {
	section, ok := doccheck.Section(readFile(t, filepath.Join(repoRoot, "docs/reference/agent-file.md")), "## Tools")
	if !ok {
		t.Fatal("the reference has no ## Tools section")
	}

	documented := doccheck.TableFirstColumn(section)
	var real []string
	for _, tool := range availableTools() {
		real = append(real, tool.Spec().Name)
	}

	slices.Sort(documented)
	slices.Sort(real)
	if !slices.Equal(documented, real) {
		t.Fatalf("documented tools %v, real tools %v", documented, real)
	}
}

func TestDocsLinksResolve(t *testing.T) {
	for _, file := range markdownFiles(t) {
		doc := readFile(t, file)
		for _, link := range doccheck.Links(doc) {
			if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") || strings.HasPrefix(link, "mailto:") {
				continue
			}
			target, anchor, _ := strings.Cut(link, "#")
			targetPath, targetDoc := file, doc
			if target != "" {
				targetPath = filepath.Join(filepath.Dir(file), target)
				b, err := os.ReadFile(targetPath)
				if err != nil {
					t.Errorf("%s links to %q, which does not exist", file, link)
					continue
				}
				targetDoc = string(b)
			}
			if anchor != "" && strings.HasSuffix(targetPath, ".md") && !doccheck.Anchors(targetDoc)[anchor] {
				t.Errorf("%s links to %q, but %s has no such heading", file, link, targetPath)
			}
		}
	}
}

func TestDocsIndexLinksEveryPage(t *testing.T) {
	index := readFile(t, filepath.Join(repoRoot, "docs/README.md"))
	linked := doccheck.Links(index)

	for _, file := range markdownFiles(t) {
		rel, err := filepath.Rel(filepath.Join(repoRoot, "docs"), file)
		if err != nil || strings.HasPrefix(rel, "..") || rel == "README.md" {
			continue
		}
		if !slices.Contains(linked, filepath.ToSlash(rel)) {
			t.Errorf("docs/README.md does not link to docs/%s", rel)
		}
	}
}
