// Package manager owns the live WhatsApp connection of every client: it starts, stops, pairs and
// unlinks them on request, and reports what each one is doing.
package manager

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/androsyz/wasabot/internal/bot"
	"github.com/androsyz/wasabot/internal/store"
	"github.com/androsyz/wasabot/internal/whatsapp"
)

const (
	// pairTimeout ends a pairing nobody finishes, even if WhatsApp never reports it.
	pairTimeout = 3 * time.Minute
	// logoutConnectTimeout is how long a stopped client gets to reconnect so it can be unlinked.
	logoutConnectTimeout = 20 * time.Second
)

var (
	ErrNotLinked     = errors.New("manager: the client has no WhatsApp number")
	ErrAlreadyLinked = errors.New("manager: the client already has a WhatsApp number")
)

type Status string

const (
	StatusConnected    Status = "connected"
	StatusPairing      Status = "pairing"
	StatusReconnecting Status = "reconnecting"
	StatusStopped      Status = "stopped"
	StatusLoggedOut    Status = "logged_out"
)

// Pairing is the state of a client's pairing attempt.
type Pairing struct {
	Code string // to show as a QR code; empty until WhatsApp issues the first one
	Err  error  // set when the attempt ended without a linked number
}

// Device is one WhatsApp connection; *whatsapp.Client is the real one.
type Device interface {
	bot.Transport
	Connect(ctx context.Context) error
	Disconnect()
	Connected() bool
	Logout(ctx context.Context) error
	OnMessage(h func(whatsapp.IncomingMessage))
	OnConnected(h func())
	OnLoggedOut(h func())
	OnPaired(h func(jid string))
	OnQR(h func(whatsapp.QREvent))
}

// NewDevice returns the device for jid, or a new unpaired one when jid is empty.
type NewDevice func(ctx context.Context, jid string) (Device, error)

type Links interface {
	List(ctx context.Context) ([]store.WhatsAppSession, error)
	Link(ctx context.Context, clientID int64, jid string) (store.WhatsAppSession, error)
	Unlink(ctx context.Context, clientID int64) error
}

type Bot interface {
	Handle(ctx context.Context, t bot.Transport, clientID int64, msg bot.Message)
	Unanswered(ctx context.Context, clientID int64) ([]bot.Message, error)
	Answer(ctx context.Context, t bot.Transport, clientID int64, pending []bot.Message)
}

type Manager struct {
	links     Links
	newDevice NewDevice
	bot       Bot
	log       *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	linked   map[int64]string // client ID to JID
	running  map[int64]Device
	pairings map[int64]*pairing
}

type pairing struct {
	dev    Device
	cancel context.CancelFunc
	state  Pairing
}

func New(links Links, newDevice NewDevice, b Bot, log *slog.Logger) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		links:     links,
		newDevice: newDevice,
		bot:       b,
		log:       log,
		ctx:       ctx,
		cancel:    cancel,
		linked:    make(map[int64]string),
		running:   make(map[int64]Device),
		pairings:  make(map[int64]*pairing),
	}
}

// Load reads which clients have a WhatsApp number.
func (m *Manager) Load(ctx context.Context) error {
	sessions, err := m.links.List(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range sessions {
		m.linked[s.ClientID] = s.JID
	}
	return nil
}

// StartAll starts every client that has a WhatsApp number. One that fails is logged and skipped.
func (m *Manager) StartAll() {
	m.mu.Lock()
	ids := make([]int64, 0, len(m.linked))
	for id := range m.linked {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		if err := m.Start(id); err != nil {
			m.log.Error("start whatsapp", "client_id", id, "error", err)
		}
	}
}

func (m *Manager) Status(clientID int64) Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	dev, running := m.running[clientID]
	switch p := m.pairings[clientID]; {
	case p != nil && p.state.Err == nil:
		return StatusPairing
	case running && dev.Connected():
		return StatusConnected
	case running:
		return StatusReconnecting
	case m.linked[clientID] != "":
		return StatusStopped
	default:
		return StatusLoggedOut
	}
}

// Start connects the client's WhatsApp number. It does nothing if it is already running.
func (m *Manager) Start(clientID int64) error {
	m.mu.Lock()
	jid, ok := m.linked[clientID]
	if !ok {
		m.mu.Unlock()
		return ErrNotLinked
	}
	if _, running := m.running[clientID]; running {
		m.mu.Unlock()
		return nil
	}
	dev, err := m.newDevice(m.ctx, jid)
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("create whatsapp client: %w", err)
	}
	m.wire(clientID, dev)
	m.running[clientID] = dev
	m.mu.Unlock()

	if err := dev.Connect(m.ctx); err != nil {
		m.forget(clientID, dev)
		return err
	}
	return nil
}

// wire connects a device to the bot. It runs before Connect, so no event is missed.
func (m *Manager) wire(clientID int64, dev Device) {
	log := m.log.With("client_id", clientID)

	dev.OnMessage(func(msg whatsapp.IncomingMessage) {
		m.bot.Handle(m.ctx, dev, clientID, bot.Message{ID: msg.ID, Chat: msg.Chat, Sender: msg.Sender, Text: msg.Text, Timestamp: msg.SentAt})
	})
	dev.OnLoggedOut(func() {
		log.Info("whatsapp number was unlinked from the phone")
		m.spawn(func() { m.unlinked(clientID, dev) })
	})

	// messages that were waiting when the process last stopped are answered once the connection is ready
	pending, err := m.bot.Unanswered(m.ctx, clientID)
	if err != nil {
		log.Error("list unanswered messages", "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	var once sync.Once
	dev.OnConnected(func() {
		once.Do(func() { m.spawn(func() { m.bot.Answer(m.ctx, dev, clientID, pending) }) })
	})
}

// forget drops dev from the running set if it is still the one there, and disconnects it.
func (m *Manager) forget(clientID int64, dev Device) {
	m.mu.Lock()
	if m.running[clientID] == dev {
		delete(m.running, clientID)
	}
	m.mu.Unlock()
	dev.Disconnect()
}

// Stop disconnects the client. Its number stays linked, so Start brings it back.
func (m *Manager) Stop(clientID int64) {
	m.mu.Lock()
	dev := m.running[clientID]
	delete(m.running, clientID)
	m.mu.Unlock()

	if dev != nil {
		dev.Disconnect()
	}
}

// Logout unlinks the WhatsApp number from the account and from the client. When WhatsApp cannot
// be reached the number is still unlinked here, and stays listed under Linked devices on the phone.
func (m *Manager) Logout(ctx context.Context, clientID int64) error {
	m.mu.Lock()
	jid, ok := m.linked[clientID]
	dev := m.running[clientID]
	delete(m.running, clientID)
	m.mu.Unlock()
	if !ok {
		return ErrNotLinked
	}

	if dev == nil {
		var err error
		if dev, err = m.connectForLogout(ctx, jid); err != nil {
			return fmt.Errorf("create whatsapp client: %w", err)
		}
	}
	if err := dev.Logout(ctx); err != nil {
		m.log.Warn("whatsapp did not confirm the logout", "client_id", clientID, "error", err)
	}

	if err := m.links.Unlink(ctx, clientID); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.linked, clientID)
	m.mu.Unlock()
	return nil
}

// connectForLogout connects a stopped client, since WhatsApp only accepts a logout over a live
// connection. If it does not come up in time the caller still logs out, which then unlinks locally.
func (m *Manager) connectForLogout(ctx context.Context, jid string) (Device, error) {
	dev, err := m.newDevice(m.ctx, jid)
	if err != nil {
		return nil, err
	}
	up := make(chan struct{})
	var once sync.Once
	dev.OnConnected(func() { once.Do(func() { close(up) }) })

	if err := dev.Connect(m.ctx); err != nil {
		return dev, nil
	}
	select {
	case <-up:
	case <-time.After(logoutConnectTimeout):
	case <-ctx.Done():
	}
	return dev, nil
}

// unlinked handles the phone removing the device: nothing is left to connect.
func (m *Manager) unlinked(clientID int64, dev Device) {
	m.forget(clientID, dev)
	if err := m.links.Unlink(m.ctx, clientID); err != nil {
		m.log.Error("unlink whatsapp session", "client_id", clientID, "error", err)
	}
	m.mu.Lock()
	delete(m.linked, clientID)
	m.mu.Unlock()
}

// Pair starts linking a WhatsApp number; Pairing reports the codes to scan. Calling it while an
// attempt is in progress does nothing, and after a failed one it starts over.
func (m *Manager) Pair(clientID int64) error {
	m.mu.Lock()
	if _, ok := m.linked[clientID]; ok {
		m.mu.Unlock()
		return ErrAlreadyLinked
	}
	if p := m.pairings[clientID]; p != nil && p.state.Err == nil {
		m.mu.Unlock()
		return nil
	}
	dev, err := m.newDevice(m.ctx, "")
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("create whatsapp client: %w", err)
	}
	ctx, cancel := context.WithTimeout(m.ctx, pairTimeout)
	p := &pairing{dev: dev, cancel: cancel}
	dev.OnQR(func(e whatsapp.QREvent) { m.qr(clientID, p, e) })
	dev.OnPaired(func(jid string) { m.spawn(func() { m.paired(clientID, p, jid) }) })
	context.AfterFunc(ctx, func() { m.qr(clientID, p, whatsapp.QREvent{Err: errors.New("pairing timed out")}) })
	old := m.pairings[clientID]
	m.pairings[clientID] = p
	m.mu.Unlock()

	if old != nil {
		old.cancel()
		old.dev.Disconnect()
	}
	if err := dev.Connect(ctx); err != nil {
		m.CancelPair(clientID)
		return err
	}
	return nil
}

// qr records a code, or the end of the attempt, unless p has been replaced or cancelled.
func (m *Manager) qr(clientID int64, p *pairing, e whatsapp.QREvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pairings[clientID] != p || p.state.Err != nil {
		return
	}
	if e.Err != nil {
		p.state = Pairing{Err: e.Err}
		return
	}
	p.state.Code = e.Code
}

// paired links the number that was just scanned and starts the client with it.
func (m *Manager) paired(clientID int64, p *pairing, jid string) {
	m.mu.Lock()
	current := m.pairings[clientID] == p
	m.mu.Unlock()
	if !current {
		return
	}

	if _, err := m.links.Link(m.ctx, clientID, jid); err != nil {
		m.log.Error("link whatsapp session", "client_id", clientID, "error", err)
		m.qr(clientID, p, whatsapp.QREvent{Err: err})
		// the number would otherwise stay linked on the phone with nothing here to use it
		if err := p.dev.Logout(m.ctx); err != nil {
			m.log.Warn("unlink rejected whatsapp number", "client_id", clientID, "error", err)
		}
		return
	}

	m.mu.Lock()
	delete(m.pairings, clientID)
	m.linked[clientID] = jid
	m.mu.Unlock()
	p.cancel()
	p.dev.Disconnect()

	if err := m.Start(clientID); err != nil {
		m.log.Error("start whatsapp after pairing", "client_id", clientID, "error", err)
	}
}

// Pairing reports the client's pairing attempt, if there is one.
func (m *Manager) Pairing(clientID int64) (Pairing, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.pairings[clientID]
	if p == nil {
		return Pairing{}, false
	}
	return p.state, true
}

// CancelPair ends the client's pairing attempt and forgets it, failed or not.
func (m *Manager) CancelPair(clientID int64) {
	m.mu.Lock()
	p := m.pairings[clientID]
	delete(m.pairings, clientID)
	m.mu.Unlock()

	if p != nil {
		p.cancel()
		p.dev.Disconnect()
	}
}

// Close disconnects everything and waits for the work it started.
func (m *Manager) Close() {
	m.cancel()

	m.mu.Lock()
	var devs []Device
	for _, d := range m.running {
		devs = append(devs, d)
	}
	for _, p := range m.pairings {
		devs = append(devs, p.dev)
	}
	m.running = make(map[int64]Device)
	m.pairings = make(map[int64]*pairing)
	m.mu.Unlock()

	for _, d := range devs {
		d.Disconnect()
	}
	m.wg.Wait()
}

func (m *Manager) spawn(f func()) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		f()
	}()
}
