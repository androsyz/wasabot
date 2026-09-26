package web

import (
	"encoding/xml"
	"slices"
	"strings"
	"testing"
)

const palette = "kglpdwbyrncsitBHDuv."

func TestSprites_AreWellFormed(t *testing.T) {
	for name, rows := range sprites {
		if len(rows) == 0 {
			t.Errorf("%s has no rows", name)
			continue
		}
		for y, row := range rows {
			if len(row) != len(rows[0]) {
				t.Errorf("%s: row %d is %d wide, row 0 is %d", name, y, len(row), len(rows[0]))
			}
			for _, c := range []byte(row) {
				if !strings.ContainsRune(palette, rune(c)) {
					t.Errorf("%s: row %d uses %q, which has no color", name, y, c)
				}
			}
		}
	}
}

func TestSprites_Sizes(t *testing.T) {
	for name, rows := range sprites {
		switch {
		case name == "toggle-track":
			if len(rows) != toggleTrackH || len(rows[0]) != toggleTrackW {
				t.Errorf("%s is %dx%d", name, len(rows[0]), len(rows))
			}
		case strings.HasPrefix(name, "toggle-"):
			if len(rows) != toggleThumb || len(rows[0]) != toggleThumb {
				t.Errorf("%s is %dx%d", name, len(rows[0]), len(rows))
			}
		case strings.HasPrefix(name, "mascot"):
			if len(rows) != mascotH || len(rows[0]) != mascotW {
				t.Errorf("%s is %dx%d, want %dx%d", name, len(rows[0]), len(rows), mascotW, mascotH)
			}
		default:
			if len(rows) != iconSize || len(rows[0]) != iconSize {
				t.Errorf("%s is %dx%d, want %dx%d so it scales to whole pixels", name, len(rows[0]), len(rows), iconSize, iconSize)
			}
		}
	}
}

func TestSprites_EveryPageSpriteExists(t *testing.T) {
	// the names templates and views use; a typo here would only show up as a 500 at runtime
	want := []string{
		"mascot", "mascot-wave", "mascot-cover", "mascot-worry", "mascot-think", "mascot-sleepy",
		"icon-clients", "icon-agents", "icon-conversations", "icon-playground", "icon-analytics", "icon-settings",
		"icon-search", "icon-sun", "icon-moon", "icon-logout",
		"toggle-track", "toggle-sun", "toggle-moon",
	}
	for _, a := range avatars {
		want = append(want, a.Sprite)
	}
	for _, name := range want {
		if _, ok := sprites[name]; !ok {
			t.Errorf("sprite %q does not exist", name)
		}
	}
}

func TestMascotExpressionsDiffer(t *testing.T) {
	names := []string{"mascot", "mascot-wave", "mascot-cover", "mascot-worry", "mascot-think", "mascot-sleepy"}
	for i, a := range names {
		for _, b := range names[i+1:] {
			if slices.Equal(sprites[a], sprites[b]) {
				t.Errorf("%s and %s are identical", a, b)
			}
		}
	}
}

func TestSpriteHTML(t *testing.T) {
	html, err := spriteHTML("icon-clients", "sprite-icon")
	if err != nil {
		t.Fatalf("spriteHTML: %v", err)
	}

	out := string(html)
	for _, want := range []string{`<svg class="px sprite-icon"`, `viewBox="0 0 16 16"`, `shape-rendering="crispEdges"`, `aria-hidden="true"`, `class="p-g"`, `class="p-i"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %s", want)
		}
	}
	if err := xml.Unmarshal([]byte(out), new(struct{})); err != nil {
		t.Errorf("not well-formed: %v", err)
	}
}

func TestSpriteHTML_JoinsRunsAndEscapesTheClass(t *testing.T) {
	sprites["test-runs"] = []string{"ggg.gg"}
	t.Cleanup(func() { delete(sprites, "test-runs") })

	html, err := spriteHTML("test-runs", `"><script>`)
	if err != nil {
		t.Fatalf("spriteHTML: %v", err)
	}

	out := string(html)
	if strings.Count(out, "<rect") != 2 || !strings.Contains(out, `x="0" y="0" width="3"`) || !strings.Contains(out, `x="4" y="0" width="2"`) {
		t.Errorf("runs of one color should become one rect: %s", out)
	}
	if strings.Contains(out, "<script>") {
		t.Errorf("the class argument must be escaped: %s", out)
	}
}

func TestSpriteHTML_UnknownSprite(t *testing.T) {
	if _, err := spriteHTML("nope", ""); err == nil {
		t.Fatal("want an error for an unknown sprite")
	}
}

func TestFaviconSVG(t *testing.T) {
	svg, err := faviconSVG("mascot")
	if err != nil {
		t.Fatalf("favicon: %v", err)
	}

	if err := xml.Unmarshal(svg, new(struct{})); err != nil {
		t.Fatalf("not well-formed: %v", err)
	}
	if !strings.Contains(string(svg), `fill="#84c341"`) || strings.Contains(string(svg), "class=") {
		t.Fatalf("a favicon needs fixed colors, it cannot use the page CSS: %.200s", svg)
	}
	if _, err := faviconSVG("nope"); err == nil {
		t.Fatal("want an error for an unknown sprite")
	}
}
