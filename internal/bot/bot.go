package bot

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/androsyz/wasabot/internal/logger"
	"github.com/androsyz/wasabot/internal/ratelimit"
	"github.com/androsyz/wasabot/internal/store"
)

const (
	queueSize  = 64
	rateWindow = time.Minute
)

type Message struct {
	ID        string
	Chat      string
	Sender    string
	Text      string
	Timestamp time.Time // when the customer sent it; zero if unknown
}

type Transport interface {
	// Send returns the ID the messaging service assigned to the sent message.
	Send(ctx context.Context, chat, text string) (id string, err error)
}

// Responder produces the reply to msg; an empty reply means "say nothing".
type Responder func(ctx context.Context, clientID int64, msg Message) (string, error)

type Options struct {
	Workers   int
	Fallback  string        // sent when the responder fails; empty stays silent
	RateLimit int           // messages answered per minute per chat; 0 disables
	MaxAge    time.Duration // older incoming messages are stored but not answered; 0 disables
	Now       func() time.Time
}

type job struct {
	transport Transport
	clientID  int64
	msg       Message
}

type Bot struct {
	messages *store.Messages
	log      *slog.Logger
	respond  Responder
	fallback string
	maxAge   time.Duration
	limiter  *ratelimit.Limiter
	now      func() time.Time
	queues   []chan job
	wg       sync.WaitGroup
}

func New(messages *store.Messages, log *slog.Logger, respond Responder, opts Options) *Bot {
	queues := make([]chan job, max(opts.Workers, 1))
	for i := range queues {
		queues[i] = make(chan job, queueSize)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Bot{
		messages: messages,
		log:      log,
		respond:  respond,
		fallback: opts.Fallback,
		maxAge:   opts.MaxAge,
		limiter:  ratelimit.New(opts.RateLimit, rateWindow),
		now:      now,
		queues:   queues,
	}
}

// Start runs the workers until ctx is cancelled; Wait blocks until they have stopped.
func (b *Bot) Start(ctx context.Context) {
	for _, q := range b.queues {
		b.wg.Add(1)
		go b.work(ctx, q)
	}
}

func (b *Bot) Wait() {
	b.wg.Wait()
}

// Handle stores the message and queues it for a reply. It returns quickly so the messaging
// library's event loop is not blocked, and a redelivered message (same ID) is ignored.
// Messages of one chat go to one worker, so replies keep their order.
//
// A message that is too old, or that exceeds the chat's rate limit, is stored but not answered.
func (b *Bot) Handle(ctx context.Context, t Transport, clientID int64, msg Message) {
	log := b.log.With("client_id", clientID, "message_id", msg.ID, "chat", logger.MaskJIDs(msg.Chat))

	_, err := b.messages.Create(ctx, store.Message{
		ClientID:  clientID,
		WAID:      msg.ID,
		Chat:      msg.Chat,
		Direction: store.DirectionIn,
		Body:      msg.Text,
		CreatedAt: msg.Timestamp,
	})
	if errors.Is(err, store.ErrDuplicateMessage) {
		log.Info("duplicate message ignored")
		return
	}
	if err != nil {
		log.Error("store incoming message", "error", err)
		return
	}

	now := b.now()
	if b.maxAge > 0 && !msg.Timestamp.IsZero() && now.Sub(msg.Timestamp) > b.maxAge {
		log.Info("old message stored without a reply", "age", now.Sub(msg.Timestamp).Round(time.Second))
		return
	}
	if !b.limiter.Allow(fmt.Sprintf("%d/%s", clientID, msg.Chat), now) {
		log.Warn("rate limit reached, message stored without a reply")
		return
	}

	b.enqueue(ctx, job{transport: t, clientID: clientID, msg: msg})
}

// Unanswered lists the messages customers are still waiting on, for example because the process
// stopped before replying. Call it before connecting, so a message delivered live afterwards
// is not mistaken for an earlier one.
func (b *Bot) Unanswered(ctx context.Context, clientID int64) ([]Message, error) {
	var since time.Time
	if b.maxAge > 0 {
		since = b.now().Add(-b.maxAge)
	}
	stored, err := b.messages.ListUnanswered(ctx, clientID, since)
	if err != nil {
		return nil, err
	}

	pending := make([]Message, len(stored))
	for i, m := range stored {
		pending[i] = Message{ID: m.WAID, Chat: m.Chat, Sender: m.Chat, Text: m.Body, Timestamp: m.CreatedAt}
	}
	return pending, nil
}

// Answer queues messages returned by Unanswered. They are already stored, so nothing is saved twice.
func (b *Bot) Answer(ctx context.Context, t Transport, clientID int64, pending []Message) {
	for _, msg := range pending {
		b.log.Info("answering a message received before the restart",
			"client_id", clientID, "message_id", msg.ID, "chat", logger.MaskJIDs(msg.Chat))
		b.enqueue(ctx, job{transport: t, clientID: clientID, msg: msg})
	}
}

func (b *Bot) enqueue(ctx context.Context, j job) {
	select {
	case b.queueFor(j.clientID, j.msg.Chat) <- j:
	case <-ctx.Done():
	}
}

func (b *Bot) queueFor(clientID int64, chat string) chan job {
	h := fnv.New32a()
	fmt.Fprintf(h, "%d/%s", clientID, chat)
	return b.queues[h.Sum32()%uint32(len(b.queues))]
}

func (b *Bot) work(ctx context.Context, q <-chan job) {
	defer b.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-q:
			b.process(ctx, j)
		}
	}
}

func (b *Bot) process(ctx context.Context, j job) {
	log := b.log.With("client_id", j.clientID, "message_id", j.msg.ID, "chat", logger.MaskJIDs(j.msg.Chat))

	reply, err := b.respond(ctx, j.clientID, j.msg)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		log.Error("respond", "error", err)
		reply = b.fallback
	}
	if reply == "" {
		return
	}

	id, err := j.transport.Send(ctx, j.msg.Chat, reply)
	if err != nil {
		log.Error("send reply", "error", err)
		return
	}

	_, err = b.messages.Create(ctx, store.Message{
		ClientID:  j.clientID,
		WAID:      id,
		Chat:      j.msg.Chat,
		Direction: store.DirectionOut,
		Body:      reply,
	})
	if err != nil {
		log.Error("store outgoing message", "error", err)
		return
	}
	log.Info("reply sent")
}
