package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/ratelimit"
	"github.com/androsyz/wasabot/internal/store"
)

//go:embed templates static
var assets embed.FS

const (
	setupGuessLimit  = 10
	setupGuessWindow = 15 * time.Minute
)

var pageNames = []string{"clients", "login", "setup", "welcome", "invite", "forgot", "code", "notfound"}

type Options struct {
	Addr    string
	Log     *slog.Logger
	Store   *store.Store
	Auth    *auth.Service
	Runtime Runtime // may be nil
	Agent   string  // shown in the "active agent & model" column
	Preview bool    // serve sample data for pages that have no backend yet

	// SetupCode must be entered on the first-run setup page, so that whoever reaches /setup first
	// cannot claim the admin account. The operator chooses it (or it is generated and logged).
	// Empty switches the setup page off.
	SetupCode string
	// CookieSecure marks cookies Secure even when TLS ends at a proxy. Direct TLS is detected.
	CookieSecure bool
	Now          func() time.Time
}

type Server struct {
	opts    Options
	pages   map[string]*template.Template
	favicon []byte
	files   http.Handler
	// wrong setup codes per address, so the code cannot be guessed
	setupGuesses *ratelimit.Limiter
	http         *http.Server
	addr         string
}

func New(o Options) (*Server, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Auth == nil {
		return nil, errors.New("web ui: an auth service is required")
	}

	pages := make(map[string]*template.Template, len(pageNames))
	for _, name := range pageNames {
		t, err := template.New(name).Funcs(template.FuncMap{"sprite": spriteHTML}).
			ParseFS(assets, "templates/layout.gohtml", "templates/auth.gohtml", "templates/pages/"+name+".gohtml")
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, err)
		}
		pages[name] = t
	}

	favicon, err := faviconSVG("mascot")
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, fmt.Errorf("static assets: %w", err)
	}
	return &Server{
		opts:         o,
		pages:        pages,
		favicon:      favicon,
		files:        http.StripPrefix("/static/", http.FileServerFS(sub)),
		setupGuesses: ratelimit.New(setupGuessLimit, setupGuessWindow),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/clients", http.StatusFound)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "ok")
	})
	mux.HandleFunc("GET /favicon.svg", s.serveFavicon)
	mux.Handle("GET /static/", s.static())

	mux.HandleFunc("GET /clients", s.protected(s.clients))
	mux.HandleFunc("POST /logout", s.protectedWith(true, s.logout))
	mux.HandleFunc("GET /welcome", s.protectedWith(true, s.welcomePage))
	mux.HandleFunc("POST /welcome", s.protectedWith(true, s.welcomeSubmit))

	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("GET /setup", s.setupPage)
	mux.HandleFunc("POST /setup", s.setupSubmit)

	// Password reset needs email delivery, which is not built, so submitting says so.
	for _, p := range []struct{ path, page, title, msg string }{
		{"/forgot", "forgot", "Forgot password", "Password reset is not available yet."},
		{"/forgot/code", "code", "Enter your code", "Code verification is not available yet."},
	} {
		mux.HandleFunc("GET "+p.path, s.authPage(p.page, p.title, ""))
		mux.HandleFunc("POST "+p.path, s.authPage(p.page, p.title, p.msg))
	}
	mux.HandleFunc("GET /invite/{token}", s.inviteInvalid)
	if s.opts.Preview {
		mux.HandleFunc("GET /preview/invite", s.invitePreview)
	}
	mux.HandleFunc("GET /", s.notFound)

	return recoverPanics(s.opts.Log, securityHeaders(sameOrigin(logRequests(s.opts.Log, mux))))
}

// Start listens right away, so a busy port is reported to the caller, and serves in the background.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return fmt.Errorf("web ui: listen on %s: %w", s.opts.Addr, err)
	}
	s.addr = ln.Addr().String()
	s.http = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.opts.Log.Error("web ui stopped", "error", err)
		}
	}()
	return nil
}

func (s *Server) Addr() string { return s.addr }

func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

func (s *Server) render(w http.ResponseWriter, status int, name, entry string, data any) {
	var buf bytes.Buffer
	if err := s.pages[name].ExecuteTemplate(&buf, entry, data); err != nil {
		s.opts.Log.Error("render page", "page", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) clients(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	q := r.URL.Query()
	filter := q.Get("status")
	if !validFilter(filter) {
		filter = "all"
	}

	data, err := s.clientsPage(r.Context(), id, strings.TrimSpace(q.Get("q")), filter, q.Get("client"))
	if err != nil {
		s.opts.Log.Error("load clients", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// htmx asks for just the table when the filters change
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Target") == "clients-results" {
		s.render(w, http.StatusOK, "clients", "clients_table", data.Body)
		return
	}
	s.render(w, http.StatusOK, "clients", "page", data)
}

func validFilter(v string) bool {
	for _, f := range statusFilters {
		if f.Value == v {
			return true
		}
	}
	return false
}

type authData struct {
	Title   string
	Notice  string
	Invite  inviteData
	Heading string
	Message string
	Back    link

	CSRF      string
	Email     string // what the user typed, to fill the form again after an error
	Name      string
	SetupCode string

	DefaultLogin bool // the admin/admin account is still unchanged
}

type link struct{ Href, Label string }

type inviteData struct {
	Client, Inviter, Role, Email, Action string
}

func (s *Server) authPage(name, title, notice string) http.HandlerFunc {
	status := http.StatusOK
	if notice != "" {
		status = http.StatusNotImplemented
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		s.render(w, status, name, "page", authData{Title: title, Notice: notice})
	}
}

func (s *Server) inviteInvalid(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusNotFound, "notfound", "page", authData{
		Title:   "Invitation",
		Heading: "This invitation is not valid",
		Message: "The link is wrong or has expired. Ask your admin for a new invite.",
		Back:    link{Href: "/login", Label: "Back to log in"},
	})
}

func (s *Server) invitePreview(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusOK, "invite", "page", authData{
		Title: "Join",
		Invite: inviteData{
			Client:  "Toko Kopi Senja",
			Inviter: "Rina",
			Role:    "Client",
			Email:   "rina@example.com",
			Action:  "/preview/invite",
		},
	})
}

func (s *Server) notFound(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusNotFound, "notfound", "page", authData{
		Title:   "Not found",
		Heading: "Page not found",
		Message: "There is nothing at this address.",
		Back:    link{Href: "/clients", Label: "Go to the dashboard"},
	})
}

func (s *Server) serveFavicon(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(s.favicon)
}

// static serves the embedded files. Fonts never change, so they are cached; the rest is revalidated.
func (s *Server) static() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if path.Ext(r.URL.Path) == ".woff2" {
			w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		s.files.ServeHTTP(w, r)
	})
}
