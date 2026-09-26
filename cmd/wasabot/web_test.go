package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/config"
	"github.com/androsyz/wasabot/internal/db/dbtest"
	"github.com/androsyz/wasabot/internal/store"
	"github.com/androsyz/wasabot/internal/web"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newWebApp(t *testing.T) (*app, *syncBuffer) {
	t.Helper()
	sqlDB := dbtest.New(t)
	st := store.New(sqlDB)
	svc, err := auth.NewService(st, auth.Options{Hasher: auth.NewHasher(auth.Params{Memory: 8, Time: 1, Threads: 1})})
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	logs := &syncBuffer{}
	return &app{
		cfg:     config.Config{Addr: "127.0.0.1:0"},
		log:     slog.New(slog.NewTextHandler(logs, nil)),
		db:      sqlDB,
		store:   st,
		auth:    svc,
		runtime: newRuntime(),
	}, logs
}

func startAndClean(t *testing.T, a *app) *web.Server {
	t.Helper()
	srv, err := a.startWeb(context.Background())
	if err != nil {
		t.Fatalf("startWeb: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return srv
}

// noRedirects lets a test look at a redirect instead of following it.
var noRedirects = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func get(t *testing.T, url string) (int, string, string) {
	t.Helper()
	resp, err := noRedirects.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

func TestStartWeb_WithAConfiguredSetupCodeNothingSecretIsLogged(t *testing.T) {
	a, logs := newWebApp(t)
	a.cfg.SetupCode = "my-own-setup-code"
	srv := startAndClean(t, a)

	if strings.Contains(logs.String(), "my-own-setup-code") || strings.Contains(logs.String(), "token=") {
		t.Fatalf("the operator's code must never be logged:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "WASABOT_SETUP_CODE") {
		t.Fatalf("the log should tell the operator where the code comes from:\n%s", logs.String())
	}

	// a whole setup over real HTTP, like a browser without access to the server's terminal
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: noRedirects.CheckRedirect}
	base := "http://" + srv.Addr()
	resp, err := client.Get(base + "/setup")
	if err != nil {
		t.Fatalf("get setup: %v", err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
	if resp.StatusCode != 200 || csrf == nil {
		t.Fatalf("setup page: %d", resp.StatusCode)
	}

	form := url.Values{"csrf": {string(csrf[1])}, "code": {"my-own-setup-code"}, "name": {"Rina"}, "email": {"rina@example.com"},
		"password": {"correct horse battery"}, "confirm": {"correct horse battery"}}
	resp, err = client.PostForm(base+"/setup", form)
	if err != nil {
		t.Fatalf("post setup: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/clients" {
		t.Fatalf("setup with the operator's code: %d to %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	resp, err = client.Get(base + "/clients")
	if err != nil {
		t.Fatalf("get clients: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("the new admin is logged in: %d", resp.StatusCode)
	}
}

func TestStartWeb_NoSetupLinkOnceAnAdminExists(t *testing.T) {
	a, logs := newWebApp(t)
	if _, err := a.auth.CreateFirstAdmin(context.Background(), "Rina", "rina@example.com", "correct horse battery", "correct horse battery"); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	srv := startAndClean(t, a)

	if strings.Contains(logs.String(), "/setup") || strings.Contains(logs.String(), "token=") {
		t.Fatalf("no setup link may be logged once an admin exists:\n%s", logs.String())
	}
	if status, loc, _ := get(t, "http://"+srv.Addr()+"/setup"); status != http.StatusSeeOther || loc != "/login" {
		t.Fatalf("/setup after setup: %d to %q", status, loc)
	}
}

func TestBootstrapAdmin_CreatesTheAdminFromTheEnvironment(t *testing.T) {
	a, logs := newWebApp(t)
	a.cfg.AdminName, a.cfg.AdminEmail, a.cfg.AdminPassword = "Rina", "rina@example.com", "correct horse battery"
	ctx := context.Background()

	if err := a.bootstrapAdmin(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	var sessions int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("bootstrapping must not start a session nobody holds: %d sessions (%v)", sessions, err)
	}

	user, err := a.store.Users.GetByEmail(ctx, "rina@example.com")
	if err != nil || user.Role != store.RoleSuperAdmin || user.Name != "Rina" {
		t.Fatalf("admin = %+v, %v", user, err)
	}
	if _, err := a.auth.Login(ctx, "rina@example.com", "correct horse battery", "1.2.3.4", false); err != nil {
		t.Fatalf("the admin can log in with the configured password: %v", err)
	}
	for _, secret := range []string{"correct horse battery", "rina@example.com"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("the log must not contain %q:\n%s", secret, logs.String())
		}
	}

	srv := startAndClean(t, a)
	if strings.Contains(logs.String(), "/setup") {
		t.Fatalf("with an admin there is no setup to announce:\n%s", logs.String())
	}
	if status, loc, _ := get(t, "http://"+srv.Addr()+"/setup"); status != http.StatusSeeOther || loc != "/login" {
		t.Fatalf("/setup after bootstrap: %d to %q", status, loc)
	}
}

func TestBootstrapAdmin_DoesNothingWithoutSettings(t *testing.T) {
	a, _ := newWebApp(t)

	if err := a.bootstrapAdmin(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if n, _ := a.store.Users.Count(context.Background()); n != 0 {
		t.Fatalf("got %d users", n)
	}
}

func TestBootstrapAdmin_LeavesAnExistingInstallAlone(t *testing.T) {
	a, _ := newWebApp(t)
	ctx := context.Background()
	if _, err := a.auth.CreateFirstAdmin(ctx, "First", "first@example.com", "correct horse battery", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	a.cfg.AdminName, a.cfg.AdminEmail, a.cfg.AdminPassword = "Other", "other@example.com", "another long password"

	if err := a.bootstrapAdmin(ctx); err != nil {
		t.Fatalf("an existing admin is not an error: %v", err)
	}

	if n, _ := a.store.Users.Count(ctx); n != 1 {
		t.Fatalf("the settings must only matter on the first start, got %d users", n)
	}
	if _, err := a.store.Users.GetByEmail(ctx, "other@example.com"); err == nil {
		t.Fatal("a second admin must not appear because the settings still contain one")
	}
}

func TestBootstrapAdmin_RejectsUnusableSettingsWithoutLeakingThem(t *testing.T) {
	a, _ := newWebApp(t)
	a.cfg.AdminName, a.cfg.AdminEmail, a.cfg.AdminPassword = "Admin", "admin@example.com", "tooshort"

	err := a.bootstrapAdmin(context.Background())

	if err == nil || !strings.Contains(err.Error(), "at least 12 characters") || !strings.Contains(err.Error(), "WASABOT_ADMIN") {
		t.Fatalf("got %v, want an error that says what to fix", err)
	}
	if strings.Contains(err.Error(), "tooshort") {
		t.Fatalf("the error leaks the password: %v", err)
	}
	if n, _ := a.store.Users.Count(context.Background()); n != 0 {
		t.Fatalf("got %d users", n)
	}
}

func TestPrepareAdmin_ADefaultInstallGetsAdminAdminAndAWarning(t *testing.T) {
	a, logs := newWebApp(t)
	ctx := context.Background()

	if err := a.prepareAdmin(ctx); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	user, err := a.store.Users.GetByEmail(ctx, "admin")
	if err != nil || !user.MustChangePassword || user.Role != store.RoleSuperAdmin {
		t.Fatalf("default admin = %+v, %v", user, err)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "admin with the password admin") {
		t.Fatalf("a default install must warn:\n%s", logs.String())
	}

	srv := startAndClean(t, a)
	if status, _, body := get(t, "http://"+srv.Addr()+"/login"); status != 200 || !strings.Contains(body, "default-login") {
		t.Fatalf("the login page tells a first-time visitor how to get in: %d", status)
	}
	if status, loc, _ := get(t, "http://"+srv.Addr()+"/setup"); status != http.StatusSeeOther || loc != "/login" {
		t.Fatalf("there is no setup page in this mode: %d to %q", status, loc)
	}
}

func TestPrepareAdmin_EveryStartWarnsUntilTheDefaultIsReplaced(t *testing.T) {
	a, logs := newWebApp(t)
	ctx := context.Background()
	if err := a.prepareAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.prepareAdmin(ctx); err != nil {
		t.Fatal(err)
	}

	if n, _ := a.store.Users.Count(ctx); n != 1 {
		t.Fatalf("a restart must not make another account, got %d users", n)
	}
	if strings.Count(logs.String(), "default admin account is active") != 2 {
		t.Fatalf("each start warns while the default is unchanged:\n%s", logs.String())
	}

	login, err := a.auth.Login(ctx, "admin", "admin", "1.2.3.4", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := a.auth.ChangeCredentials(ctx, login.User, "Rina", "rina@example.com", "correct horse battery", "correct horse battery"); err != nil {
		t.Fatalf("change: %v", err)
	}
	logs.mu.Lock()
	logs.b.Reset()
	logs.mu.Unlock()

	if err := a.prepareAdmin(ctx); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(logs.String(), "WARN") {
		t.Fatalf("no warning once the credentials are changed:\n%s", logs.String())
	}
	if _, err := a.store.Users.GetByEmail(ctx, "admin"); err == nil {
		t.Fatal("the default account must not come back after being replaced")
	}
}

func TestPrepareAdmin_ConfiguredModesNeverCreateTheDefaultAccount(t *testing.T) {
	t.Run("a setup code", func(t *testing.T) {
		a, logs := newWebApp(t)
		a.cfg.SetupCode = "my-own-setup-code"

		if err := a.prepareAdmin(context.Background()); err != nil {
			t.Fatal(err)
		}

		if n, _ := a.store.Users.Count(context.Background()); n != 0 {
			t.Fatalf("got %d users: the setup page is the way in", n)
		}
		if strings.Contains(logs.String(), "WARN") {
			t.Fatalf("nothing to warn about:\n%s", logs.String())
		}
	})

	t.Run("an admin from the environment", func(t *testing.T) {
		a, logs := newWebApp(t)
		a.cfg.AdminName, a.cfg.AdminEmail, a.cfg.AdminPassword = "Ops", "ops@example.com", "a long headless password"

		if err := a.prepareAdmin(context.Background()); err != nil {
			t.Fatal(err)
		}

		if _, err := a.store.Users.GetByEmail(context.Background(), "admin"); err == nil {
			t.Fatal("an install with configured credentials must never get admin/admin")
		}
		if n, _ := a.store.Users.Count(context.Background()); n != 1 || strings.Contains(logs.String(), "WARN") {
			t.Fatalf("got %d users, logs:\n%s", n, logs.String())
		}
	})

	t.Run("an install that already has users", func(t *testing.T) {
		a, _ := newWebApp(t)
		ctx := context.Background()
		if _, err := a.auth.CreateFirstAdmin(ctx, "Rina", "rina@example.com", "correct horse battery", "correct horse battery"); err != nil {
			t.Fatal(err)
		}

		if err := a.prepareAdmin(ctx); err != nil {
			t.Fatal(err)
		}

		if _, err := a.store.Users.GetByEmail(ctx, "admin"); err == nil {
			t.Fatal("upgrading an existing install must never add a well-known account")
		}
	})
}
