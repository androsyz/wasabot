package doccheck

import (
	"reflect"
	"strings"
	"testing"
)

const sample = "# Title\n\n## Tools\n\n| Tool | What |\n|---|---|\n| `a` | first |\n| `b` | second |\n\ntext after\n\n### Sub\n\nin sub\n\n## Other\n\nlast\n\n```markdown\n---\nname: x\n---\nbody\n```\n\n```bash\nls\n```\n\nA [link](docs/a.md#part) and [web](https://example.com).\n\n```text\n[ignored](nope.md)\n```\n"

func TestSection(t *testing.T) {
	got, ok := Section(sample, "## Tools")
	if !ok || got == "" {
		t.Fatal("section not found")
	}
	if !strings.Contains(got, "| `a` |") || !strings.Contains(got, "### Sub") || strings.Contains(got, "last") {
		t.Fatalf("a section runs to the next heading of the same level, so it keeps its subsections:\n%s", got)
	}

	sub, _ := Section(sample, "### Sub")
	if strings.Contains(sub, "last") || !strings.Contains(sub, "in sub") {
		t.Fatalf("got %q", sub)
	}
	if _, ok := Section(sample, "## Missing"); ok {
		t.Fatal("a missing section must be reported")
	}
}

func TestSection_IgnoresHeadingsInsideCodeBlocks(t *testing.T) {
	md := "## A\n\n```\n## Not a heading\n```\n\nstill A\n\n## B\n"

	got, _ := Section(md, "## A")

	if !strings.Contains(got, "still A") {
		t.Fatalf("got %q", got)
	}
}

func TestTableFirstColumn(t *testing.T) {
	got := TableFirstColumn(sample)

	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := TableFirstColumn("no table here"); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestCodeBlocks(t *testing.T) {
	got := CodeBlocks(sample, "markdown")

	if len(got) != 1 || got[0] != "---\nname: x\n---\nbody" {
		t.Fatalf("got %q", got)
	}
	if got := CodeBlocks(sample, "bash"); len(got) != 1 || got[0] != "ls" {
		t.Fatalf("got %q", got)
	}
}

func TestLinks(t *testing.T) {
	got := Links(sample)

	if !reflect.DeepEqual(got, []string{"docs/a.md#part", "https://example.com"}) {
		t.Fatalf("links inside code blocks are ignored: got %v", got)
	}
}

func TestAnchors(t *testing.T) {
	a := Anchors("# Agent file reference\n\n## Startup errors\n\n### What's `this`?\n\n```\n# not a heading\n```\n")

	for _, want := range []string{"agent-file-reference", "startup-errors", "whats-this"} {
		if !a[want] {
			t.Errorf("missing anchor %q in %v", want, a)
		}
	}
	if a["not-a-heading"] {
		t.Error("a comment inside a code block is not a heading")
	}
}
