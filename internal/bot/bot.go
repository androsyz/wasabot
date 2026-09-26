package bot

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"

	"github.com/androsyz/wasabot/internal/logger"
	"github.com/androsyz/wasabot/internal/store"
)

const queueSize = 64

type Message struct {
	ID     string
	Chat   string
	Sender string
	Text   string
}

type Transport interface {
	// Send returns the ID the messaging service assigned to the sent message.
	Send(ctx context.Context, chat, text string) (id string, err error)
}

// Responder produces the reply to msg; an empty reply means "say nothing".
type Responder func(ctx context.Context, clientID int64, msg Message) (string, error)

type job struct {
	transport Transport
	clientID  int64
	msg       Message
}

type Bot struct {
	messages *store.Messages
	log      *slog.Logger
	respond  Responder
	queues   []chan job
	wg       sync.WaitGroup
}

func New(messages *store.Messages, log *slog.Logger, respond Responder, workers int) *Bot {
	queues := make([]chan job, max(workers, 1))
	for i := range queues {
		queues[i] = make(chan job, queueSize)
	}
	return &Bot{messages: messages, log: log, respond: respond, queues: queues}
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
func (b *Bot) Handle(ctx context.Context, t Transport, clientID int64, msg Message) {
	log := b.log.With("client_id", clientID, "message_id", msg.ID, "chat", logger.MaskJIDs(msg.Chat))

	_, err := b.messages.Create(ctx, store.Message{
		ClientID:  clientID,
		WAID:      msg.ID,
		Chat:      msg.Chat,
		Direction: store.DirectionIn,
		Body:      msg.Text,
	})
	if errors.Is(err, store.ErrDuplicateMessage) {
		log.Info("duplicate message ignored")
		return
	}
	if err != nil {
		log.Error("store incoming message", "error", err)
		return
	}

	select {
	case b.queueFor(clientID, msg.Chat) <- job{transport: t, clientID: clientID, msg: msg}:
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
		log.Error("respond", "error", err)
		return
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
