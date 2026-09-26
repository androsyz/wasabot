package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/androsyz/wasabot/internal/agent"
	"github.com/androsyz/wasabot/internal/db/dbtest"
	"github.com/androsyz/wasabot/internal/store"
)

type fakeAgent struct {
	got   []agent.Message
	reply string
	err   error
}

func (f *fakeAgent) Reply(_ context.Context, conversation []agent.Message) (string, error) {
	f.got = conversation
	return f.reply, f.err
}

func seed(t *testing.T, st *store.Store, clientID int64, waID string, dir store.Direction, body string) {
	t.Helper()
	_, err := st.Messages.Create(context.Background(), store.Message{
		ClientID: clientID, WAID: waID, Chat: "628@s.whatsapp.net", Direction: dir, Body: body,
	})
	if err != nil {
		t.Fatalf("seed %s: %v", waID, err)
	}
}

func TestAgentResponder_UsesTheConversationUpToTheIncomingMessage(t *testing.T) {
	ctx := context.Background()
	st := store.New(dbtest.New(t))
	c, err := st.Clients.Create(ctx, "Acme")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	seed(t, st, c.ID, "M1", store.DirectionIn, "hello")
	seed(t, st, c.ID, "M2", store.DirectionOut, "hi, how can I help?")
	seed(t, st, c.ID, "M3", store.DirectionIn, "what are your hours?")
	seed(t, st, c.ID, "M4", store.DirectionIn, "also, do you deliver?") // arrived while M3 was queued

	fake := &fakeAgent{reply: "9 to 5"}
	respond := NewAgentResponder(fake, st.Messages, 10)

	got, err := respond(ctx, c.ID, Message{ID: "M3", Chat: "628@s.whatsapp.net", Text: "what are your hours?"})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if got != "9 to 5" {
		t.Fatalf("got %q", got)
	}

	want := []agent.Message{
		{Role: agent.RoleUser, Content: "hello"},
		{Role: agent.RoleAssistant, Content: "hi, how can I help?"},
		{Role: agent.RoleUser, Content: "what are your hours?"},
	}
	if len(fake.got) != len(want) {
		t.Fatalf("conversation = %+v, want %d messages (M4 must not be included)", fake.got, len(want))
	}
	for i := range want {
		if fake.got[i].Role != want[i].Role || fake.got[i].Content != want[i].Content {
			t.Errorf("message %d = %+v, want %+v", i, fake.got[i], want[i])
		}
	}
}

func TestAgentResponder_RespectsTheHistoryLimit(t *testing.T) {
	ctx := context.Background()
	st := store.New(dbtest.New(t))
	c, _ := st.Clients.Create(ctx, "Acme")
	for _, id := range []string{"M1", "M2", "M3", "M4"} {
		seed(t, st, c.ID, id, store.DirectionIn, "msg "+id)
	}

	fake := &fakeAgent{reply: "ok"}
	respond := NewAgentResponder(fake, st.Messages, 2)

	if _, err := respond(ctx, c.ID, Message{ID: "M4", Chat: "628@s.whatsapp.net"}); err != nil {
		t.Fatalf("respond: %v", err)
	}

	if len(fake.got) != 2 || fake.got[0].Content != "msg M3" || fake.got[1].Content != "msg M4" {
		t.Fatalf("conversation = %+v, want the newest two", fake.got)
	}
}

func TestAgentResponder_Errors(t *testing.T) {
	ctx := context.Background()
	st := store.New(dbtest.New(t))
	c, _ := st.Clients.Create(ctx, "Acme")
	seed(t, st, c.ID, "M1", store.DirectionIn, "hello")

	t.Run("incoming message was never stored", func(t *testing.T) {
		respond := NewAgentResponder(&fakeAgent{}, st.Messages, 10)

		_, err := respond(ctx, c.ID, Message{ID: "ghost", Chat: "628@s.whatsapp.net"})
		if err == nil || !strings.Contains(err.Error(), "not stored") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("agent failure is returned", func(t *testing.T) {
		boom := errors.New("llm down")
		respond := NewAgentResponder(&fakeAgent{err: boom}, st.Messages, 10)

		_, err := respond(ctx, c.ID, Message{ID: "M1", Chat: "628@s.whatsapp.net"})
		if !errors.Is(err, boom) {
			t.Fatalf("got %v, want the agent's error", err)
		}
	})
}
