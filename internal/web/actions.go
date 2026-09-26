package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"rsc.io/qr"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/manager"
	"github.com/androsyz/wasabot/internal/store"
)

const (
	maxClientNameRunes = 60
	logoutTimeout      = 25 * time.Second // under the server's write timeout
	qrQuietZone        = 4                // modules of white around the code, which scanners need
)

// notices are the only messages ?notice= can show, so a link cannot put text on the page.
var notices = map[string]string{
	"start_failed":  "WhatsApp could not be started. The logs say why.",
	"logout_failed": "The WhatsApp number could not be unlinked. The logs say why.",
}

// clientFor finds the client in the request path. A client the user may not manage is reported as
// missing, so its existence is not revealed.
func (s *Server) clientFor(w http.ResponseWriter, r *http.Request, id auth.Identity) (store.Client, bool) {
	clientID := parseID(r.PathValue("id"))
	if clientID == 0 || s.opts.Runtime == nil {
		s.notFound(w, r)
		return store.Client{}, false
	}

	var (
		c   store.Client
		err error
	)
	if id.User.Role == store.RoleSuperAdmin {
		c, err = s.opts.Store.Clients.GetByID(r.Context(), clientID)
	} else {
		c, err = s.memberClient(r.Context(), id.User.ID, clientID)
	}
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return store.Client{}, false
	}
	if err != nil {
		s.fail(w, "load client", err)
		return store.Client{}, false
	}
	return c, true
}

func (s *Server) memberClient(ctx context.Context, userID, clientID int64) (store.Client, error) {
	clients, err := s.opts.Store.Clients.ListByUser(ctx, userID)
	if err != nil {
		return store.Client{}, err
	}
	for _, c := range clients {
		if c.ID == clientID {
			return c, nil
		}
	}
	return store.Client{}, store.ErrNotFound
}

// clientAction runs do for the client in the path, then goes back to the dashboard. It is a plain
// form post, so it works before the page's scripts have loaded.
func (s *Server) clientAction(noticeOnError string, do func(ctx context.Context, rt Runtime, clientID int64) error) func(http.ResponseWriter, *http.Request, auth.Identity) {
	return func(w http.ResponseWriter, r *http.Request, id auth.Identity) {
		c, ok := s.clientFor(w, r, id)
		if !ok {
			return
		}
		target := "/clients"
		// a client that lost or gained its number since the page was drawn needs no message
		if err := do(r.Context(), s.opts.Runtime, c.ID); err != nil &&
			!errors.Is(err, manager.ErrNotLinked) && !errors.Is(err, manager.ErrAlreadyLinked) {
			s.opts.Log.Error("client action", "client_id", c.ID, "path", r.URL.Path, "error", err)
			target += "?notice=" + noticeOnError
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}

func (s *Server) startClient() func(http.ResponseWriter, *http.Request, auth.Identity) {
	return s.clientAction("start_failed", func(_ context.Context, rt Runtime, id int64) error { return rt.Start(id) })
}

func (s *Server) stopClient() func(http.ResponseWriter, *http.Request, auth.Identity) {
	return s.clientAction("", func(_ context.Context, rt Runtime, id int64) error { rt.Stop(id); return nil })
}

func (s *Server) logoutClient() func(http.ResponseWriter, *http.Request, auth.Identity) {
	return s.clientAction("logout_failed", func(ctx context.Context, rt Runtime, id int64) error {
		ctx, cancel := context.WithTimeout(ctx, logoutTimeout)
		defer cancel()
		return rt.Logout(ctx, id)
	})
}

type pairData struct {
	ClientID int64
	Name     string
	QR       template.HTML
	Failed   string // why pairing ended, when it did
}

// pairStart begins pairing and answers with the panel that shows the code.
func (s *Server) pairStart(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	c, ok := s.clientFor(w, r, id)
	if !ok {
		return
	}
	if err := s.opts.Runtime.Pair(c.ID); err != nil && !errors.Is(err, manager.ErrAlreadyLinked) {
		s.fail(w, "start pairing", err)
		return
	}
	s.pairPanel(w, c)
}

// pairPoll is fetched every few seconds by the panel, to show the current code and to notice
// when the number has been linked.
func (s *Server) pairPoll(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	c, ok := s.clientFor(w, r, id)
	if !ok {
		return
	}
	s.pairPanel(w, c)
}

func (s *Server) pairCancel(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	c, ok := s.clientFor(w, r, id)
	if !ok {
		return
	}
	s.opts.Runtime.CancelPair(c.ID)
	s.render(w, http.StatusOK, "clients", "empty", nil)
}

func (s *Server) pairPanel(w http.ResponseWriter, c store.Client) {
	p, pairing := s.opts.Runtime.Pairing(c.ID)
	if !pairing {
		switch s.opts.Runtime.Status(c.ID) {
		case manager.StatusLoggedOut, manager.StatusPairing:
			s.render(w, http.StatusOK, "clients", "empty", nil)
		default: // linked: the table needs to show the new number
			w.Header().Set("HX-Refresh", "true")
			w.WriteHeader(http.StatusNoContent)
		}
		return
	}

	data := pairData{ClientID: c.ID, Name: c.Name}
	switch {
	case p.Err != nil:
		data.Failed = pairFailure(p.Err)
	case p.Code != "":
		svg, err := qrSVG(p.Code)
		if err != nil {
			s.fail(w, "render pairing code", err)
			return
		}
		data.QR = svg
	}
	s.render(w, http.StatusOK, "clients", "pair_panel", data)
}

func pairFailure(err error) string {
	if errors.Is(err, store.ErrJIDLinked) {
		return "That WhatsApp number is already linked to another client."
	}
	return "The code expired or pairing failed."
}

// qrSVG draws the code as one path of horizontal runs. The colors are attributes, not styles,
// so the page's content security policy allows them and the code reads the same in dark mode.
func qrSVG(code string) (template.HTML, error) {
	c, err := qr.Encode(code, qr.L)
	if err != nil {
		return "", fmt.Errorf("encode qr: %w", err)
	}

	size := c.Size + 2*qrQuietZone
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="qr" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %[1]d %[1]d" shape-rendering="crispEdges" role="img" aria-label="WhatsApp pairing code">`, size)
	fmt.Fprintf(&b, `<rect width="%[1]d" height="%[1]d" fill="#fff"/><path fill="#000" d="`, size)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if !c.Black(x, y) {
				continue
			}
			run := 1
			for x+run < c.Size && c.Black(x+run, y) {
				run++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x+qrQuietZone, y+qrQuietZone, run, run)
			x += run - 1
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String()), nil
}

type addData struct {
	Name  string
	Error string
}

func (s *Server) addForm(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	if id.User.Role != store.RoleSuperAdmin {
		s.notFound(w, r)
		return
	}
	s.render(w, http.StatusOK, "clients", "add_panel", addData{})
}

func (s *Server) addClient(w http.ResponseWriter, r *http.Request, id auth.Identity) {
	if id.User.Role != store.RoleSuperAdmin {
		s.notFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || utf8.RuneCountInString(name) > maxClientNameRunes {
		s.render(w, http.StatusUnprocessableEntity, "clients", "add_panel", addData{
			Name:  name,
			Error: fmt.Sprintf("Give the client a name of up to %d characters.", maxClientNameRunes),
		})
		return
	}
	if _, err := s.opts.Store.Clients.Create(r.Context(), name); err != nil {
		s.fail(w, "create client", err)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}
