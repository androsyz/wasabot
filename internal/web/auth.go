package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/androsyz/wasabot/internal/auth"
)

const (
	sessionCookie = "wasabot_session"
	csrfCookie    = "wasabot_csrf"
	csrfField     = "csrf"
	csrfHeader    = "X-CSRF-Token"
	maxFormBytes  = 1 << 16
)

type identityKey struct{}

func (s *Server) secure(r *http.Request) bool {
	return s.opts.CookieSecure || r.TLS != nil
}

func (s *Server) cookie(r *http.Request, name, value string) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: s.secure(r)}
}

// setSession stores the session cookie. Without "remember me" it lasts until the browser closes.
func (s *Server) setSession(w http.ResponseWriter, r *http.Request, login auth.LoggedIn, remember bool) {
	c := s.cookie(r, sessionCookie, login.Token)
	if remember {
		c.Expires = login.ExpiresAt
		c.MaxAge = int(login.ExpiresAt.Sub(s.opts.Now()).Seconds())
	}
	http.SetCookie(w, c)
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	c := s.cookie(r, sessionCookie, "")
	c.MaxAge = -1
	http.SetCookie(w, c)
}

// authenticate resolves the session cookie. A dead cookie is cleared.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (auth.Identity, bool, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return auth.Identity{}, false, nil
	}
	id, err := s.opts.Auth.Authenticate(r.Context(), c.Value)
	switch {
	case errors.Is(err, auth.ErrNoSession):
		s.clearSession(w, r)
		return auth.Identity{}, false, nil
	case err != nil:
		return auth.Identity{}, false, err
	}
	return id, true, nil
}

// protected runs h only for a logged-in user, and requires the CSRF token on anything that changes state.
// An account that still has the default credentials is sent to /welcome first.
func (s *Server) protected(h func(http.ResponseWriter, *http.Request, auth.Identity)) http.HandlerFunc {
	return s.protectedWith(false, h)
}

// protectedWith is protected, optionally letting through an account that must still change its password:
// only the page where it does so, and logging out.
func (s *Server) protectedWith(allowMustChange bool, h func(http.ResponseWriter, *http.Request, auth.Identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok, err := s.authenticate(w, r)
		if err != nil {
			s.fail(w, "authenticate", err)
			return
		}
		if !ok {
			s.redirectToLogin(w, r)
			return
		}
		if id.User.MustChangePassword && !allowMustChange {
			http.Redirect(w, r, "/welcome", http.StatusSeeOther)
			return
		}
		if !safeMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
			if !validCSRF(r, id.CSRFToken) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)), id)
	}
}

func (s *Server) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	needs, err := s.opts.Auth.NeedsSetup(r.Context())
	if err != nil {
		s.fail(w, "check setup", err)
		return
	}
	target := "/login"
	if needs {
		target = "/setup"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) fail(w http.ResponseWriter, what string, err error) {
	s.opts.Log.Error(what, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// validCSRF accepts the token from a header (htmx) or a form field, compared in constant time.
func validCSRF(r *http.Request, want string) bool {
	got := r.Header.Get(csrfHeader)
	if got == "" {
		got = r.PostFormValue(csrfField)
	}
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// anonCSRF hands out the token for forms shown before login. It is a double-submit cookie:
// the form must send back the same value the browser holds in an HttpOnly cookie.
func (s *Server) anonCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookie); err == nil && plausibleToken(c.Value) {
		return c.Value
	}
	token, err := auth.NewCSRFToken()
	if err != nil {
		s.opts.Log.Error("create csrf token", "error", err)
		return ""
	}
	http.SetCookie(w, s.cookie(r, csrfCookie, token))
	return token
}

func validAnonCSRF(r *http.Request) bool {
	c, err := r.Cookie(csrfCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.PostFormValue(csrfField))) == 1
}

func plausibleToken(v string) bool {
	if len(v) != 43 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// sameOrigin rejects browser requests that come from another site. Requests without either
// header (curl, tests) pass here and still need a CSRF token.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		} else if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) authForm(w http.ResponseWriter, r *http.Request, title string) authData {
	return authData{Title: title, CSRF: s.anonCSRF(w, r)}
}

// loginData is the login form, with a hint about admin/admin while that account is still unchanged.
func (s *Server) loginData(w http.ResponseWriter, r *http.Request) authData {
	data := s.authForm(w, r, "Log in")
	active, err := s.opts.Auth.DefaultAdminActive(r.Context())
	if err != nil {
		s.opts.Log.Error("check default admin", "error", err)
	}
	data.DefaultLogin = active
	return data
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok, err := s.authenticate(w, r); err != nil {
		s.fail(w, "authenticate", err)
		return
	} else if ok {
		http.Redirect(w, r, "/clients", http.StatusSeeOther)
		return
	}
	if needs, err := s.opts.Auth.NeedsSetup(r.Context()); err != nil {
		s.fail(w, "check setup", err)
		return
	} else if needs {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login", "page", s.loginData(w, r))
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	data := s.loginData(w, r)
	data.Email = strings.TrimSpace(r.PostFormValue("email"))

	if !validAnonCSRF(r) {
		data.Notice = "Your form expired. Please try again."
		s.render(w, http.StatusForbidden, "login", "page", data)
		return
	}

	remember := r.PostFormValue("remember") != ""
	login, err := s.opts.Auth.Login(r.Context(), data.Email, r.PostFormValue("password"), clientAddr(r), remember)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		data.Notice = "Wrong email or password."
		s.render(w, http.StatusUnauthorized, "login", "page", data)
	case errors.Is(err, auth.ErrTooManyAttempts):
		data.Notice = "Too many attempts. Please wait a few minutes and try again."
		w.Header().Set("Retry-After", "900")
		s.render(w, http.StatusTooManyRequests, "login", "page", data)
	case err != nil:
		s.fail(w, "login", err)
	default:
		s.setSession(w, r, login, remember)
		http.Redirect(w, r, "/clients", http.StatusSeeOther)
	}
}

func (s *Server) setupCodeOK(given string) bool {
	return s.opts.SetupCode != "" && subtle.ConstantTimeCompare([]byte(given), []byte(s.opts.SetupCode)) == 1
}

// setupAvailable sends people away when there is nothing to set up, and explains when setup is off.
func (s *Server) setupAvailable(w http.ResponseWriter, r *http.Request) bool {
	needs, err := s.opts.Auth.NeedsSetup(r.Context())
	if err != nil {
		s.fail(w, "check setup", err)
		return false
	}
	if !needs {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return false
	}
	if s.opts.SetupCode == "" {
		s.render(w, http.StatusForbidden, "notfound", "page", authData{
			Title:   "Setup",
			Heading: "Setup is switched off",
			Message: "Set WASABOT_SETUP_CODE on the server and restart it, or set WASABOT_ADMIN_EMAIL and WASABOT_ADMIN_PASSWORD.",
		})
		return false
	}
	return true
}

func (s *Server) setupPage(w http.ResponseWriter, r *http.Request) {
	if !s.setupAvailable(w, r) {
		return
	}
	data := s.authForm(w, r, "Welcome")
	s.render(w, http.StatusOK, "setup", "page", data)
}

func (s *Server) setupSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.setupAvailable(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)

	data := s.authForm(w, r, "Welcome")
	data.Name = strings.TrimSpace(r.PostFormValue("name"))
	data.Email = strings.TrimSpace(r.PostFormValue("email"))
	if !validAnonCSRF(r) {
		data.Notice = "Your form expired. Please try again."
		s.render(w, http.StatusForbidden, "setup", "page", data)
		return
	}

	addr, now := clientAddr(r), s.opts.Now()
	if !s.setupGuesses.Check(addr, now) {
		data.Notice = "Too many wrong setup codes. Please wait a few minutes and try again."
		w.Header().Set("Retry-After", "900")
		s.render(w, http.StatusTooManyRequests, "setup", "page", data)
		return
	}
	code := r.PostFormValue("code")
	if !s.setupCodeOK(code) {
		s.setupGuesses.Record(addr, now)
		data.Notice = "That setup code is not right."
		s.render(w, http.StatusForbidden, "setup", "page", data)
		return
	}
	data.SetupCode = code // already proven, so it can stay in the form if another field needs fixing

	login, err := s.opts.Auth.CreateFirstAdmin(r.Context(), data.Name, data.Email, r.PostFormValue("password"), r.PostFormValue("confirm"))
	var invalid *auth.ValidationError
	switch {
	case errors.As(err, &invalid):
		data.Notice = invalid.Message
		s.render(w, http.StatusBadRequest, "setup", "page", data)
	case errors.Is(err, auth.ErrSetupDone):
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	case err != nil:
		s.fail(w, "create admin", err)
	default:
		s.setSession(w, r, login, false)
		http.Redirect(w, r, "/clients", http.StatusSeeOther)
	}
}

func (s *Server) welcomePage(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	if !id.User.MustChangePassword {
		http.Redirect(w, r, "/clients", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "welcome", "page", authData{Title: "Secure your account", CSRF: id.CSRFToken, Name: id.User.Name})
}

func (s *Server) welcomeSubmit(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	data := authData{
		Title: "Secure your account", CSRF: id.CSRFToken,
		Name: strings.TrimSpace(r.PostFormValue("name")), Email: strings.TrimSpace(r.PostFormValue("email")),
	}
	login, err := s.opts.Auth.ChangeCredentials(r.Context(), id.User, data.Name, data.Email, r.PostFormValue("password"), r.PostFormValue("confirm"))
	var invalid *auth.ValidationError
	switch {
	case errors.As(err, &invalid):
		data.Notice = invalid.Message
		s.render(w, http.StatusBadRequest, "welcome", "page", data)
	case errors.Is(err, auth.ErrNoChangeNeeded):
		http.Redirect(w, r, "/clients", http.StatusSeeOther)
	case err != nil:
		s.fail(w, "change credentials", err)
	default:
		s.setSession(w, r, login, false)
		http.Redirect(w, r, "/clients", http.StatusSeeOther)
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ auth.Identity) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.opts.Auth.Logout(r.Context(), c.Value); err != nil {
			s.opts.Log.Error("logout", "error", err)
		}
	}
	s.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
