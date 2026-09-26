package web

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/db/dbtest"
	"github.com/androsyz/wasabot/internal/manager"
	"github.com/androsyz/wasabot/internal/store"
)

var (
	discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))
	testNow    = time.Date(2026, 9, 27, 12, 0, 0, 0, time.Local)
)

// stubRuntime reports fixed statuses and records what the dashboard asked of it.
type stubRuntime struct {
	mu       sync.Mutex
	statuses map[int64]manager.Status
	pairing  map[int64]manager.Pairing
	calls    []string
	err      error // returned by Start, Logout and Pair
}

func (r *stubRuntime) record(call string, id int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fmt.Sprintf("%s %d", call, id))
}

func (r *stubRuntime) Called() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *stubRuntime) Status(id int64) manager.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statuses[id]
}

func (r *stubRuntime) Start(id int64) error                     { r.record("start", id); return r.err }
func (r *stubRuntime) Stop(id int64)                            { r.record("stop", id) }
func (r *stubRuntime) Logout(_ context.Context, id int64) error { r.record("logout", id); return r.err }
func (r *stubRuntime) Pair(id int64) error {
	r.record("pair", id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		if r.pairing == nil {
			r.pairing = map[int64]manager.Pairing{}
		}
		if _, ok := r.pairing[id]; !ok {
			r.pairing[id] = manager.Pairing{}
		}
	}
	return r.err
}
func (r *stubRuntime) Pairing(id int64) (manager.Pairing, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.pairing[id]
	return p, ok
}
func (r *stubRuntime) CancelPair(id int64) {
	r.record("cancel", id)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pairing, id)
}

const (
	adminPassword = "correct horse battery"
	setupCode     = "setup-code-for-tests"
)

// cheap argon2 settings keep the tests fast; the algorithm is unchanged
var testHasher = auth.NewHasher(auth.Params{Memory: 8, Time: 1, Threads: 1})

type seededClient struct {
	name, jid string
	today     int
	month     int // in addition to today's
}

func clientsToSeed(seed bool) []seededClient {
	if !seed {
		return nil
	}
	return []seededClient{
		{"Toko Kopi Senja", "6281134567890:7@s.whatsapp.net", 2, 1},
		{"Klinik Gigi Sehat", "6281234567890:3@s.whatsapp.net", 0, 0},
		{"Laundry Bersih", "6281334567890:2@s.whatsapp.net", 1, 0},
		{"Kos Melati", "", 0, 0},
	}
}

type fixture struct {
	srv    *Server
	st     *store.Store
	auth   *auth.Service
	sql    *sql.DB
	admin  store.User
	cookie string // the admin's session cookie, sent unless anon() is used
	csrf   string // the admin's session CSRF token
}

// newFixture has an admin logged in, and four clients like the dashboard design: connected,
// pairing, stopped and logged out.
func newFixture(t *testing.T, mods ...func(*Options)) *fixture {
	t.Helper()
	return build(t, true, true, mods...)
}

// newEmptyFixture has an admin but no clients yet.
func newEmptyFixture(t *testing.T, mods ...func(*Options)) *fixture {
	t.Helper()
	return build(t, true, false, mods...)
}

// newFreshFixture has no users at all: the state before first-run setup.
func newFreshFixture(t *testing.T, mods ...func(*Options)) *fixture {
	t.Helper()
	return build(t, false, false, mods...)
}

func build(t *testing.T, withAdmin, seed bool, mods ...func(*Options)) *fixture {
	t.Helper()
	ctx := context.Background()
	sqlDB := dbtest.New(t)
	st := store.New(sqlDB)
	svc, err := auth.NewService(st, auth.Options{Hasher: testHasher, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}

	for _, s := range clientsToSeed(seed) {
		c, err := st.Clients.Create(ctx, s.name)
		if err != nil {
			t.Fatalf("create client: %v", err)
		}
		if s.jid != "" {
			if _, err := st.WhatsAppSessions.Link(ctx, c.ID, s.jid); err != nil {
				t.Fatalf("link: %v", err)
			}
		}
		add := func(n int, at time.Time) {
			for i := range n {
				_, err := st.Messages.Create(ctx, store.Message{
					ClientID: c.ID, WAID: s.name + at.String() + string(rune('a'+i)), Chat: "c",
					Direction: store.DirectionIn, Body: "x", CreatedAt: at,
				})
				if err != nil {
					t.Fatalf("seed message: %v", err)
				}
			}
		}
		add(s.today, testNow.Add(-3*time.Hour))
		add(s.month, time.Date(2026, 9, 3, 9, 0, 0, 0, time.Local))
		add(5, time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local)) // last month: never counted
	}

	opts := Options{
		Log:       discardLog,
		Store:     st,
		Auth:      svc,
		SetupCode: setupCode,
		Now:       func() time.Time { return testNow },
		Agent:     "'agent.md' | test-model",
		Runtime: &stubRuntime{statuses: map[int64]manager.Status{
			1: manager.StatusConnected, 2: manager.StatusPairing, 3: manager.StatusStopped, 4: manager.StatusLoggedOut,
		}},
	}
	for _, mod := range mods {
		mod(&opts)
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	f := &fixture{srv: srv, st: st, auth: svc, sql: sqlDB}
	if withAdmin {
		login, err := svc.CreateFirstAdmin(ctx, "Rina", "rina@example.com", adminPassword, adminPassword)
		if err != nil {
			t.Fatalf("create admin: %v", err)
		}
		f.admin, f.cookie, f.csrf = login.User, sessionCookie+"="+login.Token, login.CSRFToken
	}
	return f
}

// anon is the same server without the admin's cookie.
func (f *fixture) anon() *fixture {
	c := *f
	c.cookie = ""
	return &c
}

func (f *fixture) do(method, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if f.cookie != "" {
		req.Header.Set("Cookie", f.cookie)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func (f *fixture) get(path string, headers ...string) *httptest.ResponseRecorder {
	return f.do(http.MethodGet, path, headers...)
}

func TestRoutes(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name         string
		anon         bool
		method, path string
		wantStatus   int
		wantBody     string
	}{
		{"dashboard", false, "GET", "/clients", 200, "Clients Overview"},
		{"login page", true, "GET", "/login", 200, "Log in to wasabot"},
		{"forgot page", true, "GET", "/forgot", 200, "Forgot password"},
		{"code page", true, "GET", "/forgot/code", 200, "Enter your 6-digit code"},
		{"health check", true, "GET", "/healthz", 200, "ok"},
		{"invite with an unknown token", true, "GET", "/invite/some-token", 404, "This invitation is not valid"},
		{"preview is off by default", true, "GET", "/preview/invite", 404, "Page not found"},
		{"unknown page", true, "GET", "/nothing-here", 404, "Page not found"},
		{"forgot submit is not built", true, "POST", "/forgot", 501, "Password reset is not available yet."},
		{"code submit is not built", true, "POST", "/forgot/code", 501, "Code verification is not available yet."},
		{"the dashboard takes no PUT", false, "PUT", "/clients", 405, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := f
			if tt.anon {
				srv = f.anon()
			}

			rec := srv.do(tt.method, tt.path)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body does not contain %q:\n%s", tt.wantBody, rec.Body.String())
			}
		})
	}
}

func TestRoot_RedirectsToTheDashboard(t *testing.T) {
	rec := newFixture(t).get("/")

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/clients" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestPreviewRoutesOnlyExistWhenEnabled(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Preview = true })

	rec := f.anon().get("/preview/invite")

	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Join Toko Kopi Senja on wasabot") ||
		!strings.Contains(rec.Body.String(), "Invited by Rina as Client user") {
		t.Fatalf("status %d:\n%s", rec.Code, rec.Body.String())
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	f := newFixture(t)

	for _, path := range []string{"/clients", "/login", "/nothing", "/static/css/app.css", "/favicon.svg", "/healthz"} {
		h := f.get(path).Header()
		if h.Get("Content-Security-Policy") != contentSecurityPolicy {
			t.Errorf("%s: CSP = %q", path, h.Get("Content-Security-Policy"))
		}
		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("X-Frame-Options") != "DENY" || h.Get("Referrer-Policy") != "same-origin" {
			t.Errorf("%s: missing a hardening header: %v", path, h)
		}
	}
}

// The CSP forbids inline scripts, styles and event handlers, so no page may rely on them.
func TestPagesUseNoInlineCode(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Preview = true })
	fresh := newFreshFixture(t)
	inlineScript := regexp.MustCompile(`<script(?:\s[^>]*)?>`)
	handler := regexp.MustCompile(`\son[a-z]+\s*=`)

	pages := map[string]string{}
	for _, path := range []string{"/clients"} {
		pages[path] = f.get(path).Body.String()
	}
	for _, path := range []string{"/login", "/forgot", "/forgot/code", "/preview/invite", "/nothing"} {
		pages[path] = f.anon().get(path).Body.String()
	}
	pages["/setup"] = fresh.get("/setup?token=" + setupCode).Body.String()
	pages["/setup (no link)"] = fresh.get("/setup").Body.String()

	for path, body := range pages {
		if len(body) < 200 {
			t.Fatalf("%s did not render a page: %q", path, body)
		}

		if strings.Contains(body, ` style="`) || strings.Contains(body, "<style") {
			t.Errorf("%s uses inline styles", path)
		}
		for _, tag := range inlineScript.FindAllString(body, -1) {
			if !strings.Contains(tag, " src=") {
				t.Errorf("%s has an inline script: %s", path, tag)
			}
		}
		if handler.MatchString(body) {
			t.Errorf("%s has an inline event handler", path)
		}
	}
}

func TestStaticAssets(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		path, contentType, cache string
	}{
		{"/static/css/app.css", "text/css", "no-cache"},
		{"/static/js/app.js", "text/javascript", "no-cache"},
		{"/static/js/theme.js", "text/javascript", "no-cache"},
		{"/static/js/htmx.min.js", "text/javascript", "no-cache"},
		{"/static/fonts/jetbrains-mono-800.woff2", "font/woff2", "immutable"},
	}
	for _, tt := range tests {
		rec := f.get(tt.path)

		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), tt.contentType) {
			t.Errorf("%s: status %d, content type %q", tt.path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), tt.cache) {
			t.Errorf("%s: Cache-Control = %q, want %q", tt.path, rec.Header().Get("Cache-Control"), tt.cache)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s is empty", tt.path)
		}
	}
	if rec := f.get("/static/nope.css"); rec.Code != 404 {
		t.Errorf("a missing asset must be 404, got %d", rec.Code)
	}
}

func TestFavicon(t *testing.T) {
	rec := newFixture(t).get("/favicon.svg")

	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(rec.Body.String(), "<svg") {
		t.Fatalf("status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestPanicsBecomeA500(t *testing.T) {
	h := recoverPanics(discardLog, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("status %d, body %q: the panic value must not reach the client", rec.Code, rec.Body.String())
	}
}

func TestStartServesAndShutsDown(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Addr = "127.0.0.1:0" })
	if err := f.srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	resp, err := http.Get("http://" + f.srv.Addr() + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("status %d, body %q", resp.StatusCode, body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := http.Get("http://" + f.srv.Addr() + "/healthz"); err == nil {
		t.Fatal("the server must refuse connections after shutdown")
	}
}

func TestStartReportsABusyPort(t *testing.T) {
	first := newFixture(t, func(o *Options) { o.Addr = "127.0.0.1:0" })
	if err := first.srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { first.srv.Shutdown(context.Background()) })

	second := newFixture(t, func(o *Options) { o.Addr = first.srv.Addr() })

	err := second.srv.Start()

	if err == nil || !strings.Contains(err.Error(), "listen on "+first.srv.Addr()) {
		t.Fatalf("got %v, want an error naming the address", err)
	}
}
