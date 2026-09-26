package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxSteps    = 5
	defaultToolTimeout = 15 * time.Second
	maxToolOutputRunes = 4000
)

var ErrMaxSteps = errors.New("agent: no final answer within the step limit")

type Tool interface {
	Spec() ToolSpec
	// Run's error is shown to the model as text, so it must be safe to disclose.
	Run(ctx context.Context, arguments string) (string, error)
}

type Options struct {
	LLM          LLM
	Definition   Definition
	DefaultModel string // used when the definition has no model
	Tools        []Tool // every tool that exists; the definition selects the ones the agent may use
	MaxSteps     int
	ToolTimeout  time.Duration
	Log          *slog.Logger
}

type Agent struct {
	llm         LLM
	model       string
	system      string
	specs       []ToolSpec
	tools       map[string]Tool
	maxSteps    int
	toolTimeout time.Duration
	log         *slog.Logger
}

func New(o Options) (*Agent, error) {
	model := o.Definition.Model
	if model == "" {
		model = o.DefaultModel
	}
	if model == "" {
		return nil, errors.New("no model: set one in the agent file or in the LLM settings")
	}

	registered := make(map[string]Tool, len(o.Tools))
	for _, t := range o.Tools {
		registered[t.Spec().Name] = t
	}
	allowed := make(map[string]Tool, len(o.Definition.Tools))
	var specs []ToolSpec
	for _, name := range o.Definition.Tools {
		t, ok := registered[name]
		if !ok {
			return nil, fmt.Errorf("agent %q uses unknown tool %q", o.Definition.Name, name)
		}
		if _, dup := allowed[name]; dup {
			continue
		}
		allowed[name] = t
		specs = append(specs, t.Spec())
	}

	a := &Agent{
		llm:         o.LLM,
		model:       model,
		system:      systemPrompt(o.Definition),
		specs:       specs,
		tools:       allowed,
		maxSteps:    o.MaxSteps,
		toolTimeout: o.ToolTimeout,
		log:         o.Log,
	}
	if a.maxSteps <= 0 {
		a.maxSteps = defaultMaxSteps
	}
	if a.toolTimeout <= 0 {
		a.toolTimeout = defaultToolTimeout
	}
	if a.log == nil {
		a.log = slog.Default()
	}
	return a, nil
}

func systemPrompt(def Definition) string {
	if def.Language == "" {
		return def.Prompt
	}
	return def.Prompt + "\n\nDefault reply language: " + def.Language + " (use another language only if the user writes in it)."
}

// Reply answers the last message of conversation, which holds only user and assistant messages,
// oldest first. Customer text stays in user messages and never joins the system prompt.
func (a *Agent) Reply(ctx context.Context, conversation []Message) (string, error) {
	msgs := make([]Message, 0, len(conversation)+1)
	msgs = append(msgs, Message{Role: RoleSystem, Content: a.system})
	msgs = append(msgs, conversation...)

	for range a.maxSteps {
		reply, err := a.llm.Complete(ctx, Request{Model: a.model, Messages: msgs, Tools: a.specs})
		if err != nil {
			return "", fmt.Errorf("llm: %w", err)
		}
		if len(reply.ToolCalls) == 0 {
			return strings.TrimSpace(reply.Content), nil
		}

		msgs = append(msgs, reply)
		msgs = append(msgs, a.runTools(ctx, reply.ToolCalls)...)
	}
	return "", ErrMaxSteps
}

// runTools runs the calls concurrently; results keep the order and IDs of the calls.
func (a *Agent) runTools(ctx context.Context, calls []ToolCall) []Message {
	results := make([]Message, len(calls))

	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = Message{Role: RoleTool, ToolCallID: c.ID, Content: a.runTool(ctx, c)}
		}()
	}
	wg.Wait()
	return results
}

// runTool never fails: every problem becomes text for the model to react to.
func (a *Agent) runTool(ctx context.Context, c ToolCall) string {
	tool, ok := a.tools[c.Name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", c.Name)
	}
	a.log.Info("tool call", "tool", c.Name)

	ctx, cancel := context.WithTimeout(ctx, a.toolTimeout)
	defer cancel()

	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.log.Error("tool panicked", "tool", c.Name, "panic", r)
				done <- outcome{err: errors.New("the tool crashed")}
			}
		}()
		out, err := tool.Run(ctx, c.Arguments)
		done <- outcome{out, err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			a.log.Warn("tool failed", "tool", c.Name, "error", res.err)
			return "error: " + res.err.Error()
		}
		return truncate(res.out)
	case <-ctx.Done():
		a.log.Warn("tool did not finish", "tool", c.Name, "error", ctx.Err())
		return "error: the tool did not finish in time"
	}
}

func truncate(s string) string {
	r := []rune(s)
	if len(r) <= maxToolOutputRunes {
		return s
	}
	return string(r[:maxToolOutputRunes]) + "\n[truncated]"
}
