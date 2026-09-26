package agent

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON written by the model; untrusted
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // set on assistant messages
	ToolCallID string     // set on tool messages, the ID of the call being answered
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema
}

type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolSpec
}

type LLM interface {
	Complete(ctx context.Context, req Request) (Message, error)
}
