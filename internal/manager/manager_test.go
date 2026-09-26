package manager

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/androsyz/wasabot/internal/bot"
	"github.com/androsyz/wasabot/internal/store"
	"github.com/androsyz/wasabot/internal/whatsapp"
)

const jid = "6281234567890:7@s.whatsapp.net"

type fakeDevice struct {
	jid string

	mu           sync.Mutex
	connected    bool
	connectErr   error
	logoutErr    error
	connects     int
	disconnects  int
	logouts      int
	onMessage    func(whatsapp.IncomingMessage)
	onConnected  func()
	onLoggedOut  func()
	onPaired     func(string)
	onQR         func(whatsapp.QREvent)
	autoConnects bool // Connect reports the connection as up right away
}

func (d *fakeDevice) Send(context.Context, string, string) (string, error) { return "id", nil }

func (d *fakeDevice) Connect(context.Context) error {
	d.mu.Lock()
	d.connects++
	err, auto, h := d.connectErr, d.autoConnects, d.onConnected
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if auto {
		d.setConnected(true)
		if h != nil {
			h()
		}
	}
	return nil
}

func (d *fakeDevice) Disconnect() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.disconnects++
	d.connected = false
}

func (d *fakeDevice) Connected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.connected
}

func (d *fakeDevice) setConnected(v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.connected = v
}

func (d *fakeDevice) Logout(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logouts++
	d.connected = false
	return d.logoutErr
}

func (d *fakeDevice) OnMessage(h func(whatsapp.IncomingMessage)) { d.onMessage = h }
func (d *fakeDevice) OnConnected(h func())                       { d.onConnected = h }
func (d *fakeDevice) OnLoggedOut(h func())                       { d.onLoggedOut = h }
func (d *fakeDevice) OnPaired(h func(string))                    { d.onPaired = h }
func (d *fakeDevice) OnQR(h func(whatsapp.QREvent))              { d.onQR = h }

func (d *fakeDevice) count(n *int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return *n
}

type fakeLinks struct {
	mu      sync.Mutex
	sess    map[int64]string
	linkErr error
}

func (l *fakeLinks) List(context.Context) ([]store.WhatsAppSession, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []store.WhatsAppSession
	for id, j := range l.sess {
		out = append(out, store.WhatsAppSession{ClientID: id, JID: j})
	}
	return out, nil
}

func (l *fakeLinks) Link(_ context.Context, id int64, j string) (store.WhatsAppSession, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.linkErr != nil {
		return store.WhatsAppSession{}, l.linkErr
	}
	l.sess[id] = j
	return store.WhatsAppSession{ClientID: id, JID: j}, nil
}

func (l *fakeLinks) Unlink(_ context.Context, id int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sess, id)
	return nil
}

func (l *fakeLinks) has(id int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.sess[id]
	return ok
}

type fakeBot struct {
	mu       sync.Mutex
	handled  []bot.Message
	pending  []bot.Message
	answered chan []bot.Message
}

func (b *fakeBot) Handle(_ context.Context, _ bot.Transport, _ int64, m bot.Message) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handled = append(b.handled, m)
}

func (b *fakeBot) Unanswered(context.Context, int64) ([]bot.Message, error) { return b.pending, nil }

func (b *fakeBot) Answer(_ context.Context, _ bot.Transport, _ int64, p []bot.Message) {
	b.answered <- p
}

type harness struct {
	m       *Manager
	links   *fakeLinks
	bot     *fakeBot
	mu      sync.Mutex
	devices []*fakeDevice
	// prepare configures each device the factory creates, before the manager sees it
	prepare func(*fakeDevice)
}

func newHarness(t *testing.T, linked ...int64) *harness {
	t.Helper()
	h := &harness{
		links: &fakeLinks{sess: map[int64]string{}},
		bot:   &fakeBot{answered: make(chan []bot.Message, 1)},
	}
	for _, id := range linked {
		h.links.sess[id] = jid
	}
	h.m = New(h.links, func(_ context.Context, j string) (Device, error) {
		d := &fakeDevice{jid: j}
		if h.prepare != nil {
			h.prepare(d)
		}
		h.mu.Lock()
		h.devices = append(h.devices, d)
		h.mu.Unlock()
		return d, nil
	}, h.bot, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(h.m.Close)
	if err := h.m.Load(context.Background()); err != nil {
		t.Fatalf("load: %v", err)
	}
	return h
}

func (h *harness) device(t *testing.T, i int) *fakeDevice {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if i >= len(h.devices) {
		t.Fatalf("want device #%d, only %d were created", i, len(h.devices))
	}
	return h.devices[i]
}

func (h *harness) deviceCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.devices)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStart_StatusFollowsTheConnection(t *testing.T) {
	h := newHarness(t, 1)

	if got := h.m.Status(1); got != StatusStopped {
		t.Fatalf("linked but not started: got %s, want stopped", got)
	}
	if got := h.m.Status(2); got != StatusLoggedOut {
		t.Fatalf("no number: got %s, want logged_out", got)
	}

	if err := h.m.Start(1); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := h.m.Status(1); got != StatusReconnecting {
		t.Fatalf("started, not yet connected: got %s, want reconnecting", got)
	}
	h.device(t, 0).setConnected(true)
	if got := h.m.Status(1); got != StatusConnected {
		t.Fatalf("got %s, want connected", got)
	}
}

func TestStart_IsIdempotentAndNeedsANumber(t *testing.T) {
	h := newHarness(t, 1)

	if err := h.m.Start(2); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("got %v, want ErrNotLinked", err)
	}
	if err := h.m.Start(1); err != nil {
		t.Fatal(err)
	}
	if err := h.m.Start(1); err != nil {
		t.Fatal(err)
	}
	if n := h.deviceCount(); n != 1 {
		t.Fatalf("starting twice made %d devices, want 1", n)
	}
}

func TestStart_ConnectFailureLeavesTheClientStopped(t *testing.T) {
	h := newHarness(t, 1)
	h.prepare = func(d *fakeDevice) { d.connectErr = errors.New("no network") }

	if err := h.m.Start(1); err == nil {
		t.Fatal("want the connect error")
	}
	if got := h.m.Status(1); got != StatusStopped {
		t.Fatalf("got %s, want stopped", got)
	}
	h.prepare = nil
	if err := h.m.Start(1); err != nil {
		t.Fatalf("a later start must work: %v", err)
	}
}

func TestStartAll_StartsEveryLinkedClientAndFeedsTheBot(t *testing.T) {
	h := newHarness(t, 1, 2)

	h.m.StartAll()

	if n := h.deviceCount(); n != 2 {
		t.Fatalf("got %d devices, want 2", n)
	}
	sent := time.Now()
	h.device(t, 0).onMessage(whatsapp.IncomingMessage{ID: "m1", Chat: "c", Sender: "s", Text: "hi", SentAt: sent})
	h.bot.mu.Lock()
	defer h.bot.mu.Unlock()
	if len(h.bot.handled) != 1 || h.bot.handled[0].Text != "hi" || !h.bot.handled[0].Timestamp.Equal(sent) {
		t.Fatalf("message did not reach the bot intact: %+v", h.bot.handled)
	}
}

func TestStop_DisconnectsButKeepsTheNumber(t *testing.T) {
	h := newHarness(t, 1)
	h.m.Start(1)
	dev := h.device(t, 0)

	h.m.Stop(1)

	if got := h.m.Status(1); got != StatusStopped {
		t.Fatalf("got %s, want stopped", got)
	}
	if dev.count(&dev.disconnects) != 1 {
		t.Fatal("want the device disconnected")
	}
	if !h.links.has(1) {
		t.Fatal("stopping must not unlink the number")
	}
	if err := h.m.Start(1); err != nil || h.deviceCount() != 2 {
		t.Fatalf("restart: err=%v devices=%d", err, h.deviceCount())
	}
	h.m.Stop(9) // an unknown client is not an error
}

func TestLogout_RunningClient(t *testing.T) {
	h := newHarness(t, 1)
	h.m.Start(1)
	dev := h.device(t, 0)

	if err := h.m.Logout(context.Background(), 1); err != nil {
		t.Fatalf("logout: %v", err)
	}

	if dev.count(&dev.logouts) != 1 {
		t.Fatal("want the device logged out")
	}
	if h.links.has(1) || h.m.Status(1) != StatusLoggedOut {
		t.Fatalf("the number must be unlinked; status %s", h.m.Status(1))
	}
	if h.deviceCount() != 1 {
		t.Fatal("a running client is logged out over its own connection")
	}
}

func TestLogout_StoppedClientConnectsFirst(t *testing.T) {
	h := newHarness(t, 1)
	h.prepare = func(d *fakeDevice) { d.autoConnects = true }
	if err := h.m.Logout(context.Background(), 1); err != nil {
		t.Fatalf("logout: %v", err)
	}

	dev := h.device(t, 0)
	if dev.count(&dev.connects) != 1 || dev.count(&dev.logouts) != 1 {
		t.Fatalf("want one connect then one logout, got %d and %d", dev.connects, dev.logouts)
	}
	if h.links.has(1) {
		t.Fatal("the number must be unlinked")
	}
}

func TestLogout_StillUnlinksWhenWhatsAppCannotBeReached(t *testing.T) {
	h := newHarness(t, 1)
	h.m.Start(1)
	h.device(t, 0).logoutErr = errors.New("offline")

	if err := h.m.Logout(context.Background(), 1); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if h.links.has(1) || h.m.Status(1) != StatusLoggedOut {
		t.Fatal("the number must be unlinked locally")
	}
}

func TestLogout_WithoutANumber(t *testing.T) {
	h := newHarness(t)

	if err := h.m.Logout(context.Background(), 1); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("got %v, want ErrNotLinked", err)
	}
}

func TestLoggedOutFromThePhone_UnlinksTheClient(t *testing.T) {
	h := newHarness(t, 1)
	h.m.Start(1)

	h.device(t, 0).onLoggedOut()

	eventually(t, "the client to be unlinked", func() bool { return h.m.Status(1) == StatusLoggedOut })
	if h.links.has(1) {
		t.Fatal("the number must be unlinked")
	}
}

func TestStart_AnswersWaitingMessagesOnceConnected(t *testing.T) {
	h := newHarness(t, 1)
	h.bot.pending = []bot.Message{{ID: "old", Text: "hello?"}}
	h.m.Start(1)
	dev := h.device(t, 0)

	select {
	case <-h.bot.answered:
		t.Fatal("must wait for the connection")
	default:
	}
	dev.onConnected()
	dev.onConnected() // a reconnect must not answer twice

	select {
	case got := <-h.bot.answered:
		if len(got) != 1 || got[0].ID != "old" {
			t.Fatalf("got %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting message was not answered")
	}
	select {
	case <-h.bot.answered:
		t.Fatal("answered twice")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPair_ShowsCodesThenLinksAndStarts(t *testing.T) {
	h := newHarness(t)

	if err := h.m.Pair(1); err != nil {
		t.Fatalf("pair: %v", err)
	}
	if got := h.m.Status(1); got != StatusPairing {
		t.Fatalf("got %s, want pairing", got)
	}
	if p, ok := h.m.Pairing(1); !ok || p.Code != "" || p.Err != nil {
		t.Fatalf("before the first code: %+v %v", p, ok)
	}

	dev := h.device(t, 0)
	dev.onQR(whatsapp.QREvent{Code: "code-1"})
	dev.onQR(whatsapp.QREvent{Code: "code-2"})
	if p, _ := h.m.Pairing(1); p.Code != "code-2" {
		t.Fatalf("got %q, want the latest code", p.Code)
	}

	dev.onPaired(jid)

	eventually(t, "the client to be started with its number", func() bool { return h.deviceCount() == 2 })
	if got := h.device(t, 1).jid; got != jid {
		t.Fatalf("started with %q, want the paired number", got)
	}
	if !h.links.has(1) {
		t.Fatal("the number must be linked")
	}
	if _, ok := h.m.Pairing(1); ok {
		t.Fatal("a finished pairing is forgotten")
	}
	if got := h.m.Status(1); got != StatusReconnecting {
		t.Fatalf("got %s, want the new connection coming up", got)
	}
}

func TestPair_EndingWithoutASuccessCanBeRetried(t *testing.T) {
	h := newHarness(t)
	h.m.Pair(1)
	h.device(t, 0).onQR(whatsapp.QREvent{Code: "code-1"})

	h.device(t, 0).onQR(whatsapp.QREvent{Err: errors.New("timeout")})

	if p, ok := h.m.Pairing(1); !ok || p.Err == nil || p.Code != "" {
		t.Fatalf("want a failed attempt without a code, got %+v %v", p, ok)
	}
	if got := h.m.Status(1); got != StatusLoggedOut {
		t.Fatalf("got %s, want logged_out", got)
	}
	if err := h.m.Pair(1); err != nil {
		t.Fatal(err)
	}
	if p, _ := h.m.Pairing(1); p.Err != nil || h.deviceCount() != 2 {
		t.Fatalf("a retry starts a new attempt: %+v, %d devices", p, h.deviceCount())
	}
}

func TestPair_WhileInProgressDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.m.Pair(1)
	h.m.Pair(1)

	if n := h.deviceCount(); n != 1 {
		t.Fatalf("got %d devices, want 1", n)
	}
}

func TestPair_ClientWithANumber(t *testing.T) {
	h := newHarness(t, 1)

	if err := h.m.Pair(1); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("got %v, want ErrAlreadyLinked", err)
	}
}

func TestPair_CancelStopsTheAttempt(t *testing.T) {
	h := newHarness(t)
	h.m.Pair(1)
	dev := h.device(t, 0)

	h.m.CancelPair(1)

	if _, ok := h.m.Pairing(1); ok || h.m.Status(1) != StatusLoggedOut {
		t.Fatal("the attempt must be gone")
	}
	if dev.count(&dev.disconnects) == 0 {
		t.Fatal("want the device disconnected")
	}
	dev.onPaired(jid) // a scan that races the cancel is ignored
	time.Sleep(20 * time.Millisecond)
	if h.links.has(1) {
		t.Fatal("a cancelled pairing must not link")
	}
}

func TestPair_NumberLinkedElsewhereIsRejected(t *testing.T) {
	h := newHarness(t)
	h.links.linkErr = store.ErrJIDLinked
	h.m.Pair(1)
	dev := h.device(t, 0)

	dev.onPaired(jid)

	eventually(t, "the attempt to fail", func() bool {
		p, _ := h.m.Pairing(1)
		return errors.Is(p.Err, store.ErrJIDLinked)
	})
	if dev.count(&dev.logouts) != 1 {
		t.Fatal("the rejected number must be unlinked from the account again")
	}
	if h.m.Status(1) != StatusLoggedOut || h.links.has(1) {
		t.Fatal("the client must stay without a number")
	}
}

func TestClose_DisconnectsEverything(t *testing.T) {
	h := newHarness(t, 1)
	h.m.Start(1)
	h.m.Pair(2)

	h.m.Close()

	for i := range 2 {
		d := h.device(t, i)
		if d.count(&d.disconnects) == 0 {
			t.Fatalf("device #%d was left connected", i)
		}
	}
}
