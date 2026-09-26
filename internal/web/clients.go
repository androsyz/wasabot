package web

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/androsyz/wasabot/internal/auth"
	"github.com/androsyz/wasabot/internal/store"
)

type navItem struct {
	Label  string
	Icon   string
	Href   string // empty: not built yet
	Active bool
}

func navItems(active string) []navItem {
	return []navItem{
		{Label: "Clients Overview", Icon: "icon-clients", Href: "/clients", Active: active == "clients"},
		{Label: "Agents & Skills", Icon: "icon-agents"},
		{Label: "Conversations", Icon: "icon-conversations"},
		{Label: "Playground", Icon: "icon-playground"},
		{Label: "Analytics", Icon: "icon-analytics"},
	}
}

type barData struct {
	Connected, Stopped, Connecting int
}

type pickerClient struct {
	ID       int64
	Name     string
	Selected bool
}

type selectedClient struct {
	ID     int64
	Status ClientStatus
}

type headerData struct {
	Clients  []pickerClient
	Selected *selectedClient
}

type filterOption struct {
	Value, Label string
	Selected     bool
}

// action is a control in a client's row. Path is what it posts to; Panel actions fill the panel
// above the table instead of reloading the page. Without a Path the control is not built yet.
type action struct {
	Label   string
	Button  string // "", "green" or "blue": a button instead of a text link
	Open    bool
	Path    string
	Panel   bool
	Confirm string // asked before the request is sent
}

type avatar struct {
	Sprite string
	Tone   int
}

type clientRow struct {
	ID      int64
	Name    string
	Avatar  avatar
	Status  ClientStatus
	Phone   string
	Agent   string
	Today   int64
	Month   int64
	Actions []action
}

type clientsBody struct {
	Rows     []clientRow
	Query    string
	Filters  []filterOption
	Filtered bool
	CSRF     string
	CanAdd   bool
	Notice   string
}

// page is the data every app page gets: the shell (status bar, navigation, header) plus a body.
type page struct {
	Title  string
	CSRF   string // for the logout form and htmx requests
	User   string // who is logged in
	Nav    []navItem
	Bar    barData
	Header headerData
	Body   any
}

var avatars = []avatar{
	{"avatar-cup", 0}, {"avatar-tooth", 1}, {"avatar-washer", 2},
	{"avatar-key", 3}, {"avatar-shop", 4}, {"avatar-bot", 5},
}

var statusFilters = []filterOption{
	{Value: "all", Label: "All Statuses"},
	{Value: string(StatusConnected), Label: "Connected"},
	{Value: string(StatusPairing), Label: "Pairing"},
	{Value: string(StatusReconnecting), Label: "Reconnecting"},
	{Value: string(StatusStopped), Label: "Stopped"},
	{Value: string(StatusLoggedOut), Label: "Logged out"},
}

// clientsPage builds the dashboard for who is asking: a super admin sees every client,
// anyone else only the clients they belong to.
func (s *Server) clientsPage(ctx context.Context, id auth.Identity, query, statusFilter, selected, notice string) (page, error) {
	var (
		clients []store.Client
		err     error
	)
	if id.User.Role == store.RoleSuperAdmin {
		clients, err = s.opts.Store.Clients.List(ctx)
	} else {
		clients, err = s.opts.Store.Clients.ListByUser(ctx, id.User.ID)
	}
	if err != nil {
		return page{}, err
	}
	slices.SortFunc(clients, func(a, b store.Client) int { return cmp.Compare(a.ID, b.ID) })
	sessions, err := s.opts.Store.WhatsAppSessions.List(ctx)
	if err != nil {
		return page{}, err
	}
	now := s.opts.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	todayCounts, err := s.opts.Store.Messages.CountSince(ctx, today)
	if err != nil {
		return page{}, err
	}
	monthCounts, err := s.opts.Store.Messages.CountSince(ctx, month)
	if err != nil {
		return page{}, err
	}

	jids := make(map[int64]string, len(sessions))
	for _, sess := range sessions {
		jids[sess.ClientID] = sess.JID
	}

	var (
		bar     barData
		rows    []clientRow
		picker  []pickerClient
		chosen  *selectedClient
		first   *selectedClient
		wantID  = parseID(selected)
		needle  = strings.ToLower(query)
		filters = make([]filterOption, len(statusFilters))
	)
	copy(filters, statusFilters)
	for i := range filters {
		filters[i].Selected = filters[i].Value == statusFilter
	}

	for _, c := range clients {
		status := s.status(c.ID, jids[c.ID] != "")
		switch status {
		case StatusConnected:
			bar.Connected++
		case StatusPairing, StatusReconnecting:
			bar.Connecting++
		default:
			bar.Stopped++
		}

		if first == nil {
			first = &selectedClient{ID: c.ID, Status: status}
		}
		if wantID == c.ID {
			chosen = &selectedClient{ID: c.ID, Status: status}
		}
		picker = append(picker, pickerClient{ID: c.ID, Name: c.Name})

		if needle != "" && !strings.Contains(strings.ToLower(c.Name), needle) {
			continue
		}
		if statusFilter != "all" && string(status) != statusFilter {
			continue
		}
		rows = append(rows, clientRow{
			ID:      c.ID,
			Name:    c.Name,
			Avatar:  avatars[int((c.ID-1)%int64(len(avatars))+int64(len(avatars)))%len(avatars)],
			Status:  status,
			Phone:   maskPhone(jids[c.ID]),
			Agent:   s.opts.Agent,
			Today:   todayCounts[c.ID],
			Month:   monthCounts[c.ID],
			Actions: actionsFor(c.ID, status),
		})
	}
	if chosen == nil {
		chosen = first // no request, or one for a client this user cannot see
	}
	for i := range picker {
		picker[i].Selected = chosen != nil && picker[i].ID == chosen.ID
	}

	return page{
		Title:  "Clients Overview",
		CSRF:   id.CSRFToken,
		User:   id.User.Name,
		Nav:    navItems("clients"),
		Bar:    bar,
		Header: headerData{Clients: picker, Selected: chosen},
		Body: clientsBody{
			Rows:     rows,
			Query:    query,
			Filters:  filters,
			Filtered: needle != "" || statusFilter != "all",
			CSRF:     id.CSRFToken,
			CanAdd:   id.User.Role == store.RoleSuperAdmin,
			Notice:   notices[notice],
		},
	}, nil
}

func (s *Server) status(clientID int64, linked bool) ClientStatus {
	if s.opts.Runtime != nil {
		return ClientStatus(s.opts.Runtime.Status(clientID))
	}
	if linked {
		return StatusStopped
	}
	return StatusLoggedOut
}

func actionsFor(clientID int64, s ClientStatus) []action {
	path := func(verb string) string { return fmt.Sprintf("/clients/%d/%s", clientID, verb) }
	stop := action{Label: "Stop", Path: path("stop")}
	start := action{Label: "Start", Button: "green", Path: path("start")}
	logout := action{Label: "Logout", Path: path("logout"), Confirm: "Unlink this WhatsApp number? It will have to be paired again."}
	qr := action{Label: "Show QR", Button: "blue", Path: path("pair"), Panel: true}
	open := action{Label: "Open", Open: true}

	switch s {
	case StatusConnected, StatusReconnecting:
		return []action{stop, logout, open}
	case StatusPairing:
		return []action{qr, open}
	case StatusStopped:
		return []action{start, logout, open}
	default:
		return []action{qr, open}
	}
}

func parseID(s string) int64 {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

// maskPhone turns a WhatsApp JID such as 6281234567890:7@s.whatsapp.net into "+62 812 xxxx xxxx".
func maskPhone(jid string) string {
	user, _, _ := strings.Cut(jid, "@")
	user, _, _ = strings.Cut(user, ":")
	if len(user) < 6 {
		return ""
	}
	for _, r := range user {
		if r < '0' || r > '9' {
			return ""
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "+%s %s", user[:2], user[2:5])
	for rest := len(user) - 5; rest > 0; rest -= 4 {
		b.WriteString(" ")
		b.WriteString(strings.Repeat("x", min(4, rest)))
	}
	return b.String()
}
