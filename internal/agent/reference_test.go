package agent

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/androsyz/wasabot/internal/doccheck"
)

const referencePath = "../../docs/reference/agent-file.md"

func readReference(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("read the reference: %v", err)
	}
	return string(doc)
}

// A key added to the parser without documentation, or documented but not accepted, fails here.
func TestReferenceDocumentsExactlyTheKeysTheParserAccepts(t *testing.T) {
	section, ok := doccheck.Section(readReference(t), "### Keys")
	if !ok {
		t.Fatal("the reference has no ### Keys section")
	}

	documented := doccheck.TableFirstColumn(section)
	accepted := slices.Sorted(maps.Keys(keySetters))

	slices.Sort(documented)
	if !slices.Equal(documented, accepted) {
		t.Fatalf("documented keys %v, accepted keys %v", documented, accepted)
	}
}

// Each documented startup error must be what wasabot really says, and each real one must be documented.
func TestReferenceStartupErrorsMatchTheRealMessages(t *testing.T) {
	registered := []Tool{constTool("current_time", "")}
	newAgent := func(def Definition, model string) error {
		_, err := New(Options{LLM: &scriptedLLM{}, Definition: def, DefaultModel: model, Tools: registered})
		return err
	}
	parse := func(input string) error {
		_, err := ParseDefinition([]byte(input))
		return err
	}

	cases := map[string]error{
		"to start the frontmatter":              parse("name: x\n---\nHi"),
		"is not closed":                         parse("---\nname: x\nHi"),
		`expected "key: value"`:                 parse("---\nname x\n---\nHi"),
		"unknown key":                           parse("---\nname: x\nmodle: y\n---\nHi"),
		"duplicate key":                         parse("---\nname: x\nname: y\n---\nHi"),
		"name is required":                      parse("---\nmodel: m\n---\nHi"),
		"prompt after the frontmatter is empty": parse("---\nname: x\n---\n \n"),
		"unknown tool":                          newAgent(Definition{Name: "x", Tools: []string{"launch_missiles"}}, "m"),
		"no model":                              newAgent(Definition{Name: "x"}, ""),
		"read agent definition":                 func() error { _, err := LoadDefinition("testdata/does-not-exist.md"); return err }(),
	}
	for phrase, err := range cases {
		if err == nil || !strings.Contains(err.Error(), phrase) {
			t.Errorf("expected an error containing %q, got %v", phrase, err)
		}
	}

	section, ok := doccheck.Section(readReference(t), "## Startup errors")
	if !ok {
		t.Fatal("the reference has no ## Startup errors section")
	}
	documented := doccheck.TableFirstColumn(section)
	for _, phrase := range documented {
		if _, ok := cases[phrase]; !ok {
			t.Errorf("the reference documents %q, but no test produces it", phrase)
		}
	}
	for phrase := range cases {
		if !slices.Contains(documented, phrase) {
			t.Errorf("wasabot can fail with %q but the reference does not list it", phrase)
		}
	}
}
