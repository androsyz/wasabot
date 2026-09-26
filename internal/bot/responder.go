package bot

import (
	"context"
	"errors"
	"fmt"

	"github.com/androsyz/wasabot/internal/agent"
	"github.com/androsyz/wasabot/internal/store"
)

type Agent interface {
	Reply(ctx context.Context, conversation []agent.Message) (string, error)
}

// NewAgentResponder answers with a, using up to historyLimit stored messages of the chat as memory.
// The conversation ends at the message being answered, so a message that arrived later never
// leaks into an earlier reply.
func NewAgentResponder(a Agent, messages *store.Messages, historyLimit int) Responder {
	return func(ctx context.Context, clientID int64, msg Message) (string, error) {
		history, err := messages.ListRecent(ctx, clientID, msg.Chat, msg.ID, historyLimit)
		if err != nil {
			return "", fmt.Errorf("load conversation: %w", err)
		}
		if len(history) == 0 {
			return "", errors.New("load conversation: the incoming message is not stored")
		}
		return a.Reply(ctx, toAgentMessages(history))
	}
}

func toAgentMessages(history []store.Message) []agent.Message {
	out := make([]agent.Message, len(history))
	for i, m := range history {
		role := agent.RoleUser
		if m.Direction == store.DirectionOut {
			role = agent.RoleAssistant
		}
		out[i] = agent.Message{Role: role, Content: m.Body}
	}
	return out
}
