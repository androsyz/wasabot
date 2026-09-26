package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/androsyz/wasabot/internal/db/dbtest"
	"github.com/androsyz/wasabot/internal/store"
)

const goodPassword = "correct horse battery"

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type env struct {
	svc   *Service
	st    *store.Store
	sqlDB *sql.DB
	clock *clock
}

func newEnv(t *testing.T, mods ...func(*Options)) *env {
	t.Helper()
	sqlDB := dbtest.New(t)
	st := store.New(sqlDB)
	clk := &clock{now: time.Now()}

	opts := Options{Hasher: NewHasher(testParams), Now: clk.Now}
	for _, mod := range mods {
		mod(&opts)
	}
	svc, err := NewService(st, opts)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &env{svc: svc, st: st, sqlDB: sqlDB, clock: clk}
}

func (e *env) addUser(t *testing.T, email, password string, mod func(*store.User)) store.User {
	t.Helper()
	u := store.User{Email: email, Name: "User", Role: store.RoleClientUser}
	if password != "" {
		hash, err := NewHasher(testParams).Hash(password)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		u.PasswordHash = &hash
	}
	if mod != nil {
		mod(&u)
	}
	created, err := e.st.Users.Create(context.Background(), u)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return created
}

func TestNeedsSetup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if needs, err := e.svc.NeedsSetup(ctx); err != nil || !needs {
		t.Fatalf("empty database: %v, %v", needs, err)
	}
	e.addUser(t, "a@example.com", goodPassword, nil)

	if needs, err := e.svc.NeedsSetup(ctx); err != nil || needs {
		t.Fatalf("after a user exists: %v, %v", needs, err)
	}
}

func TestCreateFirstAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	got, err := e.svc.CreateFirstAdmin(ctx, "  Rina  ", " Rina@Example.COM ", goodPassword, goodPassword)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	u := got.User
	if u.Name != "Rina" || u.Email != "rina@example.com" || u.Role != store.RoleSuperAdmin || u.Disabled {
		t.Fatalf("user = %+v", u)
	}
	if u.PasswordHash == nil || strings.Contains(*u.PasswordHash, goodPassword) {
		t.Fatal("only a hash may be stored")
	}
	if ok, err := Verify(goodPassword, *u.PasswordHash); err != nil || !ok {
		t.Fatalf("the stored hash must verify the password: %v, %v", ok, err)
	}

	id, err := e.svc.Authenticate(ctx, got.Token)
	if err != nil || id.User.ID != u.ID || id.CSRFToken == "" {
		t.Fatalf("setup logs the admin in: %+v, %v", id, err)
	}
}

func TestCreateFirstAdmin_OnlyWorksOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateFirstAdmin(ctx, "A", "a@example.com", goodPassword, goodPassword); err != nil {
		t.Fatalf("first: %v", err)
	}

	_, err := e.svc.CreateFirstAdmin(ctx, "B", "b@example.com", goodPassword, goodPassword)

	if !errors.Is(err, ErrSetupDone) {
		t.Fatalf("got %v, want ErrSetupDone", err)
	}
	if n, _ := e.st.Users.Count(ctx); n != 1 {
		t.Fatalf("want one user, got %d", n)
	}
}

func TestCreateFirstAdmin_Validation(t *testing.T) {
	e := newEnv(t)

	tests := []struct {
		name, given, email, password, confirm, want string
	}{
		{"blank name", "  ", "a@example.com", goodPassword, goodPassword, "Enter your name."},
		{"name too long", strings.Repeat("n", 101), "a@example.com", goodPassword, goodPassword, "Enter your name."},
		{"no at sign", "A", "example.com", goodPassword, goodPassword, "Enter a valid email address."},
		{"display name in the address", "A", "Al <a@example.com>", goodPassword, goodPassword, "Enter a valid email address."},
		{"domain without a dot", "A", "a@localhost", goodPassword, goodPassword, "Enter a valid email address."},
		{"empty email", "A", "", goodPassword, goodPassword, "Enter a valid email address."},
		{"email too long", "A", strings.Repeat("a", 250) + "@example.com", goodPassword, goodPassword, "Enter a valid email address."},
		{"short password", "A", "a@example.com", "short", "short", "Password must be at least 12 characters."},
		{"password too long", "A", "a@example.com", strings.Repeat("a", 2000), strings.Repeat("a", 2000), "Password is too long."},
		{"passwords differ", "A", "a@example.com", goodPassword, goodPassword + "x", "Passwords do not match."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.CreateFirstAdmin(context.Background(), tt.given, tt.email, tt.password, tt.confirm)

			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Message != tt.want {
				t.Fatalf("got %v, want a validation error %q", err, tt.want)
			}
		})
	}
	if n, _ := e.st.Users.Count(context.Background()); n != 0 {
		t.Fatalf("a rejected setup must not create a user, got %d", n)
	}
}

func TestLogin_Success(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "rina@example.com", goodPassword, nil)

	got, err := e.svc.Login(context.Background(), "  RINA@example.com ", goodPassword, "1.2.3.4", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if got.User.ID != u.ID || got.Token == "" || got.CSRFToken == "" {
		t.Fatalf("got %+v", got)
	}
	if want := e.clock.Now().Add(12 * time.Hour); !got.ExpiresAt.Equal(want) && got.ExpiresAt.Unix() != want.Unix() {
		t.Fatalf("expires %v, want %v", got.ExpiresAt, want)
	}
}

func TestLogin_RememberMeLastsLonger(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "rina@example.com", goodPassword, nil)

	got, err := e.svc.Login(context.Background(), "rina@example.com", goodPassword, "1.2.3.4", true)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if want := e.clock.Now().Add(30 * 24 * time.Hour); got.ExpiresAt.Unix() != want.Unix() {
		t.Fatalf("expires %v, want %v", got.ExpiresAt, want)
	}
}

func TestLogin_EveryFailureLooksTheSame(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "rina@example.com", goodPassword, nil)
	e.addUser(t, "disabled@example.com", goodPassword, func(u *store.User) { u.Disabled = true })
	// the disabled flag is not part of Create, so set it directly
	if _, err := e.sqlDB.Exec(`UPDATE users SET disabled = 1 WHERE email = 'disabled@example.com'`); err != nil {
		t.Fatal(err)
	}
	e.addUser(t, "nopassword@example.com", "", nil)

	tests := []struct{ name, email, password string }{
		{"wrong password", "rina@example.com", "wrong password here"},
		{"unknown email", "nobody@example.com", goodPassword},
		{"disabled account with the right password", "disabled@example.com", goodPassword},
		{"account without a password", "nopassword@example.com", goodPassword},
		{"malformed email", "not an email", goodPassword},
		{"empty everything", "", ""},
	}
	var messages []string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.Login(context.Background(), tt.email, tt.password, "1.2.3.4", false)

			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("got %v, want ErrInvalidCredentials", err)
			}
			messages = append(messages, err.Error())
		})
	}
	for _, m := range messages {
		if m != messages[0] {
			t.Fatalf("the failures must be indistinguishable, got %q and %q", messages[0], m)
		}
	}
}

func TestLogin_LocksAnAccountAfterRepeatedFailures(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.AccountFailures = 3; o.IPAttempts = 1000 })
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)

	for range 3 {
		if _, err := e.svc.Login(ctx, "rina@example.com", "wrong password here", "1.2.3.4", false); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("got %v", err)
		}
	}

	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "9.9.9.9", false); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("the right password must not get in while the account is locked, got %v", err)
	}

	e.clock.Advance(16 * time.Minute)
	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false); err != nil {
		t.Fatalf("the lock ends after the window: %v", err)
	}
}

func TestLogin_LockoutHitsUnknownEmailsToo(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.AccountFailures = 2; o.IPAttempts = 1000 })
	ctx := context.Background()

	for range 2 {
		e.svc.Login(ctx, "ghost@example.com", "whatever password", "1.2.3.4", false)
	}

	if _, err := e.svc.Login(ctx, "ghost@example.com", "whatever password", "1.2.3.4", false); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("got %v: an attacker must not be able to tell real accounts from fake ones by who gets locked", err)
	}
}

func TestLogin_ASuccessResetsTheFailureCount(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.AccountFailures = 3; o.IPAttempts = 1000 })
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)

	for range 2 {
		e.svc.Login(ctx, "rina@example.com", "wrong password here", "1.2.3.4", false)
	}
	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false); err != nil {
		t.Fatalf("login: %v", err)
	}

	for range 2 {
		if _, err := e.svc.Login(ctx, "rina@example.com", "wrong password here", "1.2.3.4", false); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("got %v: earlier failures must not count after a success", err)
		}
	}
}

func TestLogin_LockoutIsPerAccount(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.AccountFailures = 2; o.IPAttempts = 1000 })
	ctx := context.Background()
	e.addUser(t, "a@example.com", goodPassword, nil)
	e.addUser(t, "b@example.com", goodPassword, nil)
	for range 2 {
		e.svc.Login(ctx, "a@example.com", "wrong password here", "1.2.3.4", false)
	}

	if _, err := e.svc.Login(ctx, "b@example.com", goodPassword, "1.2.3.4", false); err != nil {
		t.Fatalf("locking a@ must not lock b@: %v", err)
	}
}

func TestLogin_LimitsAttemptsPerAddress(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.IPAttempts = 3 })
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)

	for range 3 {
		e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)
	}

	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("got %v, want the address blocked", err)
	}
	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "5.6.7.8", false); err != nil {
		t.Fatalf("another address is unaffected: %v", err)
	}
}

func TestLogin_UpgradesAnOutdatedHash(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Hasher = NewHasher(Params{Memory: 16, Time: 2, Threads: 1}) })
	ctx := context.Background()
	u := e.addUser(t, "rina@example.com", goodPassword, nil) // hashed with testParams: older settings

	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false); err != nil {
		t.Fatalf("login: %v", err)
	}

	got, _ := e.st.Users.GetByID(ctx, u.ID)
	if !e.svc.hasher.NeedsRehash(*u.PasswordHash) || e.svc.hasher.NeedsRehash(*got.PasswordHash) {
		t.Fatal("the stored hash should have been replaced with one using the current settings")
	}
	if ok, _ := Verify(goodPassword, *got.PasswordHash); !ok {
		t.Fatal("the upgraded hash must still accept the password")
	}
}

func TestLogin_ACorruptStoredHashIsAServerError(t *testing.T) {
	e := newEnv(t)
	broken := "not-a-hash"
	e.addUser(t, "rina@example.com", "", func(u *store.User) { u.PasswordHash = &broken })

	_, err := e.svc.Login(context.Background(), "rina@example.com", goodPassword, "1.2.3.4", false)

	if err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("got %v: corruption is our problem, not a wrong password", err)
	}
}

func TestAuthenticate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)
	in, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	got, err := e.svc.Authenticate(ctx, in.Token)
	if err != nil || got.User.Email != "rina@example.com" || got.CSRFToken != in.CSRFToken {
		t.Fatalf("got %+v, %v", got, err)
	}

	bad := map[string]string{
		"empty":           "",
		"unknown":         "not-a-real-token",
		"tampered":        in.Token[:len(in.Token)-1] + "x",
		"way too long":    strings.Repeat("a", 1000),
		"the stored hash": HashToken(in.Token),
	}
	for name, token := range bad {
		if _, err := e.svc.Authenticate(ctx, token); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s: got %v, want ErrNoSession", name, err)
		}
	}
}

func TestAuthenticate_SessionsExpire(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)
	in, _ := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)

	e.clock.Advance(11 * time.Hour)
	if _, err := e.svc.Authenticate(ctx, in.Token); err != nil {
		t.Fatalf("still valid after 11h: %v", err)
	}

	e.clock.Advance(2 * time.Hour)
	if _, err := e.svc.Authenticate(ctx, in.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v, want the session expired after 13h", err)
	}
}

func TestAuthenticate_DisablingAUserEndsTheirSession(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.addUser(t, "rina@example.com", goodPassword, nil)
	in, _ := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)
	if _, err := e.sqlDB.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := e.svc.Authenticate(ctx, in.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v, want no session for a disabled user", err)
	}
	if _, _, err := e.st.Sessions.Get(ctx, HashToken(in.Token), e.clock.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("the session should have been deleted")
	}
}

func TestLogout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)
	first, _ := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)
	second, _ := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)

	if err := e.svc.Logout(ctx, first.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if _, err := e.svc.Authenticate(ctx, first.Token); !errors.Is(err, ErrNoSession) {
		t.Error("the logged-out session must be gone")
	}
	if _, err := e.svc.Authenticate(ctx, second.Token); err != nil {
		t.Error("another session of the same user stays")
	}
	for _, token := range []string{"", "unknown", strings.Repeat("a", 1000)} {
		if err := e.svc.Logout(ctx, token); err != nil {
			t.Errorf("logging out %q is not an error: %v", token, err)
		}
	}
}

func TestSessionsStoreOnlyTheTokenHash(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)

	in, _ := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)

	var ids []string
	rows, err := e.sqlDB.Query(`SELECT id_hash FROM sessions`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	if len(ids) != 1 || ids[0] == in.Token || ids[0] != HashToken(in.Token) {
		t.Fatalf("stored %v: the database must hold the hash, never the cookie value %q", ids, in.Token)
	}
}

func TestLogin_ExpiredSessionsAreCleanedUp(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addUser(t, "rina@example.com", goodPassword, nil)
	e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)
	e.clock.Advance(13 * time.Hour)

	e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false)

	var n int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("want only the fresh session left, got %d (%v)", n, err)
	}
}

func TestBootstrapAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if err := e.svc.BootstrapAdmin(ctx, " Rina ", "Rina@Example.com", goodPassword); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	u, err := e.st.Users.GetByEmail(ctx, "rina@example.com")
	if err != nil || u.Role != store.RoleSuperAdmin || u.Name != "Rina" || u.PasswordHash == nil {
		t.Fatalf("admin = %+v, %v", u, err)
	}
	var sessions int
	if err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("no session may be started for a bootstrapped admin: %d (%v)", sessions, err)
	}
	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false); err != nil {
		t.Fatalf("the admin logs in with the configured password: %v", err)
	}
}

func TestBootstrapAdmin_OnlyWhenThereIsNoUser(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "someone@example.com", goodPassword, nil)

	err := e.svc.BootstrapAdmin(context.Background(), "Rina", "rina@example.com", goodPassword)

	if !errors.Is(err, ErrSetupDone) {
		t.Fatalf("got %v, want ErrSetupDone", err)
	}
}

func TestBootstrapAdmin_ValidatesLikeSetup(t *testing.T) {
	e := newEnv(t)

	for name, tt := range map[string]struct{ name, email, password, want string }{
		"short password": {"Rina", "rina@example.com", "short", "Password must be at least 12 characters."},
		"bad email":      {"Rina", "not-an-email", goodPassword, "Enter a valid email address."},
		"blank name":     {" ", "rina@example.com", goodPassword, "Enter your name."},
	} {
		err := e.svc.BootstrapAdmin(context.Background(), tt.name, tt.email, tt.password)

		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Message != tt.want {
			t.Errorf("%s: got %v, want %q", name, err, tt.want)
		}
	}
}

func TestEnsureDefaultAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	created, err := e.svc.EnsureDefaultAdmin(ctx)
	if err != nil || !created {
		t.Fatalf("empty database: %v, %v", created, err)
	}

	u, err := e.st.Users.GetByEmail(ctx, "admin")
	if err != nil || u.Role != store.RoleSuperAdmin || !u.MustChangePassword || u.PasswordHash == nil {
		t.Fatalf("default admin = %+v, %v", u, err)
	}
	if ok, _ := Verify("admin", *u.PasswordHash); !ok {
		t.Fatal("the default password is 'admin'")
	}
	if again, err := e.svc.EnsureDefaultAdmin(ctx); err != nil || again {
		t.Fatalf("a second call must do nothing: %v, %v", again, err)
	}
	if n, _ := e.st.Users.Count(ctx); n != 1 {
		t.Fatalf("got %d users", n)
	}
}

func TestEnsureDefaultAdmin_NeverAddsToAnExistingInstall(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "rina@example.com", goodPassword, nil)

	created, err := e.svc.EnsureDefaultAdmin(context.Background())

	if err != nil || created {
		t.Fatalf("got %v, %v: an install with users must never get a well-known account", created, err)
	}
	if _, err := e.st.Users.GetByEmail(context.Background(), "admin"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("no admin account may appear")
	}
}

func TestLogin_TheDefaultAdmin(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.EnsureDefaultAdmin(ctx)

	for _, login := range []string{"admin", "Admin", "  ADMIN "} {
		got, err := e.svc.Login(ctx, login, "admin", "1.2.3.4", false)
		if err != nil {
			t.Fatalf("%q: %v", login, err)
		}
		if !got.User.MustChangePassword {
			t.Fatalf("%q: the session must know the account still has to change its password", login)
		}
	}
	if _, err := e.svc.Login(ctx, "admin", "wrong", "1.2.3.4", false); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("got %v", err)
	}
}

func TestLogin_ANameThatIsNotAnEmailIsRejectedUnlessItIsTheDefaultAdmin(t *testing.T) {
	e := newEnv(t)
	e.addUser(t, "rina@example.com", goodPassword, nil)

	for _, login := range []string{"rina", "root", "administrator", "admin@", "admin "} {
		if _, err := e.svc.Login(context.Background(), login, goodPassword, "1.2.3.4", false); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%q: got %v", login, err)
		}
	}
}

func TestDefaultAdminActive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if active, err := e.svc.DefaultAdminActive(ctx); err != nil || active {
		t.Fatalf("no such account: %v, %v", active, err)
	}
	e.svc.EnsureDefaultAdmin(ctx)
	if active, err := e.svc.DefaultAdminActive(ctx); err != nil || !active {
		t.Fatalf("unchanged default account: %v, %v", active, err)
	}
}

func TestChangeCredentials(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.EnsureDefaultAdmin(ctx)
	old, _ := e.svc.Login(ctx, "admin", "admin", "1.2.3.4", false)

	got, err := e.svc.ChangeCredentials(ctx, old.User, " Rina ", "Rina@Example.com", goodPassword, goodPassword)
	if err != nil {
		t.Fatalf("change: %v", err)
	}

	if got.User.Email != "rina@example.com" || got.User.Name != "Rina" || got.User.MustChangePassword || got.User.Role != store.RoleSuperAdmin {
		t.Fatalf("user = %+v", got.User)
	}
	if got.Token == "" || got.Token == old.Token {
		t.Fatal("a fresh session must be issued")
	}
	if _, err := e.svc.Authenticate(ctx, old.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v: the session opened with admin/admin must be dead", err)
	}
	if _, err := e.svc.Authenticate(ctx, got.Token); err != nil {
		t.Fatalf("the new session works: %v", err)
	}
	if _, err := e.svc.Login(ctx, "admin", "admin", "1.2.3.4", false); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("got %v: admin/admin must stop working", err)
	}
	if _, err := e.svc.Login(ctx, "rina@example.com", goodPassword, "1.2.3.4", false); err != nil {
		t.Fatalf("the new credentials work: %v", err)
	}
	if active, _ := e.svc.DefaultAdminActive(ctx); active {
		t.Fatal("the default account is gone once it has been changed")
	}
}

func TestChangeCredentials_EndsEverySessionOfTheUser(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.EnsureDefaultAdmin(ctx)
	first, _ := e.svc.Login(ctx, "admin", "admin", "1.2.3.4", false)
	second, _ := e.svc.Login(ctx, "admin", "admin", "5.6.7.8", false) // someone else who also knows admin/admin

	if _, err := e.svc.ChangeCredentials(ctx, first.User, "Rina", "rina@example.com", goodPassword, goodPassword); err != nil {
		t.Fatalf("change: %v", err)
	}

	if _, err := e.svc.Authenticate(ctx, second.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("got %v: a second session opened with the default credentials must not outlive the change", err)
	}
}

func TestChangeCredentials_Validation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.EnsureDefaultAdmin(ctx)
	e.addUser(t, "taken@example.com", goodPassword, nil)
	in, _ := e.svc.Login(ctx, "admin", "admin", "1.2.3.4", false)

	for name, tt := range map[string]struct{ name, email, password, confirm, want string }{
		"blank name":     {" ", "rina@example.com", goodPassword, goodPassword, "Enter your name."},
		"no email":       {"Rina", "", goodPassword, goodPassword, "Enter a valid email address."},
		"still 'admin'":  {"Rina", "admin", goodPassword, goodPassword, "Enter a valid email address."},
		"short password": {"Rina", "rina@example.com", "admin", "admin", "Password must be at least 12 characters."},
		"mismatch":       {"Rina", "rina@example.com", goodPassword, goodPassword + "x", "Passwords do not match."},
		"email in use":   {"Rina", "TAKEN@example.com", goodPassword, goodPassword, "That email is already used."},
	} {
		_, err := e.svc.ChangeCredentials(ctx, in.User, tt.name, tt.email, tt.password, tt.confirm)

		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Message != tt.want {
			t.Errorf("%s: got %v, want %q", name, err, tt.want)
		}
	}
	if _, err := e.svc.Login(ctx, "admin", "admin", "1.2.3.4", false); err != nil {
		t.Fatalf("rejected changes must leave the account as it was: %v", err)
	}
}

func TestChangeCredentials_OnlyForAccountsThatMustChange(t *testing.T) {
	e := newEnv(t)
	u := e.addUser(t, "rina@example.com", goodPassword, nil)

	_, err := e.svc.ChangeCredentials(context.Background(), u, "Rina", "new@example.com", goodPassword, goodPassword)

	if !errors.Is(err, ErrNoChangeNeeded) {
		t.Fatalf("got %v: this path exists for the forced change, not as a backdoor to rewrite any account", err)
	}
}
