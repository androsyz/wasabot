package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/androsyz/wasabot/internal/ratelimit"
	"github.com/androsyz/wasabot/internal/store"
)

// DefaultAdminLogin is the login of the account made when nothing else is configured. Its password
// is the same word; the account can do nothing but choose real credentials (User.MustChangePassword).
const (
	DefaultAdminLogin    = "admin"
	defaultAdminPassword = "admin"
)

const (
	defaultSessionTTL  = 12 * time.Hour
	defaultRememberTTL = 30 * 24 * time.Hour
	defaultWindow      = 15 * time.Minute
	defaultAccountFail = 8
	defaultIPAttempts  = 30
	maxEmailLength     = 254
	maxNameLength      = 100
)

var (
	// ErrInvalidCredentials is the only login failure shown to users: it never says whether the email exists.
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrTooManyAttempts    = errors.New("too many attempts")
	ErrNoSession          = errors.New("no valid session")
	ErrSetupDone          = errors.New("setup is already done")
	ErrNoChangeNeeded     = errors.New("this account does not need new credentials")
)

// ValidationError carries a message that is safe and useful to show next to a form.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

type Options struct {
	SessionTTL      time.Duration // default 12h
	RememberTTL     time.Duration // default 30 days
	Hasher          *Hasher       // default DefaultParams
	AttemptWindow   time.Duration // default 15m
	AccountFailures int           // failed logins per account per window before a lockout, default 8
	IPAttempts      int           // login attempts per address per window, default 30
	Now             func() time.Time
}

type Service struct {
	users       *store.Users
	sessions    *store.Sessions
	hasher      *Hasher
	dummyHash   string
	now         func() time.Time
	sessionTTL  time.Duration
	rememberTTL time.Duration
	accounts    *ratelimit.Limiter
	addresses   *ratelimit.Limiter
}

func NewService(st *store.Store, o Options) (*Service, error) {
	if o.Hasher == nil {
		o.Hasher = NewHasher(DefaultParams)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	o.SessionTTL = cmpOr(o.SessionTTL, defaultSessionTTL)
	o.RememberTTL = cmpOr(o.RememberTTL, defaultRememberTTL)
	o.AttemptWindow = cmpOr(o.AttemptWindow, defaultWindow)
	if o.AccountFailures == 0 {
		o.AccountFailures = defaultAccountFail
	}
	if o.IPAttempts == 0 {
		o.IPAttempts = defaultIPAttempts
	}

	// verifying against this when an email is unknown costs the same as a real check
	dummy, err := o.Hasher.Hash("a password nobody has")
	if err != nil {
		return nil, err
	}
	return &Service{
		users:       st.Users,
		sessions:    st.Sessions,
		hasher:      o.Hasher,
		dummyHash:   dummy,
		now:         o.Now,
		sessionTTL:  o.SessionTTL,
		rememberTTL: o.RememberTTL,
		accounts:    ratelimit.New(o.AccountFailures, o.AttemptWindow),
		addresses:   ratelimit.New(o.IPAttempts, o.AttemptWindow),
	}, nil
}

func cmpOr(v, fallback time.Duration) time.Duration {
	if v == 0 {
		return fallback
	}
	return v
}

// Identity is who a request belongs to, plus the session's CSRF token.
type Identity struct {
	User      store.User
	CSRFToken string
	ExpiresAt time.Time
}

// LoggedIn is what a successful login or setup returns; Token goes into the cookie.
type LoggedIn struct {
	Token string
	Identity
}

func (s *Service) NeedsSetup(ctx context.Context) (bool, error) {
	n, err := s.users.Count(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// CreateFirstAdmin creates the super admin and logs them in. It works only while there are no users.
func (s *Service) CreateFirstAdmin(ctx context.Context, name, email, password, confirm string) (LoggedIn, error) {
	user, err := s.createFirstAdmin(ctx, name, email, password, confirm)
	if err != nil {
		return LoggedIn{}, err
	}
	return s.startSession(ctx, user, false)
}

// EnsureDefaultAdmin creates the admin/admin account when there is no user at all, and reports
// whether it did. The account is marked MustChangePassword.
func (s *Service) EnsureDefaultAdmin(ctx context.Context) (bool, error) {
	hash, err := s.hasher.Hash(defaultAdminPassword)
	if err != nil {
		return false, err
	}
	_, err = s.users.CreateIfNone(ctx, store.User{
		Email: DefaultAdminLogin, Name: "Admin", PasswordHash: &hash, Role: store.RoleSuperAdmin, MustChangePassword: true,
	})
	if errors.Is(err, store.ErrUsersExist) {
		return false, nil
	}
	return err == nil, err
}

// DefaultAdminActive reports whether the admin/admin account still exists unchanged, so the
// login page can tell people how to get in the first time.
func (s *Service) DefaultAdminActive(ctx context.Context) (bool, error) {
	u, err := s.users.GetByEmail(ctx, DefaultAdminLogin)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return u.MustChangePassword, nil
}

// ChangeCredentials replaces the name, email and password of an account that must change them.
// Every existing session of the user is ended and a fresh one is returned, so a session
// opened with the old credentials cannot survive the change.
func (s *Service) ChangeCredentials(ctx context.Context, user store.User, name, email, password, confirm string) (LoggedIn, error) {
	if !user.MustChangePassword {
		return LoggedIn{}, ErrNoChangeNeeded
	}
	name, email, hash, err := s.validateCredentials(name, email, password, confirm)
	if err != nil {
		return LoggedIn{}, err
	}

	switch err := s.users.UpdateCredentials(ctx, user.ID, name, email, hash); {
	case errors.Is(err, store.ErrEmailTaken):
		return LoggedIn{}, &ValidationError{"That email is already used."}
	case err != nil:
		return LoggedIn{}, err
	}
	if err := s.sessions.DeleteByUser(ctx, user.ID); err != nil {
		return LoggedIn{}, err
	}
	updated, err := s.users.GetByID(ctx, user.ID)
	if err != nil {
		return LoggedIn{}, err
	}
	return s.startSession(ctx, updated, false)
}

// BootstrapAdmin creates the super admin from settings, for hosts with no browser at first start.
// Unlike CreateFirstAdmin it starts no session. It returns ErrSetupDone if a user already exists.
func (s *Service) BootstrapAdmin(ctx context.Context, name, email, password string) error {
	_, err := s.createFirstAdmin(ctx, name, email, password, password)
	return err
}

func (s *Service) createFirstAdmin(ctx context.Context, name, email, password, confirm string) (store.User, error) {
	name, email, hash, err := s.validateCredentials(name, email, password, confirm)
	if err != nil {
		return store.User{}, err
	}
	user, err := s.users.CreateIfNone(ctx, store.User{Email: email, Name: name, PasswordHash: &hash, Role: store.RoleSuperAdmin})
	if errors.Is(err, store.ErrUsersExist) {
		return store.User{}, ErrSetupDone
	}
	if err != nil {
		return store.User{}, err
	}
	return user, nil
}

// validateCredentials checks what a person typed for name, email and password, and hashes the password.
func (s *Service) validateCredentials(name, email, password, confirm string) (cleanName, cleanEmail, hash string, err error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength {
		return "", "", "", &ValidationError{"Enter your name."}
	}
	email, ok := normalizeEmail(email)
	if !ok {
		return "", "", "", &ValidationError{"Enter a valid email address."}
	}
	switch err := ValidatePassword(password); {
	case errors.Is(err, ErrPasswordTooShort):
		return "", "", "", &ValidationError{fmt.Sprintf("Password must be at least %d characters.", MinPasswordLength)}
	case errors.Is(err, ErrPasswordTooLong):
		return "", "", "", &ValidationError{"Password is too long."}
	}
	if password != confirm {
		return "", "", "", &ValidationError{"Passwords do not match."}
	}
	hash, err = s.hasher.Hash(password)
	if err != nil {
		return "", "", "", err
	}
	return name, email, hash, nil
}

// Login checks the credentials and starts a session. addr identifies the client, for rate limiting.
// Wrong email, wrong password, a disabled account and an account without a password all return
// ErrInvalidCredentials after the same amount of work.
func (s *Service) Login(ctx context.Context, email, password, addr string, remember bool) (LoggedIn, error) {
	now := s.now()
	if !s.addresses.Allow(addr, now) {
		return LoggedIn{}, ErrTooManyAttempts
	}
	email, valid := normalizeLogin(email)
	if !valid || len(password) > maxPasswordBytes {
		Verify(password[:min(len(password), 8)], s.dummyHash) // keep the cost the same
		return LoggedIn{}, ErrInvalidCredentials
	}
	if !s.accounts.Check(email, now) {
		return LoggedIn{}, ErrTooManyAttempts
	}

	user, err := s.users.GetByEmail(ctx, email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return s.reject(email, password), ErrInvalidCredentials
	case err != nil:
		return LoggedIn{}, err
	}
	if user.Disabled || user.PasswordHash == nil {
		return s.reject(email, password), ErrInvalidCredentials
	}

	ok, err := Verify(password, *user.PasswordHash)
	if err != nil {
		return LoggedIn{}, fmt.Errorf("verify password of user %d: %w", user.ID, err)
	}
	if !ok {
		return s.reject(email, password), ErrInvalidCredentials
	}

	s.accounts.Reset(email)
	if s.hasher.NeedsRehash(*user.PasswordHash) {
		if hash, err := s.hasher.Hash(password); err == nil {
			s.users.SetPasswordHash(ctx, user.ID, hash) // best effort: the login itself succeeded
		}
	}
	return s.startSession(ctx, user, remember)
}

// reject spends the time of a real check and counts the failure against the account.
func (s *Service) reject(email, password string) LoggedIn {
	Verify(password, s.dummyHash)
	s.accounts.Record(email, s.now())
	return LoggedIn{}
}

func (s *Service) startSession(ctx context.Context, user store.User, remember bool) (LoggedIn, error) {
	token, hash, err := NewToken()
	if err != nil {
		return LoggedIn{}, err
	}
	csrf, err := NewCSRFToken()
	if err != nil {
		return LoggedIn{}, err
	}

	now := s.now()
	ttl := s.sessionTTL
	if remember {
		ttl = s.rememberTTL
	}
	sess := store.Session{IDHash: hash, UserID: user.ID, CSRFToken: csrf, ExpiresAt: now.Add(ttl)}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return LoggedIn{}, err
	}
	s.sessions.DeleteExpired(ctx, now) // housekeeping; a failure here does not matter

	return LoggedIn{Token: token, Identity: Identity{User: user, CSRFToken: csrf, ExpiresAt: sess.ExpiresAt}}, nil
}

// Authenticate resolves a cookie value to an identity, or returns ErrNoSession.
func (s *Service) Authenticate(ctx context.Context, token string) (Identity, error) {
	if token == "" || len(token) > maxTokenChars {
		return Identity{}, ErrNoSession
	}
	hash := HashToken(token)

	sess, user, err := s.sessions.Get(ctx, hash, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return Identity{}, ErrNoSession
	}
	if err != nil {
		return Identity{}, err
	}
	if user.Disabled {
		s.sessions.Delete(ctx, hash)
		return Identity{}, ErrNoSession
	}
	return Identity{User: user, CSRFToken: sess.CSRFToken, ExpiresAt: sess.ExpiresAt}, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" || len(token) > maxTokenChars {
		return nil
	}
	return s.sessions.Delete(ctx, HashToken(token))
}

// normalizeEmail lowercases and trims, and rejects anything that is not a plain address.
func normalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > maxEmailLength {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@"):], ".") {
		return "", false
	}
	return email, true
}

// normalizeLogin is normalizeEmail, except that the default admin's login is not an email address.
func normalizeLogin(login string) (string, bool) {
	if l := strings.ToLower(strings.TrimSpace(login)); l == DefaultAdminLogin {
		return l, true
	}
	return normalizeEmail(login)
}
