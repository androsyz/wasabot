package web

import (
	"regexp"
	"strings"
	"testing"
)

func TestEveryPageHasOneAccessibleThemeToggle(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Preview = true })
	fresh := newFreshFixture(t)

	pages := map[string]string{"/clients": f.get("/clients").Body.String(), "/setup": fresh.get("/setup").Body.String()}
	for _, path := range []string{"/login", "/forgot", "/forgot/code", "/preview/invite", "/nothing"} {
		pages[path] = f.anon().get(path).Body.String()
	}

	for path, body := range pages {
		if n := strings.Count(body, "data-theme-toggle"); n != 1 {
			t.Errorf("%s has %d theme toggles, want exactly one", path, n)
			continue
		}
		for _, want := range []string{`role="switch"`, `aria-checked="`, `aria-label="Dark mode"`, "toggle-track", "toggle-sun", "toggle-moon"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the toggle is missing %s", path, want)
			}
		}
		for _, old := range []string{"theme-btn", "theme-switch", "data-theme-set"} {
			if strings.Contains(body, old) {
				t.Errorf("%s still uses the old theme control (%s)", path, old)
			}
		}
	}
}

func TestPagesTellTheBrowserAboutBothColorSchemes(t *testing.T) {
	body := newFixture(t).get("/clients").Body.String()

	if !strings.Contains(body, `<meta name="color-scheme" content="light dark">`) {
		t.Error("color-scheme lets scrollbars and form controls match the theme before the CSS loads")
	}
	if !strings.Contains(body, `<meta name="theme-color" content="#eeede4">`) {
		t.Error("theme-color colors the mobile browser bar")
	}
}

func TestThemeScriptRunsBeforeThePagePaints(t *testing.T) {
	body := newFixture(t).get("/clients").Body.String()

	head := body[strings.Index(body, "<head>"):strings.Index(body, "</head>")]
	theme := regexp.MustCompile(`<script src="/static/js/theme\.js"([^>]*)></script>`).FindStringSubmatch(head)
	if theme == nil {
		t.Fatal("theme.js must be loaded in <head>")
	}
	if strings.Contains(theme[1], "defer") || strings.Contains(theme[1], "async") {
		t.Errorf("theme.js must block rendering, or the page flashes the wrong theme first: %q", theme[1])
	}
	if !strings.Contains(head, `<script src="/static/js/app.js" defer></script>`) {
		t.Error("app.js should be deferred")
	}
}

func TestThemeStyles(t *testing.T) {
	css := newFixture(t).get("/static/css/app.css").Body.String()

	for _, want := range []string{
		`:root[data-theme="dark"]`, "color-scheme: dark", "color-scheme: light",
		"prefers-reduced-motion", ".toggle-thumb", "--px-B", "--px-t",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css does not contain %q", want)
		}
	}
	if strings.Contains(css, "prefers-color-scheme") {
		t.Error("the system preference is read in JavaScript, so there is one source of truth for the theme")
	}
}
