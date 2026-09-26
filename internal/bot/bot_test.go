package bot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/androsyz/wasabot/internal/db/dbtest"
	"github.com/androsyz/wasabot/internal/store"
)

var t0 = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

type sent struct{ chat, text string }

type fakeTransport struct {
	sent chan sent
	n    atomic.Int64
	err  error
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{sent: make(chan sent, 100)}
}

func (f *fakeTransport) Send(_ context.Context, chat, text string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.sent <- sent{chat: chat, text: text}
	return fmt.Sprintf("OUT%d", f.n.Add(1)), nil
}

func (f *fakeTransport) next(t *testing.T) sent {
	t.Helper()
	select {
	case s := <-f.sent:
		return s
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a reply")
		return sent{}
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type env struct {
	bot      *Bot
	st       *store.Store
	sqlDB    *sql.DB
	clientID int64
	tr       *fakeTransport
	clock    *fakeClock
	cancel   context.CancelFunc
}

func newEnv(t *testing.T, respond Responder, mods ...func(*Options)) *env {
	t.Helper()
	sqlDB := dbtest.New(t)
	st := store.New(sqlDB)
	c, err := st.Clients.Create(context.Background(), "Acme")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	clock := &fakeClock{now: t0}
	opts := Options{Workers: 3, Now: clock.Now}
	for _, mod := range mods {
		mod(&opts)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(st.Messages, log, respond, opts)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)
	t.Cleanup(func() {
		cancel()
		b.Wait()
	})
	return &env{bot: b, st: st, sqlDB: sqlDB, clientID: c.ID, tr: newFakeTransport(), clock: clock, cancel: cancel}
}

func (e *env) handle(id, chat, text string) {
	e.bot.Handle(context.Background(), e.tr, e.clientID, Message{ID: id, Chat: chat, Sender: chat, Text: text})
}

func (e *env) countMessages(t *testing.T, direction store.Direction) int {
	t.Helper()
	var n int
	err := e.sqlDB.QueryRow(`SELECT COUNT(*) FROM messages WHERE direction = ?`, direction).Scan(&n)
	if err != nil {
		t.Fatalf("count messages: %v", err)
	}
	return n
}

// the outgoing message is stored after Send returns, so poll briefly
func (e *env) waitForMessages(t *testing.T, direction store.Direction, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for e.countMessages(t, direction) != want {
		if time.Now().After(deadline) {
			t.Fatalf("want %d %q messages, got %d", want, direction, e.countMessages(t, direction))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func echo(_ context.Context, _ int64, msg Message) (string, error) {
	return "re: " + msg.Text, nil
}

func TestBot_StoresIncomingAndReplies(t *testing.T) {
	e := newEnv(t, echo)

	e.handle("IN1", "628@s.whatsapp.net", "halo")

	got := e.tr.next(t)
	if got.chat != "628@s.whatsapp.net" || got.text != "re: halo" {
		t.Fatalf("got %+v", got)
	}
	e.waitForMessages(t, store.DirectionIn, 1)
	e.waitForMessages(t, store.DirectionOut, 1)
}

func TestBot_IgnoresDuplicateDelivery(t *testing.T) {
	var calls atomic.Int64
	e := newEnv(t, func(ctx context.Context, id int64, msg Message) (string, error) {
		calls.Add(1)
		return echo(ctx, id, msg)
	})

	e.handle("IN1", "628@s.whatsapp.net", "halo")
	e.tr.next(t)
	e.handle("IN1", "628@s.whatsapp.net", "halo") // redelivered by WhatsApp
	e.handle("IN2", "628@s.whatsapp.net", "lagi") // same chat, so it queues behind a duplicate if one slipped through

	if got := e.tr.next(t); got.text != "re: lagi" {
		t.Fatalf("second reply should be for IN2, got %+v", got)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("responder called %d times, want 2", n)
	}
	e.waitForMessages(t, store.DirectionIn, 2)
}

func TestBot_KeepsOrderWithinAChat(t *testing.T) {
	e := newEnv(t, echo)

	for i := range 10 {
		e.handle(fmt.Sprintf("IN%d", i), "628@s.whatsapp.net", fmt.Sprint(i))
	}

	for i := range 10 {
		if got := e.tr.next(t); got.text != fmt.Sprintf("re: %d", i) {
			t.Fatalf("reply %d = %q, out of order", i, got.text)
		}
	}
}

func TestBot_ChatsAreIndependent(t *testing.T) {
	block := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, id int64, msg Message) (string, error) {
		if msg.Chat == "slow@s.whatsapp.net" {
			<-block
		}
		return echo(ctx, id, msg)
	})
	t.Cleanup(func() { close(block) })

	// find a fast chat that lands on a different worker than the slow one
	slow := e.bot.queueFor(e.clientID, "slow@s.whatsapp.net")
	fast := ""
	for i := range 100 {
		chat := fmt.Sprintf("fast%d@s.whatsapp.net", i)
		if e.bot.queueFor(e.clientID, chat) != slow {
			fast = chat
			break
		}
	}
	if fast == "" {
		t.Fatal("no chat maps to a different worker")
	}

	e.handle("S1", "slow@s.whatsapp.net", "slow")
	e.handle("F1", fast, "fast")

	if got := e.tr.next(t); got.chat != fast {
		t.Fatalf("fast chat should not wait for the slow one, got %+v", got)
	}
}

func TestBot_ResponderErrorSendsNothingAndKeepsWorking(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, id int64, msg Message) (string, error) {
		if msg.Text == "boom" {
			return "", errors.New("llm down")
		}
		return echo(ctx, id, msg)
	})

	e.handle("IN1", "628@s.whatsapp.net", "boom")
	e.handle("IN2", "628@s.whatsapp.net", "ok")

	if got := e.tr.next(t); got.text != "re: ok" {
		t.Fatalf("want the next message to be answered, got %+v", got)
	}
	e.waitForMessages(t, store.DirectionIn, 2)
	e.waitForMessages(t, store.DirectionOut, 1)
}

func TestBot_EmptyReplyStaysSilent(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	e := newEnv(t, func(_ context.Context, _ int64, msg Message) (string, error) {
		mu.Lock()
		seen = append(seen, msg.Text)
		mu.Unlock()
		if msg.Text == "quiet" {
			return "", nil
		}
		return "loud", nil
	})

	e.handle("IN1", "628@s.whatsapp.net", "quiet")
	e.handle("IN2", "628@s.whatsapp.net", "talk")

	if got := e.tr.next(t); got.text != "loud" {
		t.Fatalf("got %+v", got)
	}
	e.waitForMessages(t, store.DirectionOut, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("responder saw %v, want both messages", seen)
	}
}

func TestBot_SendFailureStoresNoOutgoingMessage(t *testing.T) {
	e := newEnv(t, echo)
	e.tr.err = errors.New("offline")

	e.handle("IN1", "628@s.whatsapp.net", "halo")
	e.waitForMessages(t, store.DirectionIn, 1)

	// give the worker time to attempt the send and fail
	time.Sleep(100 * time.Millisecond)
	if n := e.countMessages(t, store.DirectionOut); n != 0 {
		t.Fatalf("want no outgoing message after a failed send, got %d", n)
	}
}

func (e *env) handleAt(id, chat, text string, sentAt time.Time) {
	e.bot.Handle(context.Background(), e.tr, e.clientID, Message{ID: id, Chat: chat, Sender: chat, Text: text, Timestamp: sentAt})
}

// expectNoReply gives the workers time to (wrongly) reply, then checks nothing was sent.
func (e *env) expectNoReply(t *testing.T) {
	t.Helper()
	select {
	case s := <-e.tr.sent:
		t.Fatalf("unexpected reply: %+v", s)
	case <-time.After(150 * time.Millisecond):
	}
}

func failing(context.Context, int64, Message) (string, error) {
	return "", errors.New("llm down")
}

func TestBot_FallbackReplyWhenTheResponderFails(t *testing.T) {
	e := newEnv(t, failing, func(o *Options) { o.Fallback = "sorry, try again" })

	e.handle("IN1", "628@s.whatsapp.net", "halo")

	if got := e.tr.next(t); got.text != "sorry, try again" || got.chat != "628@s.whatsapp.net" {
		t.Fatalf("got %+v, want the fallback sent to the chat", got)
	}
	e.waitForMessages(t, store.DirectionOut, 1)
}

func TestBot_NoFallbackWhenShuttingDown(t *testing.T) {
	var e *env
	e = newEnv(t, func(context.Context, int64, Message) (string, error) {
		e.cancel() // shutdown begins while the reply is being produced
		return "", context.Canceled
	}, func(o *Options) { o.Fallback = "sorry, try again" })

	e.handle("IN1", "628@s.whatsapp.net", "halo")

	e.expectNoReply(t)
	if n := e.countMessages(t, store.DirectionOut); n != 0 {
		t.Fatalf("want no outgoing message during shutdown, got %d", n)
	}
}

func TestBot_RateLimitsPerChat(t *testing.T) {
	e := newEnv(t, echo, func(o *Options) { o.RateLimit = 2 })
	const chat = "628@s.whatsapp.net"

	e.handle("IN1", chat, "one")
	e.handle("IN1", chat, "one") // redelivered: must not use up the quota
	e.handle("IN2", chat, "two")
	e.tr.next(t)
	e.tr.next(t)

	e.handle("IN3", chat, "three")
	e.expectNoReply(t)
	e.waitForMessages(t, store.DirectionIn, 3)

	e.handle("OTHER1", "999@s.whatsapp.net", "hi")
	if got := e.tr.next(t); got.chat != "999@s.whatsapp.net" {
		t.Fatalf("another chat has its own quota, got %+v", got)
	}

	e.clock.Advance(61 * time.Second)
	e.handle("IN4", chat, "four")
	if got := e.tr.next(t); got.text != "re: four" {
		t.Fatalf("the chat is free again after the window, got %+v", got)
	}
	e.waitForMessages(t, store.DirectionIn, 5)
	e.waitForMessages(t, store.DirectionOut, 4)
}

func TestBot_OldMessagesAreStoredButNotAnswered(t *testing.T) {
	e := newEnv(t, echo, func(o *Options) { o.MaxAge = 10 * time.Minute })
	const chat = "628@s.whatsapp.net"
	now := e.clock.Now()

	e.handleAt("OLD", chat, "sent while offline", now.Add(-11*time.Minute))
	e.expectNoReply(t)
	e.waitForMessages(t, store.DirectionIn, 1)

	var createdAt int64
	if err := e.sqlDB.QueryRow(`SELECT created_at FROM messages WHERE wa_id = 'OLD'`).Scan(&createdAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if want := now.Add(-11 * time.Minute).Unix(); createdAt != want {
		t.Errorf("created_at = %d, want the time the customer sent it (%d)", createdAt, want)
	}

	e.handleAt("RECENT", chat, "fresh", now.Add(-5*time.Minute))
	if got := e.tr.next(t); got.text != "re: fresh" {
		t.Fatalf("got %+v", got)
	}

	e.handleAt("NO-TIME", chat, "unknown send time", time.Time{})
	if got := e.tr.next(t); got.text != "re: unknown send time" {
		t.Fatalf("a message without a timestamp must be answered, got %+v", got)
	}
}

func TestBot_AgeLimitCanBeDisabled(t *testing.T) {
	e := newEnv(t, echo, func(o *Options) { o.MaxAge = 0 })

	e.handleAt("OLD", "628@s.whatsapp.net", "ancient", e.clock.Now().Add(-48*time.Hour))

	if got := e.tr.next(t); got.text != "re: ancient" {
		t.Fatalf("got %+v", got)
	}
}

func (e *env) seed(t *testing.T, waID, chat string, dir store.Direction, age time.Duration) {
	t.Helper()
	_, err := e.st.Messages.Create(context.Background(), store.Message{
		ClientID: e.clientID, WAID: waID, Chat: chat, Direction: dir, Body: waID, CreatedAt: e.clock.Now().Add(-age),
	})
	if err != nil {
		t.Fatalf("seed %s: %v", waID, err)
	}
}

func TestBot_AnswersMessagesThatWereWaitingBeforeARestart(t *testing.T) {
	e := newEnv(t, echo, func(o *Options) { o.MaxAge = 10 * time.Minute })
	ctx := context.Background()

	e.seed(t, "W1", "waiting@s.whatsapp.net", store.DirectionIn, 2*time.Minute)
	e.seed(t, "A1", "answered@s.whatsapp.net", store.DirectionIn, 3*time.Minute)
	e.seed(t, "A2", "answered@s.whatsapp.net", store.DirectionOut, 2*time.Minute)
	e.seed(t, "O1", "old@s.whatsapp.net", store.DirectionIn, time.Hour)

	pending, err := e.bot.Unanswered(ctx, e.clientID)
	if err != nil {
		t.Fatalf("unanswered: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "W1" || pending[0].Chat != "waiting@s.whatsapp.net" || pending[0].Text != "W1" {
		t.Fatalf("pending = %+v, want only the waiting chat's message", pending)
	}

	e.bot.Answer(ctx, e.tr, e.clientID, pending)

	if got := e.tr.next(t); got.chat != "waiting@s.whatsapp.net" || got.text != "re: W1" {
		t.Fatalf("got %+v", got)
	}
	e.waitForMessages(t, store.DirectionOut, 2) // the seeded reply plus the new one
	if n := e.countMessages(t, store.DirectionIn); n != 3 {
		t.Fatalf("answering must not store the incoming message again, got %d incoming messages", n)
	}
}

func TestBot_RecoveryHonorsAndCanIgnoreTheAgeLimit(t *testing.T) {
	e := newEnv(t, echo, func(o *Options) { o.MaxAge = 0 })
	e.seed(t, "O1", "old@s.whatsapp.net", store.DirectionIn, 48*time.Hour)

	pending, err := e.bot.Unanswered(context.Background(), e.clientID)
	if err != nil {
		t.Fatalf("unanswered: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("with the age limit disabled every waiting message counts, got %+v", pending)
	}
}
