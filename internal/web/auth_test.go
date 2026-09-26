package web

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/androsyz/wasabot/internal/store"
)

var hiddenCSRF = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// post sends a form, with the given cookies and extra headers.
func (f *fixture) post(path string, form url.Values, cookies string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// loginForm opens the login page like a browser and returns the CSRF cookie and field value.
func loginForm(t *testing.T, f *fixture) (cookie, field string) {
	t.Helper()
	rec := f.anon().get("/login")
	c := cookieNamed(rec, csrfCookie)
	m := hiddenCSRF.FindStringSubmatch(rec.Body.String())
	if rec.Code != 200 || c == nil || m == nil {
		t.Fatalf("login page: status %d, cookie %v, field %v", rec.Code, c, m)
	}
	return csrfCookie + "=" + c.Value, m[1]
}

func (f *fixture) login(t *testing.T, email, password string, extra url.Values, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	cookie, field := loginForm(t, f)
	form := url.Values{"csrf": {field}, "email": {email}, "password": {password}}
	for k, v := range extra {
		form[k] = v
	}
	return f.post("/login", form, cookie, headers...)
}

func TestDashboardRequiresLogin(t *testing.T) {
	f := newFixture(t)

	rec := f.anon().get("/clients")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("logged out: %d to %q, want a redirect to /login", rec.Code, rec.Header().Get("Location"))
	}
	if strings.Contains(rec.Body.String(), "Toko Kopi Senja") {
		t.Error("no client data may leak into the redirect")
	}

	fresh := newFreshFixture(t)
	if rec := fresh.get("/clients"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
		t.Errorf("no users yet: %d to %q, want a redirect to /setup", rec.Code, rec.Header().Get("Location"))
	}
}

func TestBadCookiesAreTreatedAsLoggedOutAndCleared(t *testing.T) {
	f := newFixture(t)

	for name, cookie := range map[string]string{
		"garbage":      sessionCookie + "=garbage",
		"empty":        sessionCookie + "=",
		"too long":     sessionCookie + "=" + strings.Repeat("a", 500),
		"another name": "other=" + strings.TrimPrefix(f.cookie, sessionCookie+"="),
	} {
		g := *f
		g.cookie = cookie
		rec := g.get("/clients")

		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
			t.Errorf("%s: %d to %q", name, rec.Code, rec.Header().Get("Location"))
		}
	}
}

func TestLoginPage(t *testing.T) {
	f := newFixture(t)

	rec := f.anon().get("/login")

	c := cookieNamed(rec, csrfCookie)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure {
		t.Fatalf("csrf cookie = %+v", c)
	}
	if m := hiddenCSRF.FindStringSubmatch(rec.Body.String()); m == nil || m[1] != c.Value {
		t.Fatal("the form must carry the same token as the cookie")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}

	again := f.anon()
	again.cookie = csrfCookie + "=" + c.Value
	if second := again.get("/login"); cookieNamed(second, csrfCookie) != nil {
		t.Error("a valid token is reused, not replaced on every visit")
	}
}

func TestLoginPageRedirects(t *testing.T) {
	f := newFixture(t)
	if rec := f.get("/login"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/clients" {
		t.Errorf("already logged in: %d to %q", rec.Code, rec.Header().Get("Location"))
	}

	fresh := newFreshFixture(t)
	if rec := fresh.get("/login"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/setup" {
		t.Errorf("no users yet: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLogin_Success(t *testing.T) {
	f := newFixture(t)

	rec := f.login(t, " Rina@Example.com ", adminPassword, nil)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/clients" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	c := cookieNamed(rec, sessionCookie)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Secure {
		t.Fatalf("session cookie = %+v", c)
	}
	if c.MaxAge != 0 || !c.Expires.IsZero() {
		t.Errorf("without remember me the cookie ends with the browser session: %+v", c)
	}

	g := f.anon()
	g.cookie = sessionCookie + "=" + c.Value
	if got := g.get("/clients"); got.Code != 200 || !strings.Contains(got.Body.String(), "Clients Overview") {
		t.Fatalf("the new session must open the dashboard: %d", got.Code)
	}
}

func TestLogin_RememberMe(t *testing.T) {
	f := newFixture(t)

	rec := f.login(t, "rina@example.com", adminPassword, url.Values{"remember": {"on"}})

	c := cookieNamed(rec, sessionCookie)
	if c == nil || c.MaxAge < 29*24*3600 || c.MaxAge > 31*24*3600 || c.Expires.IsZero() {
		t.Fatalf("remember me should last about 30 days: %+v", c)
	}
}

func TestCookiesAreSecureOverTLSOrWhenConfigured(t *testing.T) {
	t.Run("TLS is detected", func(t *testing.T) {
		f := newFixture(t)
		cookie, field := loginForm(t, f)
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{
			"csrf": {field}, "email": {"rina@example.com"}, "password": {adminPassword},
		}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", cookie)
		req.TLS = &tls.ConnectionState{}
		rec := httptest.NewRecorder()

		f.srv.Handler().ServeHTTP(rec, req)

		if c := cookieNamed(rec, sessionCookie); c == nil || !c.Secure {
			t.Fatalf("over TLS the session cookie must be Secure: %+v", c)
		}
	})

	t.Run("configured for a TLS-terminating proxy", func(t *testing.T) {
		f := newFixture(t, func(o *Options) { o.CookieSecure = true })

		rec := f.login(t, "rina@example.com", adminPassword, nil)

		if c := cookieNamed(rec, sessionCookie); c == nil || !c.Secure {
			t.Fatalf("session cookie = %+v", c)
		}
	})
}

func TestLogin_WrongCredentials(t *testing.T) {
	f := newFixture(t)

	wrong := f.login(t, "rina@example.com", "not the password!", nil)
	unknown := f.login(t, "nobody@example.com", adminPassword, nil)

	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong password": wrong, "unknown email": unknown} {
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Wrong email or password.") {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if cookieNamed(rec, sessionCookie) != nil {
			t.Errorf("%s: a failed login must not set a session", name)
		}
		if strings.Contains(rec.Body.String(), "not the password!") || strings.Contains(rec.Body.String(), adminPassword) {
			t.Errorf("%s: the password must never be echoed", name)
		}
	}
	if !strings.Contains(wrong.Body.String(), `value="rina@example.com"`) {
		t.Error("the email is kept so the user only retypes the password")
	}
	strip := func(s string) string { return hiddenCSRF.ReplaceAllString(s, "") }
	if strip(wrong.Body.String()) == strip(unknown.Body.String()) {
		return
	}
	// the only difference allowed is the email that was typed back
	if strings.ReplaceAll(strip(wrong.Body.String()), "rina@example.com", "X") != strings.ReplaceAll(strip(unknown.Body.String()), "nobody@example.com", "X") {
		t.Error("the two failures must look identical, or the page reveals which emails exist")
	}
}

func TestLogin_EscapesTheEmailItKeepsInTheForm(t *testing.T) {
	f := newFixture(t)

	rec := f.login(t, `"><script>alert(1)</script>`, "whatever password", nil)

	if strings.Contains(rec.Body.String(), "<script>alert(1)") {
		t.Fatal("the typed email reached the page unescaped")
	}
}

func TestLogin_RequiresTheCSRFToken(t *testing.T) {
	f := newFixture(t)
	cookie, field := loginForm(t, f)
	creds := func(extra url.Values) url.Values {
		v := url.Values{"email": {"rina@example.com"}, "password": {adminPassword}}
		for k, x := range extra {
			v[k] = x
		}
		return v
	}

	tests := map[string]*httptest.ResponseRecorder{
		"no field":               f.post("/login", creds(nil), cookie),
		"wrong field":            f.post("/login", creds(url.Values{"csrf": {"nope"}}), cookie),
		"field without cookie":   f.post("/login", creds(url.Values{"csrf": {field}}), ""),
		"cookie, empty field":    f.post("/login", creds(url.Values{"csrf": {""}}), cookie),
		"another cookie value":   f.post("/login", creds(url.Values{"csrf": {field}}), csrfCookie+"=someone-elses-value"),
		"empty cookie and field": f.post("/login", creds(url.Values{"csrf": {""}}), csrfCookie+"="),
	}
	for name, rec := range tests {
		if rec.Code != http.StatusForbidden || cookieNamed(rec, sessionCookie) != nil {
			t.Errorf("%s: status %d, want 403 and no session", name, rec.Code)
		}
	}

	if rec := f.post("/login", creds(url.Values{"csrf": {field}}), cookie); rec.Code != http.StatusSeeOther {
		t.Errorf("the matching token must work: %d", rec.Code)
	}
}

func TestLogin_RejectsRequestsFromOtherSites(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name    string
		headers []string
		want    int
	}{
		{"another origin", []string{"Origin", "https://evil.example"}, 403},
		{"origin null", []string{"Origin", "null"}, 403},
		{"origin with a different port", []string{"Origin", "http://example.com:81"}, 403},
		{"cross-site fetch metadata", []string{"Sec-Fetch-Site", "cross-site"}, 403},
		{"same-site but not same-origin", []string{"Sec-Fetch-Site", "same-site"}, 403},
		{"same origin", []string{"Origin", "http://example.com"}, 303},
		{"same origin, different case", []string{"Origin", "http://EXAMPLE.com"}, 303},
		{"same-origin fetch metadata", []string{"Sec-Fetch-Site", "same-origin"}, 303},
		{"typed into the address bar", []string{"Sec-Fetch-Site", "none"}, 303},
		{"no browser headers at all", nil, 303},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := f.login(t, "rina@example.com", adminPassword, nil, tt.headers...)

			if rec.Code != tt.want {
				t.Fatalf("got %d, want %d", rec.Code, tt.want)
			}
			if tt.want == 403 && cookieNamed(rec, sessionCookie) != nil {
				t.Fatal("a rejected request must not log anyone in")
			}
		})
	}
}

func TestLogin_IsRateLimited(t *testing.T) {
	f := newFixture(t)

	for range 8 {
		if rec := f.login(t, "rina@example.com", "wrong password here", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("got %d", rec.Code)
		}
	}
	rec := f.login(t, "rina@example.com", adminPassword, nil)

	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "900" {
		t.Fatalf("got %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "Too many attempts") || cookieNamed(rec, sessionCookie) != nil {
		t.Fatal("even the right password must not get in while locked")
	}
}

func TestLogout(t *testing.T) {
	f := newFixture(t)
	form := url.Values{"csrf": {f.csrf}}

	if rec := f.post("/logout", url.Values{}, f.cookie); rec.Code != http.StatusForbidden {
		t.Errorf("without a token: %d, want 403", rec.Code)
	}
	if rec := f.post("/logout", url.Values{"csrf": {"wrong"}}, f.cookie); rec.Code != http.StatusForbidden {
		t.Errorf("with a wrong token: %d, want 403", rec.Code)
	}
	if rec := f.get("/clients"); rec.Code != 200 {
		t.Fatal("a rejected logout must not end the session")
	}
	if rec := f.anon().post("/logout", form, ""); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("logging out without a session just goes to the login page: %d", rec.Code)
	}

	rec := f.post("/logout", form, f.cookie)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if c := cookieNamed(rec, sessionCookie); c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Errorf("the cookie must be cleared: %+v", c)
	}
	if after := f.get("/clients"); after.Code != http.StatusSeeOther {
		t.Errorf("the old cookie must be dead on the server, got %d", after.Code)
	}
}

func TestLogout_AcceptsTheCSRFHeaderToo(t *testing.T) {
	f := newFixture(t)

	rec := f.post("/logout", url.Values{}, f.cookie, "X-CSRF-Token", f.csrf)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("htmx sends the token as a header: %d", rec.Code)
	}
}

func TestDashboardCarriesTheSessionCSRFToken(t *testing.T) {
	f := newFixture(t)

	body := f.get("/clients").Body.String()

	if m := hiddenCSRF.FindStringSubmatch(body); m == nil || m[1] != f.csrf {
		t.Error("the logout form needs the session's token")
	}
	if !strings.Contains(body, `hx-headers='{"X-CSRF-Token": "`+f.csrf+`"}'`) {
		t.Error("htmx requests need the token as a header")
	}
	if !strings.Contains(body, "Log out") || !strings.Contains(body, "Rina") {
		t.Error("the dashboard shows who is logged in and how to log out")
	}
}

func TestPagesAreNotCached(t *testing.T) {
	f := newFixture(t)

	for _, path := range []string{"/clients"} {
		if got := f.get(path).Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", path, got)
		}
	}
	if got := f.anon().get("/login").Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("/login: Cache-Control = %q", got)
	}
}

func TestSetupPage_AlwaysShowsTheFormWithACodeField(t *testing.T) {
	f := newFreshFixture(t)

	rec := f.get("/setup")

	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="code"`) || !strings.Contains(body, "Create admin account") {
		t.Fatalf("someone who cannot read the server log must still see the form: %d", rec.Code)
	}
	if !strings.Contains(body, "WASABOT_SETUP_CODE") {
		t.Error("the page says where the code comes from")
	}
	if strings.Contains(body, `value="`+setupCode+`"`) {
		t.Error("the code itself must never be in the page")
	}
}

func TestSetup_IsSwitchedOffWithoutAConfiguredCode(t *testing.T) {
	f := newFreshFixture(t, func(o *Options) { o.SetupCode = "" })

	for _, path := range []string{"/setup", "/setup?token="} {
		rec := f.get(path)

		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Setup is switched off") ||
			strings.Contains(rec.Body.String(), `name="password"`) {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	cookie, form := setupForm(t, newFreshFixture(t)) // any valid-looking submission
	if rec := f.post("/setup", form, cookie); rec.Code != http.StatusForbidden {
		t.Errorf("posting while switched off: %d, want 403", rec.Code)
	}
	if n, _ := f.st.Users.Count(context.Background()); n != 0 {
		t.Fatalf("got %d users", n)
	}
}

func setupForm(t *testing.T, f *fixture) (cookie string, form url.Values) {
	t.Helper()
	rec := f.get("/setup")
	c := cookieNamed(rec, csrfCookie)
	m := hiddenCSRF.FindStringSubmatch(rec.Body.String())
	if rec.Code != 200 || c == nil || m == nil {
		t.Fatalf("setup page: %d", rec.Code)
	}
	return csrfCookie + "=" + c.Value, url.Values{
		"csrf": {m[1]}, "code": {setupCode},
		"name": {"Rina"}, "email": {"rina@example.com"}, "password": {adminPassword}, "confirm": {adminPassword},
	}
}

func TestSetup_CreatesTheAdminAndLogsThemIn(t *testing.T) {
	f := newFreshFixture(t)
	cookie, form := setupForm(t, f)

	rec := f.post("/setup", form, cookie)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/clients" {
		t.Fatalf("got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	session := cookieNamed(rec, sessionCookie)
	if session == nil || !session.HttpOnly {
		t.Fatalf("setup logs the admin in: %+v", session)
	}
	admin, err := f.st.Users.GetByEmail(context.Background(), "rina@example.com")
	if err != nil || admin.Role != store.RoleSuperAdmin || admin.Name != "Rina" {
		t.Fatalf("admin = %+v, %v", admin, err)
	}

	g := f.anon()
	g.cookie = sessionCookie + "=" + session.Value
	if got := g.get("/clients"); got.Code != 200 {
		t.Fatalf("the admin's session must work: %d", got.Code)
	}
	if got := f.anon().get("/login"); got.Code != 200 {
		t.Errorf("after setup the login page is shown instead of the setup redirect: %d", got.Code)
	}
}

func TestSetup_OnlyOnce(t *testing.T) {
	f := newFreshFixture(t)
	cookie, form := setupForm(t, f)
	if rec := f.post("/setup", form, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("first setup: %d", rec.Code)
	}

	rec := f.post("/setup", form, cookie)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("a second setup: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if get := f.get("/setup"); get.Code != http.StatusSeeOther || get.Header().Get("Location") != "/login" {
		t.Fatalf("the setup page after setup: %d", get.Code)
	}
	if n, _ := f.st.Users.Count(context.Background()); n != 1 {
		t.Fatalf("want one user, got %d", n)
	}
}

func TestSetup_Protections(t *testing.T) {
	f := newFreshFixture(t)
	cookie, form := setupForm(t, f)
	with := func(k, v string) url.Values {
		c := url.Values{}
		for key, val := range form {
			c[key] = val
		}
		c.Set(k, v)
		return c
	}

	tests := map[string]*httptest.ResponseRecorder{
		"wrong setup code": f.post("/setup", with("code", "guess"), cookie),
		"no setup code":    f.post("/setup", with("code", ""), cookie),
		"almost right":     f.post("/setup", with("code", setupCode+"x"), cookie),
		"wrong csrf":       f.post("/setup", with("csrf", "nope"), cookie),
		"no csrf cookie":   f.post("/setup", form, ""),
		"other origin":     f.post("/setup", form, cookie, "Origin", "https://evil.example"),
	}
	for name, rec := range tests {
		if rec.Code != http.StatusForbidden || cookieNamed(rec, sessionCookie) != nil {
			t.Errorf("%s: %d, want 403 and no session", name, rec.Code)
		}
	}
	if !strings.Contains(tests["wrong setup code"].Body.String(), "That setup code is not right.") {
		t.Error("a wrong code gets a clear message")
	}
	if n, _ := f.st.Users.Count(context.Background()); n != 0 {
		t.Fatalf("none of those may create a user, got %d", n)
	}
}

func TestSetup_WrongCodesAreRateLimited(t *testing.T) {
	f := newFreshFixture(t)
	cookie, form := setupForm(t, f)
	form.Set("code", "wrong code")

	for i := range 10 {
		if rec := f.post("/setup", form, cookie); rec.Code != http.StatusForbidden {
			t.Fatalf("guess %d: %d", i+1, rec.Code)
		}
	}
	form.Set("code", setupCode) // even the right code is refused while blocked

	rec := f.post("/setup", form, cookie)

	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "900" {
		t.Fatalf("got %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if n, _ := f.st.Users.Count(context.Background()); n != 0 {
		t.Fatalf("a blocked address must not create the admin, got %d users", n)
	}
}

func TestSetup_ValidationErrorsKeepWhatWasTyped(t *testing.T) {
	f := newFreshFixture(t)
	cookie, form := setupForm(t, f)
	form.Set("password", "short")
	form.Set("confirm", "short")

	rec := f.post("/setup", form, cookie)

	body := rec.Body.String()
	if rec.Code != http.StatusBadRequest || !strings.Contains(body, "Password must be at least 12 characters.") {
		t.Fatalf("got %d", rec.Code)
	}
	if !strings.Contains(body, `value="Rina"`) || !strings.Contains(body, `value="rina@example.com"`) {
		t.Error("name and email are kept")
	}
	if strings.Contains(body, `value="short"`) {
		t.Error("passwords are never sent back")
	}
	if !strings.Contains(body, `name="code" value="`+setupCode+`"`) {
		t.Error("a correct code stays in the form, so one typo does not mean starting over")
	}
	if n, _ := f.st.Users.Count(context.Background()); n != 0 {
		t.Fatalf("got %d users", n)
	}
}

func TestSetup_AWrongCodeIsNotEchoedBack(t *testing.T) {
	f := newFreshFixture(t)
	cookie, form := setupForm(t, f)
	form.Set("code", "my-wrong-guess")

	rec := f.post("/setup", form, cookie)

	if strings.Contains(rec.Body.String(), "my-wrong-guess") {
		t.Fatal("a rejected code must not be echoed into the page")
	}
}

func TestDashboard_ShowsClientUsersOnlyTheirOwnClients(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hash, _ := testHasher.Hash("member password 123")
	member, err := f.st.Users.Create(ctx, store.User{Email: "member@example.com", Name: "Member", PasswordHash: &hash, Role: store.RoleClientUser})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	if err := f.st.Clients.AddUser(ctx, 2, member.ID); err != nil { // "Klinik Gigi Sehat"
		t.Fatalf("add member: %v", err)
	}
	rec := f.login(t, "member@example.com", "member password 123", nil)
	session := cookieNamed(rec, sessionCookie)
	if session == nil {
		t.Fatalf("member login: %d", rec.Code)
	}
	g := f.anon()
	g.cookie = sessionCookie + "=" + session.Value

	body := g.get("/clients").Body.String()

	if !strings.Contains(body, "Klinik Gigi Sehat") {
		t.Error("the member's own client is listed")
	}
	for _, other := range []string{"Toko Kopi Senja", "Laundry Bersih", "Kos Melati"} {
		if strings.Contains(body, other) {
			t.Errorf("%q belongs to someone else and must not appear anywhere on the page", other)
		}
	}
	if !strings.Contains(body, "0 Connected") || !strings.Contains(body, "1 Connecting") {
		t.Error("the status bar counts only the clients this user can see")
	}

	// asking for a client they may not see falls back to their own instead of exposing it
	asked := g.get("/clients?client=1").Body.String()
	if !strings.Contains(asked, `<option value="2" selected>`) || strings.Contains(asked, "Toko Kopi Senja") {
		t.Error("?client= must not select or reveal a client the user cannot see")
	}
}

func TestDashboard_AnUnknownClientParameterFallsBackToTheFirst(t *testing.T) {
	f := newFixture(t)

	for _, q := range []string{"?client=999", "?client=abc", "?client=-1", "?client="} {
		rec := f.get("/clients" + q)

		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `<option value="1" selected>`) {
			t.Errorf("%s: status %d, want the first client selected", q, rec.Code)
		}
	}
}

func TestDisablingAUserEndsTheirSessionAtTheNextRequest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	hash, _ := testHasher.Hash("member password 123")
	member, err := f.st.Users.Create(ctx, store.User{Email: "member@example.com", Name: "Member", PasswordHash: &hash, Role: store.RoleClientUser})
	if err != nil {
		t.Fatal(err)
	}
	rec := f.login(t, "member@example.com", "member password 123", nil)
	session := cookieNamed(rec, sessionCookie)
	g := f.anon()
	g.cookie = sessionCookie + "=" + session.Value
	if g.get("/clients").Code != 200 {
		t.Fatal("the member is in")
	}

	if _, err := f.sql.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, member.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}

	after := g.get("/clients")
	if after.Code != http.StatusSeeOther || after.Header().Get("Location") != "/login" {
		t.Fatalf("got %d to %q", after.Code, after.Header().Get("Location"))
	}
	if c := cookieNamed(after, sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("the dead cookie is cleared: %+v", c)
	}
}
