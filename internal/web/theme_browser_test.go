package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// These tests load the real login page and the real theme.js and app.js in headless Chrome, with
// a stubbed system preference and storage, and check what the scripts do. They are skipped
// when no Chrome or Chromium is installed.

var browserNames = []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"}

func findBrowser(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("browser tests are skipped with -short")
	}
	for _, name := range browserNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("no Chrome or Chromium found")
	return ""
}

const stubScript = `<script>
window.__system = "light"; window.__listeners = [];
window.matchMedia = function (q) {
  return { media: q, get matches() { return window.__system === "dark"; },
    addEventListener: function (type, fn) { window.__listeners.push(fn); }, removeEventListener: function () {} };
};
window.__setSystem = function (v) { window.__system = v; window.__listeners.forEach(function (fn) { fn({ matches: v === "dark" }); }); };
try { localStorage.clear(); } catch (e) {}
%s
</script>`

const earlyScript = `<script>window.__early = document.documentElement.getAttribute("data-theme");</script>`

const driverScript = `<script>window.addEventListener("load", function () {
  var click = function () { document.querySelector("[data-theme-toggle]").click(); };
  %s
  var root = document.documentElement, toggle = document.querySelector("[data-theme-toggle]");
  var saved = null; try { saved = localStorage.getItem("wasabot-theme"); } catch (e) {}
  var out = { early: window.__early, theme: root.getAttribute("data-theme"), pref: root.getAttribute("data-theme-pref"),
    checked: toggle.getAttribute("aria-checked"), saved: saved,
    color: document.querySelector('meta[name="theme-color"]').getAttribute("content") };
  var pre = document.createElement("pre"); pre.id = "out"; pre.textContent = JSON.stringify(out); document.body.appendChild(pre);
});</script>`

type themeState struct {
	Early   string  `json:"early"`
	Theme   string  `json:"theme"`
	Pref    string  `json:"pref"`
	Checked string  `json:"checked"`
	Saved   *string `json:"saved"`
	Color   string  `json:"color"`
}

var outPre = regexp.MustCompile(`(?s)<pre id="out">(.*?)</pre>`)

// runTheme loads the login page with the given setup and steps and returns what the page ended up as.
func runTheme(t *testing.T, browser, setup, steps string) themeState {
	t.Helper()
	f := newFixture(t)
	page := f.anon().get("/login").Body.String()
	if !strings.Contains(page, `<script src="/static/js/theme.js"></script>`) || !strings.Contains(page, "</body>") {
		t.Fatal("unexpected login page")
	}
	page = strings.Replace(page, "<head>", "<head>"+strings.Replace(stubScript, "%s", setup, 1), 1)
	page = strings.Replace(page, `<script src="/static/js/theme.js"></script>`, `<script src="/static/js/theme.js"></script>`+earlyScript, 1)
	page = strings.Replace(page, "</body>", strings.Replace(driverScript, "%s", steps, 1)+"</body>", 1)

	// the page is served without the CSP, so the injected scripts may run; assets come from the real handler
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
	})
	mux.Handle("/", f.srv.Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, browser, "--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=4000", "--dump-dom", srv.URL+"/test")
	dom, err := cmd.Output()
	if err != nil {
		t.Fatalf("browser: %v", err)
	}

	m := outPre.FindSubmatch(dom)
	if m == nil {
		t.Fatalf("the page did not report a result; the scripts probably failed:\n%.600s", dom)
	}
	var got themeState
	if err := json.Unmarshal(m[1], &got); err != nil {
		t.Fatalf("result %q: %v", m[1], err)
	}
	return got
}

func TestTheme_InABrowser(t *testing.T) {
	browser := findBrowser(t)
	str := func(s string) *string { return &s }

	tests := []struct {
		name         string
		setup, steps string
		want         themeState
	}{
		{
			name: "follows a light system by default",
			want: themeState{Early: "light", Theme: "light", Pref: "auto", Checked: "false", Color: "#eeede4"},
		},
		{
			name:  "follows a dark system by default",
			setup: `window.__system = "dark";`,
			want:  themeState{Early: "dark", Theme: "dark", Pref: "auto", Checked: "true", Color: "#161915"},
		},
		{
			name:  "a saved dark choice beats a light system",
			setup: `localStorage.setItem("wasabot-theme", "dark");`,
			want:  themeState{Early: "dark", Theme: "dark", Pref: "dark", Checked: "true", Saved: str("dark"), Color: "#161915"},
		},
		{
			name:  "a saved light choice beats a dark system",
			setup: `window.__system = "dark"; localStorage.setItem("wasabot-theme", "light");`,
			want:  themeState{Early: "light", Theme: "light", Pref: "light", Checked: "false", Saved: str("light"), Color: "#eeede4"},
		},
		{
			name:  "an unknown saved value is ignored",
			setup: `window.__system = "dark"; localStorage.setItem("wasabot-theme", "neon");`,
			want:  themeState{Early: "dark", Theme: "dark", Pref: "auto", Checked: "true", Saved: str("neon"), Color: "#161915"},
		},
		{
			name:  "clicking on a light system switches to dark and remembers it",
			steps: `click();`,
			want:  themeState{Early: "light", Theme: "dark", Pref: "dark", Checked: "true", Saved: str("dark"), Color: "#161915"},
		},
		{
			name:  "clicking back to what the system has returns to following it",
			steps: `click(); click();`,
			want:  themeState{Early: "light", Theme: "light", Pref: "auto", Checked: "false", Color: "#eeede4"},
		},
		{
			name:  "clicking on a dark system switches to light and remembers it",
			setup: `window.__system = "dark";`,
			steps: `click();`,
			want:  themeState{Early: "dark", Theme: "light", Pref: "light", Checked: "false", Saved: str("light"), Color: "#eeede4"},
		},
		{
			name:  "a system change is followed while nothing is chosen",
			steps: `window.__setSystem("dark");`,
			want:  themeState{Early: "light", Theme: "dark", Pref: "auto", Checked: "true", Color: "#161915"},
		},
		{
			name:  "a system change is ignored after an explicit choice",
			steps: `click(); window.__setSystem("dark"); window.__setSystem("light");`,
			want:  themeState{Early: "light", Theme: "dark", Pref: "dark", Checked: "true", Saved: str("dark"), Color: "#161915"},
		},
		{
			name:  "another tab choosing dark is picked up",
			steps: `localStorage.setItem("wasabot-theme", "dark"); window.dispatchEvent(new StorageEvent("storage", { key: "wasabot-theme" }));`,
			want:  themeState{Early: "light", Theme: "dark", Pref: "dark", Checked: "true", Saved: str("dark"), Color: "#161915"},
		},
		{
			name:  "another tab going back to the system is picked up",
			setup: `localStorage.setItem("wasabot-theme", "dark");`,
			steps: `localStorage.removeItem("wasabot-theme"); window.dispatchEvent(new StorageEvent("storage", { key: "wasabot-theme" }));`,
			want:  themeState{Early: "dark", Theme: "light", Pref: "auto", Checked: "false", Color: "#eeede4"},
		},
		{
			name:  "still works when storage is blocked",
			setup: `Object.defineProperty(window, "localStorage", { get: function () { throw new Error("denied"); } });`,
			steps: `click();`,
			want:  themeState{Early: "light", Theme: "dark", Pref: "dark", Checked: "true", Color: "#161915"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := runTheme(t, browser, tt.setup, tt.steps)

			want := tt.want
			if (got.Saved == nil) != (want.Saved == nil) || (got.Saved != nil && *got.Saved != *want.Saved) {
				t.Errorf("saved = %v, want %v", deref(got.Saved), deref(want.Saved))
			}
			got.Saved, want.Saved = nil, nil
			if got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nothing>"
	}
	return *s
}
