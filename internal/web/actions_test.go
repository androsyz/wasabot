package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/androsyz/wasabot/internal/manager"
	"github.com/androsyz/wasabot/internal/store"
)

func (f *fixture) runtime() *stubRuntime { return f.srv.opts.Runtime.(*stubRuntime) }

// act posts as the fixture's logged-in user, with the CSRF token as a form field.
func (f *fixture) act(path string, form url.Values) (code int, location, body string, header http.Header) {
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf", f.csrf)
	rec := f.post(path, form, f.cookie)
	return rec.Code, rec.Header().Get("Location"), rec.Body.String(), rec.Header()
}

// memberOf logs in a client user who belongs to the given clients only.
func memberOf(t *testing.T, f *fixture, clientIDs ...int64) *fixture {
	t.Helper()
	ctx := context.Background()
	hash, err := testHasher.Hash("member password 123")
	if err != nil {
		t.Fatal(err)
	}
	member, err := f.st.Users.Create(ctx, store.User{Email: "member@example.com", Name: "Member", PasswordHash: &hash, Role: store.RoleClientUser})
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	for _, id := range clientIDs {
		if err := f.st.Clients.AddUser(ctx, id, member.ID); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	rec := f.login(t, "member@example.com", "member password 123", nil)
	session := cookieNamed(rec, sessionCookie)
	if session == nil {
		t.Fatalf("member login: %d", rec.Code)
	}
	identity, err := f.auth.Authenticate(ctx, session.Value)
	if err != nil {
		t.Fatalf("authenticate member: %v", err)
	}
	g := f.anon()
	g.cookie = sessionCookie + "=" + session.Value
	g.csrf = identity.CSRFToken
	return g
}

func TestClientActions_RunAndGoBackToTheDashboard(t *testing.T) {
	tests := []struct {
		verb, call string
		client     int64
	}{
		{"start", "start 3", 3},
		{"stop", "stop 1", 1},
		{"logout", "logout 1", 1},
	}
	for _, tt := range tests {
		t.Run(tt.verb, func(t *testing.T) {
			f := newFixture(t)

			code, location, _, _ := f.act("/clients/"+strconv.FormatInt(tt.client, 10)+"/"+tt.verb, nil)

			if code != http.StatusSeeOther || location != "/clients" {
				t.Fatalf("got %d to %q, want a redirect to the dashboard", code, location)
			}
			if got := f.runtime().Called(); len(got) != 1 || got[0] != tt.call {
				t.Fatalf("runtime calls %v, want [%s]", got, tt.call)
			}
		})
	}
}

func TestClientActions_FailureShowsAFixedNotice(t *testing.T) {
	f := newFixture(t)
	f.runtime().err = errors.New("dial tcp 10.0.0.1: secret detail")

	_, location, _, _ := f.act("/clients/3/start", nil)
	if location != "/clients?notice=start_failed" {
		t.Fatalf("redirected to %q", location)
	}

	page := f.get(location).Body.String()
	if !strings.Contains(page, "WhatsApp could not be started") {
		t.Error("the dashboard must show the notice")
	}
	if strings.Contains(page, "secret detail") {
		t.Error("the error text must stay in the logs")
	}
}

func TestClientActions_UnknownNoticeShowsNothing(t *testing.T) {
	body := newFixture(t).get("/clients?notice=%3Cscript%3E").Body.String()

	if strings.Contains(body, "notice-bad") || strings.Contains(body, "<script>") {
		t.Fatal("only the known notices may appear")
	}
}

func TestClientActions_AStaleButtonIsNotAnError(t *testing.T) {
	f := newFixture(t)
	f.runtime().err = manager.ErrNotLinked

	_, location, _, _ := f.act("/clients/4/start", nil)

	if location != "/clients" {
		t.Fatalf("redirected to %q, want a plain redirect", location)
	}
}

func TestClientActions_NeedLoginAndCSRF(t *testing.T) {
	f := newFixture(t)

	if rec := f.anon().post("/clients/1/stop", url.Values{}, ""); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("logged out: got %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := f.post("/clients/1/stop", url.Values{}, f.cookie); rec.Code != http.StatusForbidden {
		t.Errorf("without a token: got %d, want 403", rec.Code)
	}
	if rec := f.post("/clients/1/stop", url.Values{"csrf": {"wrong"}}, f.cookie); rec.Code != http.StatusForbidden {
		t.Errorf("with a wrong token: got %d, want 403", rec.Code)
	}
	if got := f.runtime().Called(); len(got) != 0 {
		t.Fatalf("nothing may run: %v", got)
	}
}

func TestClientActions_RespectWhoOwnsTheClient(t *testing.T) {
	f := newFixture(t)
	member := memberOf(t, f, 2)

	if code, _, _, _ := member.act("/clients/2/stop", nil); code != http.StatusSeeOther {
		t.Errorf("own client: got %d, want a redirect", code)
	}
	for _, path := range []string{"/clients/1/stop", "/clients/1/pair", "/clients/1/pair/cancel", "/clients/999/stop"} {
		if code, _, _, _ := member.act(path, nil); code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, code)
		}
	}
	if got := f.runtime().Called(); len(got) != 1 || got[0] != "stop 2" {
		t.Fatalf("only the member's own client may be touched: %v", got)
	}
}

func TestClientActions_BadIDs(t *testing.T) {
	f := newFixture(t)

	for _, path := range []string{"/clients/abc/stop", "/clients/0/stop", "/clients/-1/stop", "/clients/999/stop"} {
		if code, _, _, _ := f.act(path, nil); code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, code)
		}
	}
}

func TestClientActions_NoRuntimeNoActions(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Runtime = nil })

	if code, _, _, _ := f.act("/clients/1/stop", nil); code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", code)
	}
}

func TestClientsPage_RowActionsPostWithTheToken(t *testing.T) {
	f := newFixture(t)

	body := f.get("/clients").Body.String()

	for _, want := range []string{
		`action="/clients/1/stop"`, `action="/clients/1/logout"`, `action="/clients/3/start"`,
		`hx-post="/clients/2/pair"`, `hx-post="/clients/4/pair"`,
		`name="csrf" value="` + f.csrf + `"`,
		`data-confirm="Unlink this WhatsApp number?`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page does not contain %q", want)
		}
	}
}

func TestPair_ShowsTheCodeAsAnInlineImage(t *testing.T) {
	f := newFixture(t)

	code, _, body, _ := f.act("/clients/4/pair", nil)
	if code != http.StatusOK || !strings.Contains(body, "Getting a code") {
		t.Fatalf("before the first code: %d %q", code, body)
	}
	if got := f.runtime().Called(); len(got) != 1 || got[0] != "pair 4" {
		t.Fatalf("runtime calls %v", got)
	}

	f.runtime().pairing[4] = manager.Pairing{Code: "2@a-pairing-code,with,commas"}
	poll := f.get("/clients/4/pair", "HX-Request", "true")
	page := poll.Body.String()

	if poll.Code != http.StatusOK || !strings.Contains(page, `<svg class="qr"`) || !strings.Contains(page, "<path") {
		t.Fatalf("the code must be drawn: %d %s", poll.Code, page)
	}
	if strings.Contains(page, "a-pairing-code") {
		t.Fatal("the code text itself must not be in the page")
	}
	if !strings.Contains(page, `hx-get="/clients/4/pair"`) || !strings.Contains(page, `hx-trigger="every 2s"`) {
		t.Error("the panel must keep polling for the next code")
	}
	if strings.Contains(page, "style=") {
		t.Error("inline styles are blocked by the content security policy")
	}
}

func TestPair_FailureStopsPollingAndOffersARetry(t *testing.T) {
	f := newFixture(t)
	f.runtime().pairing = map[int64]manager.Pairing{4: {Err: errors.New("timeout")}}

	page := f.get("/clients/4/pair").Body.String()

	if !strings.Contains(page, "The code expired or pairing failed.") || !strings.Contains(page, "Try again") {
		t.Errorf("want the failure and a retry: %s", page)
	}
	if strings.Contains(page, "hx-trigger") {
		t.Error("a failed attempt must stop polling")
	}
}

func TestPair_NumberLinkedElsewhere(t *testing.T) {
	f := newFixture(t)
	f.runtime().pairing = map[int64]manager.Pairing{4: {Err: store.ErrJIDLinked}}

	page := f.get("/clients/4/pair").Body.String()

	if !strings.Contains(page, "already linked to another client") {
		t.Errorf("want the specific reason: %s", page)
	}
}

func TestPair_LinkedClientRefreshesThePage(t *testing.T) {
	f := newFixture(t)

	rec := f.get("/clients/3/pair", "HX-Request", "true") // stopped: it has a number and no attempt

	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("got %d with HX-Refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}
}

func TestPair_NothingInProgressClearsThePanel(t *testing.T) {
	f := newFixture(t)

	rec := f.get("/clients/4/pair", "HX-Request", "true")

	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestPair_CancelEndsTheAttempt(t *testing.T) {
	f := newFixture(t)
	f.runtime().pairing = map[int64]manager.Pairing{4: {Code: "x"}}

	code, _, body, _ := f.act("/clients/4/pair/cancel", nil)

	if code != http.StatusOK || strings.TrimSpace(body) != "" {
		t.Fatalf("got %d %q", code, body)
	}
	if _, ok := f.runtime().Pairing(4); ok {
		t.Fatal("the attempt must be gone")
	}
}

func TestPair_StartFailureIsAnError(t *testing.T) {
	f := newFixture(t)
	f.runtime().err = errors.New("boom")

	if code, _, _, _ := f.act("/clients/4/pair", nil); code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", code)
	}
}

func TestQRSVG(t *testing.T) {
	svg, err := qrSVG("2@ref,noise,key")
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)

	if !strings.HasPrefix(s, `<svg class="qr"`) || !strings.HasSuffix(s, "</svg>") || !strings.Contains(s, `fill="#fff"`) {
		t.Fatalf("unexpected svg: %.120s", s)
	}
	if strings.Contains(s, "noise") {
		t.Fatal("the code text must not appear in the image")
	}
}

func TestAddClient(t *testing.T) {
	f := newFixture(t)

	form := f.get("/clients/new", "HX-Request", "true")
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), `hx-post="/clients"`) {
		t.Fatalf("form: %d %s", form.Code, form.Body.String())
	}

	code, _, _, header := f.act("/clients", url.Values{"name": {"  Bengkel Jaya  "}})

	if code != http.StatusNoContent || header.Get("HX-Refresh") != "true" {
		t.Fatalf("got %d with HX-Refresh %q", code, header.Get("HX-Refresh"))
	}
	clients, err := f.st.Clients.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range clients {
		names = append(names, c.Name)
	}
	if !slices.Contains(names, "Bengkel Jaya") {
		t.Fatalf("stored %q, want the trimmed name", names)
	}
	if !strings.Contains(f.get("/clients").Body.String(), "Bengkel Jaya</span>") {
		t.Fatal("the new client must be on the dashboard")
	}
}

func TestAddClient_RejectsBadNames(t *testing.T) {
	f := newFixture(t)
	before, _ := f.st.Clients.List(context.Background())

	for name, value := range map[string]string{"empty": "   ", "too long": strings.Repeat("a", maxClientNameRunes+1)} {
		code, _, body, _ := f.act("/clients", url.Values{"name": {value}})
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Give the client a name") {
			t.Errorf("%s: got %d %s", name, code, body)
		}
	}
	if after, _ := f.st.Clients.List(context.Background()); len(after) != len(before) {
		t.Fatal("nothing may be created")
	}
}

func TestAddClient_EscapesTheNameInTheForm(t *testing.T) {
	f := newFixture(t)

	_, _, body, _ := f.act("/clients", url.Values{"name": {strings.Repeat("<", maxClientNameRunes+1)}})

	if strings.Contains(body, "<<<") {
		t.Fatal("the name must be escaped when it is shown again")
	}
}

func TestAddClient_OnlyForSuperAdmins(t *testing.T) {
	f := newFixture(t)
	member := memberOf(t, f, 2)

	if code, _, _, _ := member.act("/clients", url.Values{"name": {"Nope"}}); code != http.StatusNotFound {
		t.Errorf("create: got %d, want 404", code)
	}
	if rec := member.get("/clients/new"); rec.Code != http.StatusNotFound {
		t.Errorf("form: got %d, want 404", rec.Code)
	}
	if strings.Contains(member.get("/clients").Body.String(), `hx-get="/clients/new"`) {
		t.Error("the button must be hidden")
	}
	if !strings.Contains(f.get("/clients").Body.String(), `hx-get="/clients/new"`) {
		t.Error("a super admin sees the button")
	}
}

func TestManagerStatusesMatchTheDashboard(t *testing.T) {
	for _, s := range []struct {
		m manager.Status
		w ClientStatus
	}{
		{manager.StatusConnected, StatusConnected},
		{manager.StatusPairing, StatusPairing},
		{manager.StatusReconnecting, StatusReconnecting},
		{manager.StatusStopped, StatusStopped},
		{manager.StatusLoggedOut, StatusLoggedOut},
	} {
		if string(s.m) != string(s.w) {
			t.Errorf("manager %q and dashboard %q differ", s.m, s.w)
		}
	}
}
