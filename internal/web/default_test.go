package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// newDefaultFixture is a fresh install that made the admin/admin account, with clients to protect.
func newDefaultFixture(t *testing.T) *fixture {
	t.Helper()
	f := build(t, false, true)
	if created, err := f.auth.EnsureDefaultAdmin(context.Background()); err != nil || !created {
		t.Fatalf("default admin: %v, %v", created, err)
	}
	return f
}

// signInAsDefault logs in with admin/admin and returns the fixture holding that session.
func signInAsDefault(t *testing.T, f *fixture) *fixture {
	t.Helper()
	rec := f.login(t, "admin", "admin", nil)
	session := cookieNamed(rec, sessionCookie)
	if rec.Code != http.StatusSeeOther || session == nil {
		t.Fatalf("admin/admin login: %d", rec.Code)
	}
	g := f.anon()
	g.cookie = sessionCookie + "=" + session.Value
	page := g.get("/welcome")
	m := hiddenCSRF.FindStringSubmatch(page.Body.String())
	if page.Code != 200 || m == nil {
		t.Fatalf("welcome page: %d", page.Code)
	}
	g.csrf = m[1]
	return g
}

func TestDefaultLogin_TheLoginPageSaysHowToGetIn(t *testing.T) {
	f := newDefaultFixture(t)

	page := f.anon().get("/login").Body.String()
	failed := f.login(t, "admin", "not the password", nil).Body.String()

	for name, body := range map[string]string{"the login page": page, "a failed login": failed} {
		if !strings.Contains(body, `id="default-login"`) || !strings.Contains(body, "<strong>admin</strong> / <strong>admin</strong>") {
			t.Errorf("%s should explain the default login", name)
		}
	}
	if strings.Contains(newFixture(t).anon().get("/login").Body.String(), "default-login") {
		t.Error("an install with a real admin must not advertise admin/admin")
	}
}

func TestDefaultLogin_TheSignInAcceptsANameThatIsNotAnEmail(t *testing.T) {
	f := newDefaultFixture(t)

	if body := f.anon().get("/login").Body.String(); strings.Contains(body, `type="email" name="email"`) {
		t.Fatal("the login field is plain text: a browser would refuse 'admin' as an email")
	}
	if rec := f.login(t, "Admin", "admin", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestDefaultLogin_EveryPageSendsYouToChooseNewCredentials(t *testing.T) {
	f := signInAsDefault(t, newDefaultFixture(t))

	for _, path := range []string{"/clients", "/clients?q=a&status=stopped", "/"} {
		rec := f.get(path)

		if rec.Code >= 400 || (rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound) {
			t.Errorf("%s: %d", path, rec.Code)
		}
		if rec.Code == http.StatusSeeOther && rec.Header().Get("Location") != "/welcome" {
			t.Errorf("%s redirects to %q, want /welcome", path, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), "Toko Kopi Senja") {
			t.Errorf("%s: client data must not be served before the credentials are changed", path)
		}
	}
	htmx := f.get("/clients", "HX-Request", "true", "HX-Target", "clients-results")
	if htmx.Code != http.StatusSeeOther || strings.Contains(htmx.Body.String(), "<table") {
		t.Errorf("an htmx table refresh must not work either: %d", htmx.Code)
	}
	if rec := f.get("/login"); rec.Code != http.StatusSeeOther {
		t.Errorf("already signed in: %d", rec.Code)
	}
}

func TestDefaultLogin_WhatStillWorksBeforeTheChange(t *testing.T) {
	f := signInAsDefault(t, newDefaultFixture(t))

	welcome := f.get("/welcome")
	if welcome.Code != 200 || !strings.Contains(welcome.Body.String(), "Secure your account") || !strings.Contains(welcome.Body.String(), `value="Admin"`) {
		t.Errorf("the welcome page: %d", welcome.Code)
	}
	for _, path := range []string{"/static/css/app.css", "/healthz", "/favicon.svg"} {
		if rec := f.get(path); rec.Code != 200 {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := f.post("/logout", url.Values{"csrf": {f.csrf}}, f.cookie); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("logging out must always be possible: %d", rec.Code)
	}
}

func TestWelcome_ChoosingNewCredentials(t *testing.T) {
	f := signInAsDefault(t, newDefaultFixture(t))
	old := f.cookie

	rec := f.post("/welcome", url.Values{
		"csrf": {f.csrf}, "name": {"Rina"}, "email": {"Rina@Example.com"},
		"password": {adminPassword}, "confirm": {adminPassword},
	}, f.cookie)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/clients" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	fresh := cookieNamed(rec, sessionCookie)
	if fresh == nil || sessionCookie+"="+fresh.Value == old {
		t.Fatalf("a new session cookie must replace the old one: %+v", fresh)
	}

	g := f.anon()
	g.cookie = sessionCookie + "=" + fresh.Value
	if got := g.get("/clients"); got.Code != 200 || !strings.Contains(got.Body.String(), "Clients Overview") {
		t.Fatalf("the dashboard opens after the change: %d", got.Code)
	}
	oldSession := f.anon()
	oldSession.cookie = old
	if got := oldSession.get("/clients"); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/login" {
		t.Errorf("the session opened with admin/admin must be dead: %d to %q", got.Code, got.Header().Get("Location"))
	}
	if got := f.login(t, "admin", "admin", nil); got.Code != http.StatusUnauthorized {
		t.Errorf("admin/admin must stop working: %d", got.Code)
	}
	if got := f.login(t, "rina@example.com", adminPassword, nil); got.Code != http.StatusSeeOther {
		t.Errorf("the new credentials work: %d", got.Code)
	}
	if strings.Contains(f.anon().get("/login").Body.String(), "default-login") {
		t.Error("the hint disappears once the default account is gone")
	}
	if got := g.get("/welcome"); got.Code != http.StatusSeeOther || got.Header().Get("Location") != "/clients" {
		t.Errorf("an account that has chosen its credentials has no business on /welcome: %d to %q", got.Code, got.Header().Get("Location"))
	}
}

func TestWelcome_ValidationKeepsWhatWasTypedAndChangesNothing(t *testing.T) {
	base := newDefaultFixture(t)
	f := signInAsDefault(t, base)

	rec := f.post("/welcome", url.Values{
		"csrf": {f.csrf}, "name": {"Rina"}, "email": {"rina@example.com"},
		"password": {"short"}, "confirm": {"short"},
	}, f.cookie)

	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest || !strings.Contains(body, "Password must be at least 12 characters.") {
		t.Fatalf("got %d", rec.Code)
	}
	if !strings.Contains(body, `value="Rina"`) || !strings.Contains(body, `value="rina@example.com"`) || strings.Contains(body, `value="short"`) {
		t.Error("name and email are kept, the password never is")
	}
	if got := base.login(t, "admin", "admin", nil); got.Code != http.StatusSeeOther {
		t.Errorf("a rejected change must leave admin/admin working (until it is changed): %d", got.Code)
	}
}

func TestWelcome_Protections(t *testing.T) {
	f := signInAsDefault(t, newDefaultFixture(t))
	form := url.Values{"csrf": {f.csrf}, "name": {"Rina"}, "email": {"rina@example.com"}, "password": {adminPassword}, "confirm": {adminPassword}}
	without := func(k string) url.Values {
		c := url.Values{}
		for key, v := range form {
			if key != k {
				c[key] = v
			}
		}
		return c
	}

	tests := map[string]*httptest.ResponseRecorder{
		"no csrf token":    f.post("/welcome", without("csrf"), f.cookie),
		"wrong csrf token": f.post("/welcome", func() url.Values { c := without("csrf"); c.Set("csrf", "nope"); return c }(), f.cookie),
		"another origin":   f.post("/welcome", form, f.cookie, "Origin", "https://evil.example"),
	}
	for name, rec := range tests {
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, rec.Code)
		}
	}
	if rec := f.anon().post("/welcome", form, ""); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("without a session: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := f.login(t, "admin", "admin", nil); got.Code != http.StatusSeeOther {
		t.Errorf("none of those may change the account: %d", got.Code)
	}
}

func TestWelcome_IsNotForAccountsThatAlreadyChoseTheirs(t *testing.T) {
	f := newFixture(t)

	if rec := f.get("/welcome"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/clients" {
		t.Errorf("GET: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	rec := f.post("/welcome", url.Values{"csrf": {f.csrf}, "name": {"Hacker"}, "email": {"hacker@example.com"}, "password": {"another long password"}, "confirm": {"another long password"}}, f.cookie)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/clients" {
		t.Errorf("POST: %d", rec.Code)
	}
	if got := f.login(t, "rina@example.com", adminPassword, nil); got.Code != http.StatusSeeOther {
		t.Errorf("a normal account's credentials must not be rewritable through /welcome: %d", got.Code)
	}
}

func TestSetup_IsNotOfferedWhenTheDefaultAccountExists(t *testing.T) {
	f := newDefaultFixture(t)

	rec := f.anon().get("/setup")

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}
