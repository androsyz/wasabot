package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const (
	requestTimeout = 90 * time.Second
	maxRetries     = 2
)

// Client talks to any OpenAI-compatible chat completions API.
type Client struct {
	chat openai.ChatService
}

// NewClient builds only the chat service: unlike openai.NewClient it ignores OPENAI_*
// environment variables, so unrelated keys or headers are never sent to the configured provider.
// An empty apiKey sends no Authorization header, for local servers that need none.
func NewClient(apiKey, baseURL string) *Client {
	opts := []option.RequestOption{
		option.WithHTTPClient(&http.Client{Timeout: requestTimeout}),
		option.WithBaseURL(strings.TrimSuffix(baseURL, "/") + "/"),
		option.WithMaxRetries(maxRetries),
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	return &Client{chat: openai.NewChatService(opts...)}
}

func (o *Client) Complete(ctx context.Context, req Request) (Message, error) {
	messages, err := toOpenAIMessages(req.Messages)
	if err != nil {
		return Message{}, err
	}

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(req.Model),
		Messages: messages,
	}
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  shared.FunctionParameters(t.Parameters),
		}))
	}

	resp, err := o.chat.Completions.New(ctx, params)
	if err != nil {
		return Message{}, fmt.Errorf("chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Message{}, errors.New("chat completion: no choices returned")
	}
	return fromOpenAI(resp.Choices[0].Message), nil
}

func toOpenAIMessages(msgs []Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case RoleTool:
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		case RoleAssistant:
			out = append(out, assistantMessage(m))
		default:
			return nil, fmt.Errorf("unsupported message role %q", m.Role)
		}
	}
	return out, nil
}

func assistantMessage(m Message) openai.ChatCompletionMessageParamUnion {
	p := openai.ChatCompletionAssistantMessageParam{}
	if m.Content != "" {
		p.Content = openai.ChatCompletionAssistantMessageParamContentUnion{OfString: openai.String(m.Content)}
	}
	for _, tc := range m.ToolCalls {
		p.ToolCalls = append(p.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
			OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
				ID: tc.ID,
				Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			},
		})
	}
	return openai.ChatCompletionMessageParamUnion{OfAssistant: &p}
}

func fromOpenAI(m openai.ChatCompletionMessage) Message {
	msg := Message{Role: RoleAssistant, Content: m.Content}
	for _, tc := range m.ToolCalls {
		if tc.Type != "function" {
			continue
		}
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return msg
}
