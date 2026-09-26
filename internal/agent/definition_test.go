package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadDefinition_Sample(t *testing.T) {
	def, err := LoadDefinition("testdata/agent.md")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if def.Name != "Wasabi" || def.Model != "gpt-4o-mini" || def.Language != "id" {
		t.Errorf("header = %+v", def)
	}
	if !reflect.DeepEqual(def.Tools, []string{"current_time"}) {
		t.Errorf("tools = %v", def.Tools)
	}
	if !strings.HasPrefix(def.Prompt, "You are Wasabi") || !strings.HasSuffix(def.Prompt, "instead of guessing.") {
		t.Errorf("prompt = %q", def.Prompt)
	}
}

func TestLoadDefinition_MissingFile(t *testing.T) {
	if _, err := LoadDefinition("testdata/nope.md"); err == nil {
		t.Fatal("want an error")
	}
}

func TestParseDefinition(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Definition
	}{
		{
			name: "minimal",
			in:   "---\nname: Bot\n---\nHello.",
			want: Definition{Name: "Bot", Prompt: "Hello."},
		},
		{
			name: "crlf line endings",
			in:   "---\r\nname: Bot\r\nmodel: m\r\n---\r\nLine one.\r\nLine two.",
			want: Definition{Name: "Bot", Model: "m", Prompt: "Line one.\nLine two."},
		},
		{
			name: "quoted values and bracketed tool list",
			in:   "---\nname: \"Toko Budi\"\ntools: [a, 'b',  c ]\n---\nHi",
			want: Definition{Name: "Toko Budi", Tools: []string{"a", "b", "c"}, Prompt: "Hi"},
		},
		{
			name: "blank lines and comments in the header",
			in:   "---\n\n# a comment\nname: Bot\n\n---\nHi",
			want: Definition{Name: "Bot", Prompt: "Hi"},
		},
		{
			name: "colon in the value and a fence-like line in the body",
			in:   "---\nname: Bot: v2\n---\nBefore\n---\nAfter",
			want: Definition{Name: "Bot: v2", Prompt: "Before\n---\nAfter"},
		},
		{
			name: "empty tools value",
			in:   "---\nname: Bot\ntools:\n---\nHi",
			want: Definition{Name: "Bot", Prompt: "Hi"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDefinition([]byte(tt.in))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseDefinition_Errors(t *testing.T) {
	tests := []struct {
		name, in, wantErr string
	}{
		{"empty file", "", `line 1: expected "---"`},
		{"no opening fence", "name: Bot\n---\nHi", `line 1: expected "---"`},
		{"unclosed frontmatter", "---\nname: Bot\nHi", "not closed"},
		{"line without a colon", "---\nname Bot\n---\nHi", `line 2: expected "key: value"`},
		{"unknown key", "---\nname: Bot\nmodle: m\n---\nHi", `line 3: unknown key "modle"`},
		{"duplicate key", "---\nname: A\nname: B\n---\nHi", `line 3: duplicate key "name"`},
		{"missing name", "---\nmodel: m\n---\nHi", "name is required"},
		{"empty prompt", "---\nname: Bot\n---\n  \n", "prompt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDefinition([]byte(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}
