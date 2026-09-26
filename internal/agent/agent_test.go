package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type scriptedLLM struct {
	replies  []Message
	err      error
	requests []Request
}

func (s *scriptedLLM) Complete(_ context.Context, req Request) (Message, error) {
	s.requests = append(s.requests, req)
	if s.err != nil {
		return Message{}, s.err
	}
	i := min(len(s.requests)-1, len(s.replies)-1)
	return s.replies[i], nil
}

type funcTool struct {
	name string
	run  func(ctx context.Context, args string) (string, error)
}

func (f funcTool) Spec() ToolSpec {
	return ToolSpec{Name: f.name, Description: f.name + " tool", Parameters: map[string]any{"type": "object"}}
}

func (f funcTool) Run(ctx context.Context, args string) (string, error) { return f.run(ctx, args) }

func constTool(name, result string) funcTool {
	return funcTool{name: name, run: func(context.Context, string) (string, error) { return result, nil }}
}

func text(s string) Message { return Message{Role: RoleAssistant, Content: s} }

func calls(cs ...ToolCall) Message { return Message{Role: RoleAssistant, ToolCalls: cs} }

func newAgent(t *testing.T, llm LLM, def Definition, tools ...Tool) *Agent {
	t.Helper()
	a, err := New(Options{
		LLM:          llm,
		Definition:   def,
		DefaultModel: "default-model",
		Tools:        tools,
		ToolTimeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	return a
}

var userHi = []Message{{Role: RoleUser, Content: "hi"}}

func TestAgent_Reply_PlainAnswer(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{text("  Halo! \n")}}
	a := newAgent(t, llm, Definition{Name: "Bot", Model: "def-model", Prompt: "Be brief."})

	conversation := []Message{
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Content: "hi there"},
		{Role: RoleUser, Content: "how are you?"},
	}
	got, err := a.Reply(context.Background(), conversation)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got != "Halo!" {
		t.Fatalf("got %q, want the trimmed answer", got)
	}

	req := llm.requests[0]
	if req.Model != "def-model" {
		t.Errorf("model = %q, want the definition's model", req.Model)
	}
	if len(req.Messages) != 4 || req.Messages[0].Role != RoleSystem || req.Messages[0].Content != "Be brief." {
		t.Fatalf("messages = %+v, want the system prompt first, then the conversation", req.Messages)
	}
	if req.Messages[3].Content != "how are you?" {
		t.Errorf("last message = %+v", req.Messages[3])
	}
	if req.Tools != nil {
		t.Errorf("tools = %+v, want none", req.Tools)
	}
}

func TestAgent_ModelAndLanguage(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{text("ok")}}
	a := newAgent(t, llm, Definition{Name: "Bot", Language: "id", Prompt: "Be brief."})

	if _, err := a.Reply(context.Background(), userHi); err != nil {
		t.Fatalf("reply: %v", err)
	}

	req := llm.requests[0]
	if req.Model != "default-model" {
		t.Errorf("model = %q, want the default model", req.Model)
	}
	system := req.Messages[0].Content
	if !strings.HasPrefix(system, "Be brief.") || !strings.Contains(system, "Default reply language: id") {
		t.Errorf("system prompt = %q", system)
	}
}

func TestNew_Errors(t *testing.T) {
	t.Run("no model anywhere", func(t *testing.T) {
		_, err := New(Options{LLM: &scriptedLLM{}, Definition: Definition{Name: "Bot", Prompt: "p"}})
		if err == nil || !strings.Contains(err.Error(), "no model") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("definition names a tool that is not registered", func(t *testing.T) {
		_, err := New(Options{
			LLM:          &scriptedLLM{},
			Definition:   Definition{Name: "Bot", Prompt: "p", Tools: []string{"launch_missiles"}},
			DefaultModel: "m",
			Tools:        []Tool{constTool("current_time", "")},
		})
		if err == nil || !strings.Contains(err.Error(), `unknown tool "launch_missiles"`) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestAgent_Reply_ToolRoundTrip(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		calls(ToolCall{ID: "call_1", Name: "current_time", Arguments: `{"timezone":"UTC"}`}),
		text("It is noon."),
	}}
	var gotArgs string
	clock := funcTool{name: "current_time", run: func(_ context.Context, args string) (string, error) {
		gotArgs = args
		return "12:00", nil
	}}
	a := newAgent(t, llm, Definition{Name: "Bot", Prompt: "p", Tools: []string{"current_time"}}, clock)

	got, err := a.Reply(context.Background(), userHi)
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if got != "It is noon." || gotArgs != `{"timezone":"UTC"}` {
		t.Fatalf("got %q with args %q", got, gotArgs)
	}

	if len(llm.requests) != 2 {
		t.Fatalf("llm called %d times, want 2", len(llm.requests))
	}
	if len(llm.requests[0].Tools) != 1 || llm.requests[0].Tools[0].Name != "current_time" {
		t.Errorf("tools offered = %+v", llm.requests[0].Tools)
	}
	second := llm.requests[1].Messages
	if len(second) != 4 {
		t.Fatalf("second request has %d messages, want system, user, assistant, tool", len(second))
	}
	if second[2].Role != RoleAssistant || second[2].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant message = %+v", second[2])
	}
	if want := (Message{Role: RoleTool, ToolCallID: "call_1", Content: "12:00"}); second[3].Role != want.Role ||
		second[3].ToolCallID != want.ToolCallID || second[3].Content != want.Content {
		t.Errorf("tool message = %+v, want %+v", second[3], want)
	}
}

func TestAgent_OnlyToolsNamedInTheDefinitionAreAvailable(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		calls(ToolCall{ID: "c1", Name: "secret_admin", Arguments: "{}"}),
		text("done"),
	}}
	var adminRan bool
	admin := funcTool{name: "secret_admin", run: func(context.Context, string) (string, error) {
		adminRan = true
		return "ok", nil
	}}
	a := newAgent(t, llm, Definition{Name: "Bot", Prompt: "p", Tools: []string{"current_time"}}, constTool("current_time", "12:00"), admin)

	if _, err := a.Reply(context.Background(), userHi); err != nil {
		t.Fatalf("reply: %v", err)
	}

	if adminRan {
		t.Fatal("a registered tool the definition does not list must not run, even if the model asks for it")
	}
	if got := llm.requests[0].Tools; len(got) != 1 || got[0].Name != "current_time" {
		t.Errorf("tools offered = %+v, want only current_time", got)
	}
	if result := llm.requests[1].Messages[3].Content; !strings.Contains(result, `unknown tool "secret_admin"`) {
		t.Errorf("tool result = %q", result)
	}
}

func TestAgent_ToolCallsRunConcurrentlyAndKeepOrder(t *testing.T) {
	var started sync.WaitGroup
	started.Add(3)
	barrier := func(name string) funcTool {
		return funcTool{name: name, run: func(context.Context, string) (string, error) {
			started.Done()
			started.Wait() // returns only once all three tools are running at the same time
			return "result of " + name, nil
		}}
	}
	llm := &scriptedLLM{replies: []Message{
		calls(ToolCall{ID: "c1", Name: "a"}, ToolCall{ID: "c2", Name: "b"}, ToolCall{ID: "c3", Name: "c"}),
		text("done"),
	}}
	a := newAgent(t, llm, Definition{Name: "Bot", Prompt: "p", Tools: []string{"a", "b", "c"}}, barrier("a"), barrier("b"), barrier("c"))

	if _, err := a.Reply(context.Background(), userHi); err != nil {
		t.Fatalf("reply: %v", err)
	}

	msgs := llm.requests[1].Messages
	for i, name := range []string{"a", "b", "c"} {
		m := msgs[3+i]
		if m.Role != RoleTool || m.ToolCallID != "c"+string(rune('1'+i)) || m.Content != "result of "+name {
			t.Errorf("result %d = %+v", i, m)
		}
	}
}

func TestAgent_ToolProblemsBecomeTextForTheModel(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	tests := []struct {
		name string
		tool funcTool
		want string
	}{
		{
			name: "error",
			tool: funcTool{name: "t", run: func(context.Context, string) (string, error) { return "", errors.New("boom") }},
			want: "error: boom",
		},
		{
			name: "panic",
			tool: funcTool{name: "t", run: func(context.Context, string) (string, error) { panic("oh no") }},
			want: "error: the tool crashed",
		},
		{
			name: "does not finish even though it ignores the context",
			tool: funcTool{name: "t", run: func(context.Context, string) (string, error) { <-release; return "late", nil }},
			want: "error: the tool did not finish in time",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &scriptedLLM{replies: []Message{calls(ToolCall{ID: "c1", Name: "t"}), text("sorry about that")}}
			a, err := New(Options{
				LLM:          llm,
				Definition:   Definition{Name: "Bot", Prompt: "p", Tools: []string{"t"}},
				DefaultModel: "m",
				Tools:        []Tool{tt.tool},
				ToolTimeout:  50 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			got, err := a.Reply(context.Background(), userHi)
			if err != nil {
				t.Fatalf("a tool problem must not fail the reply: %v", err)
			}
			if got != "sorry about that" {
				t.Errorf("got %q", got)
			}
			if result := llm.requests[1].Messages[3].Content; result != tt.want {
				t.Errorf("tool result = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestAgent_LongToolOutputIsTruncated(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{calls(ToolCall{ID: "c1", Name: "big"}), text("ok")}}
	a := newAgent(t, llm, Definition{Name: "Bot", Prompt: "p", Tools: []string{"big"}}, constTool("big", strings.Repeat("é", 10000)))

	if _, err := a.Reply(context.Background(), userHi); err != nil {
		t.Fatalf("reply: %v", err)
	}

	result := llm.requests[1].Messages[3].Content
	if !strings.HasSuffix(result, "[truncated]") || len([]rune(result)) > maxToolOutputRunes+20 {
		t.Fatalf("result has %d runes, want it cut near %d", len([]rune(result)), maxToolOutputRunes)
	}
}

func TestAgent_Reply_StopsAtTheStepLimit(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{calls(ToolCall{ID: "c1", Name: "loop"})}}
	a, err := New(Options{
		LLM:          llm,
		Definition:   Definition{Name: "Bot", Prompt: "p", Tools: []string{"loop"}},
		DefaultModel: "m",
		Tools:        []Tool{constTool("loop", "again")},
		MaxSteps:     3,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	_, err = a.Reply(context.Background(), userHi)

	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("got %v, want ErrMaxSteps", err)
	}
	if len(llm.requests) != 3 {
		t.Fatalf("llm called %d times, want exactly the step limit (3)", len(llm.requests))
	}
}

func TestAgent_Reply_LLMFailureIsAnError(t *testing.T) {
	boom := errors.New("provider down")
	llm := &scriptedLLM{err: boom}
	a := newAgent(t, llm, Definition{Name: "Bot", Prompt: "p"})

	_, err := a.Reply(context.Background(), userHi)

	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want it to wrap the llm error", err)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("llm called %d times, want 1 (no retry loop here)", len(llm.requests))
	}
}
