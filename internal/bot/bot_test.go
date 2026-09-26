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

type env struct {
	bot      *Bot
	st       *store.Store
	sqlDB    *sql.DB
	clientID int64
	tr       *fakeTransport
}

func newEnv(t *testing.T, respond Responder) *env {
	t.Helper()
	sqlDB := dbtest.New(t)
	st := store.New(sqlDB)
	c, err := st.Clients.Create(context.Background(), "Acme")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	b := New(st.Messages, log, respond, 3)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)
	t.Cleanup(func() {
		cancel()
		b.Wait()
	})
	return &env{bot: b, st: st, sqlDB: sqlDB, clientID: c.ID, tr: newFakeTransport()}
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
