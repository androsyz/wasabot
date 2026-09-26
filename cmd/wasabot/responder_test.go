package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/androsyz/wasabot/internal/bot"
	"github.com/androsyz/wasabot/internal/config"
	"github.com/androsyz/wasabot/internal/db/dbtest"
	"github.com/androsyz/wasabot/internal/store"
)

var discardLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func writeAgentFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write agent file: %v", err)
	}
	return path
}

func TestNewResponder_WithoutLLMEchoes(t *testing.T) {
	respond, _, err := newResponder(config.Config{LLMBaseURL: "https://api.openai.com/v1"}, discardLog, nil)
	if err != nil {
		t.Fatalf("newResponder: %v", err)
	}

	got, err := respond(context.Background(), 1, bot.Message{Text: "hi"})
	if err != nil || got != "you said: hi" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNewResponder_StartupErrors(t *testing.T) {
	base := config.Config{LLMBaseURL: "http://localhost:1/v1", LLMAPIKey: "k", LLMModel: "m"}

	t.Run("agent file is missing", func(t *testing.T) {
		cfg := base
		cfg.AgentFile = filepath.Join(t.TempDir(), "nope.md")

		if _, _, err := newResponder(cfg, discardLog, nil); err == nil {
			t.Fatal("want an error")
		}
	})

	t.Run("agent file asks for a tool that does not exist", func(t *testing.T) {
		cfg := base
		cfg.AgentFile = writeAgentFile(t, "---\nname: Bot\ntools: launch_missiles\n---\nHi")

		_, _, err := newResponder(cfg, discardLog, nil)
		if err == nil || !strings.Contains(err.Error(), `unknown tool "launch_missiles"`) || !strings.Contains(err.Error(), cfg.AgentFile) {
			t.Fatalf("got %v, want an error naming the tool and the file", err)
		}
	})
}

func TestBuiltInAgentWorksWithoutAnyAgentFile(t *testing.T) {
	cfg := config.Config{LLMBaseURL: "http://localhost:1/v1", LLMAPIKey: "k", LLMModel: "gpt-4o-mini"} // AgentFile is empty

	respond, label, err := newResponder(cfg, discardLog, nil)

	if err != nil || respond == nil {
		t.Fatalf("a bare binary must start with the built-in agent: %v", err)
	}
	if label != "'built-in' | gpt-4o-mini" {
		t.Fatalf("label = %q", label)
	}
}

func TestExampleAgentFileIsValid(t *testing.T) {
	cfg := config.Config{LLMBaseURL: "http://localhost:1/v1", LLMAPIKey: "k", LLMModel: "m", AgentFile: "../../examples/agent.md"}

	if _, _, err := newResponder(cfg, discardLog, nil); err != nil {
		t.Fatalf("examples/agent.md is what users copy, so it must load: %v", err)
	}
}

type recordingTransport struct {
	replies chan string
}

func (r *recordingTransport) Send(_ context.Context, _, text string) (string, error) {
	r.replies <- text
	return "OUT1", nil
}

// TestBotAnswersThroughTheLLMAndATool wires the real bot, agent, tool, store and HTTP client
// against a fake LLM server: everything except WhatsApp itself.
func TestBotAnswersThroughTheLLMAndATool(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []map[string]any
		auths    []string
	)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		requests = append(requests, body)
		auths = append(auths, r.Header.Get("Authorization"))
		n := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			io.WriteString(w, `{"id":"1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"tool_calls",
				"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function",
				"function":{"name":"current_time","arguments":"{\"timezone\":\"Asia/Jakarta\"}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"id":"2","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop",
			"message":{"role":"assistant","content":"Sekarang sudah malam."}}]}`)
	}))
	t.Cleanup(llm.Close)

	sqlDB := dbtest.New(t)
	st := store.New(sqlDB)
	client, err := st.Clients.Create(context.Background(), "Acme")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	cfg := config.Config{
		LLMBaseURL: llm.URL,
		LLMAPIKey:  "test-key",
		LLMModel:   "test-model",
		AgentFile:  writeAgentFile(t, "---\nname: Bot\nlanguage: id\ntools: current_time\n---\nYou answer briefly."),
	}
	respond, _, err := newResponder(cfg, discardLog, st.Messages)
	if err != nil {
		t.Fatalf("newResponder: %v", err)
	}

	b := bot.New(st.Messages, discardLog, respond, bot.Options{Workers: 1})
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)
	t.Cleanup(func() {
		cancel()
		b.Wait()
	})

	transport := &recordingTransport{replies: make(chan string, 1)}
	b.Handle(ctx, transport, client.ID, bot.Message{ID: "IN1", Chat: "628@s.whatsapp.net", Sender: "628@s.whatsapp.net", Text: "jam berapa sekarang?"})

	select {
	case reply := <-transport.replies:
		if reply != "Sekarang sudah malam." {
			t.Fatalf("reply = %q", reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the reply")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("llm called %d times, want 2 (tool call, then answer)", len(requests))
	}
	if auths[0] != "Bearer test-key" {
		t.Errorf("Authorization = %q", auths[0])
	}

	first := requests[0]
	if first["model"] != "test-model" {
		t.Errorf("model = %v, want the configured default", first["model"])
	}
	messages := first["messages"].([]any)
	system := messages[0].(map[string]any)
	user := messages[1].(map[string]any)
	if system["role"] != "system" || !strings.HasPrefix(system["content"].(string), "You answer briefly.") {
		t.Errorf("system message = %v", system)
	}
	if user["role"] != "user" || user["content"] != "jam berapa sekarang?" {
		t.Errorf("user message = %v", user)
	}
	if tools := first["tools"].([]any); len(tools) != 1 {
		t.Errorf("tools = %v, want current_time only", tools)
	}

	second := requests[1]["messages"].([]any)
	toolMsg := second[len(second)-1].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" || !strings.Contains(toolMsg["content"].(string), "(Asia/Jakarta)") {
		t.Errorf("tool result sent back = %v", toolMsg)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want the incoming and the outgoing message stored, got %d", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func realResponder(t *testing.T, llmURL string, st *store.Store) bot.Responder {
	t.Helper()
	cfg := config.Config{
		LLMBaseURL: llmURL,
		LLMAPIKey:  "test-key",
		LLMModel:   "test-model",
		AgentFile:  writeAgentFile(t, "---\nname: Bot\n---\nYou answer briefly."),
	}
	respond, _, err := newResponder(cfg, discardLog, st.Messages)
	if err != nil {
		t.Fatalf("newResponder: %v", err)
	}
	return respond
}

func startBot(t *testing.T, respond bot.Responder, st *store.Store, opts bot.Options) (*bot.Bot, context.Context) {
	t.Helper()
	b := bot.New(st.Messages, discardLog, respond, opts)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)
	t.Cleanup(func() {
		cancel()
		b.Wait()
	})
	return b, ctx
}

func waitForReply(t *testing.T, tr *recordingTransport) string {
	t.Helper()
	select {
	case reply := <-tr.replies:
		return reply
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a reply")
		return ""
	}
}

func TestBotSendsTheFallbackWhenTheLLMFails(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"model not found","code":400}}`)
	}))
	t.Cleanup(llm.Close)

	st := store.New(dbtest.New(t))
	client, err := st.Clients.Create(context.Background(), "Acme")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	b, ctx := startBot(t, realResponder(t, llm.URL, st), st, bot.Options{Workers: 1, Fallback: "Maaf, coba lagi ya."})

	transport := &recordingTransport{replies: make(chan string, 1)}
	b.Handle(ctx, transport, client.ID, bot.Message{ID: "IN1", Chat: "628@s.whatsapp.net", Text: "halo"})

	if reply := waitForReply(t, transport); reply != "Maaf, coba lagi ya." {
		t.Fatalf("reply = %q, want the fallback", reply)
	}
}

func TestBotAnswersAMessageThatWasWaitingBeforeARestart(t *testing.T) {
	var (
		mu   sync.Mutex
		body map[string]any
	)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		json.NewDecoder(r.Body).Decode(&body)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop",
			"message":{"role":"assistant","content":"Maaf lama menunggu, ada yang bisa dibantu?"}}]}`)
	}))
	t.Cleanup(llm.Close)

	st := store.New(dbtest.New(t))
	ctx := context.Background()
	client, err := st.Clients.Create(ctx, "Acme")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	// stored by the previous process, which stopped before replying
	_, err = st.Messages.Create(ctx, store.Message{
		ClientID: client.ID, WAID: "OLD1", Chat: "628@s.whatsapp.net", Direction: store.DirectionIn,
		Body: "apa kabar?", CreatedAt: time.Now().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	b, botCtx := startBot(t, realResponder(t, llm.URL, st), st, bot.Options{Workers: 1, MaxAge: 10 * time.Minute})
	pending, err := b.Unanswered(ctx, client.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("unanswered = %+v, %v", pending, err)
	}

	transport := &recordingTransport{replies: make(chan string, 1)}
	b.Answer(botCtx, transport, client.ID, pending)

	if reply := waitForReply(t, transport); reply != "Maaf lama menunggu, ada yang bisa dibantu?" {
		t.Fatalf("reply = %q", reply)
	}

	mu.Lock()
	defer mu.Unlock()
	messages := body["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	if last["role"] != "user" || last["content"] != "apa kabar?" {
		t.Errorf("the model must see the waiting message as the last user message, got %v", last)
	}
}

func TestNewResponder_AgentLabel(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config.Config
		agent string
		want  string
	}{
		{"no llm", config.Config{LLMBaseURL: "https://api.openai.com/v1"}, "", "Echo (no LLM)"},
		{"the built-in agent when no file is set", config.Config{LLMAPIKey: "k", LLMBaseURL: "http://localhost:1/v1", LLMModel: "gpt-4o-mini"}, "", "'built-in' | gpt-4o-mini"},
		{"default model", config.Config{LLMAPIKey: "k", LLMBaseURL: "http://localhost:1/v1", LLMModel: "gpt-4o-mini"}, "---\nname: Bot\n---\nHi", "'agent.md' | gpt-4o-mini"},
		{"the agent file overrides the model", config.Config{LLMAPIKey: "k", LLMBaseURL: "http://localhost:1/v1", LLMModel: "gpt-4o-mini"}, "---\nname: Bot\nmodel: llama3\n---\nHi", "'agent.md' | llama3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if tt.agent != "" {
				cfg.AgentFile = writeAgentFile(t, tt.agent)
			}

			_, label, err := newResponder(cfg, discardLog, nil)
			if err != nil {
				t.Fatalf("newResponder: %v", err)
			}
			if label != tt.want {
				t.Fatalf("label = %q, want %q", label, tt.want)
			}
		})
	}
}
