package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const textCompletion = `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[
	{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"halo!"}}]}`

const toolCompletion = `{"id":"c2","object":"chat.completion","created":1,"model":"m","choices":[
	{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[
		{"id":"call_1","type":"function","function":{"name":"current_time","arguments":"{\"tz\":\"UTC\"}"}}]}}]}`

type captured struct {
	path   string
	header http.Header
	body   map[string]any
}

func fakeLLMServer(t *testing.T, status int, response string) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.path = r.URL.Path
		got.header = r.Header.Clone()
		if err := json.Unmarshal(raw, &got.body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestClient_Complete_Text(t *testing.T) {
	srv, got := fakeLLMServer(t, http.StatusOK, textCompletion)
	llm := NewClient("test-key", srv.URL)

	msg, err := llm.Complete(context.Background(), Request{
		Model: "gpt-4o-mini",
		Messages: []Message{
			{Role: RoleSystem, Content: "be brief"},
			{Role: RoleUser, Content: "halo"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if msg.Role != RoleAssistant || msg.Content != "halo!" || len(msg.ToolCalls) != 0 {
		t.Fatalf("got %+v", msg)
	}

	if got.path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", got.path)
	}
	if auth := got.header.Get("Authorization"); auth != "Bearer test-key" {
		t.Errorf("Authorization = %q", auth)
	}
	if got.body["model"] != "gpt-4o-mini" {
		t.Errorf("model = %v", got.body["model"])
	}
	messages := got.body["messages"].([]any)
	first := messages[0].(map[string]any)
	if len(messages) != 2 || first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("messages = %v", messages)
	}
	if _, ok := got.body["tools"]; ok {
		t.Errorf("no tools were requested, body has %v", got.body["tools"])
	}
}

func TestClient_Complete_ToolCallsAndToolSpecs(t *testing.T) {
	srv, got := fakeLLMServer(t, http.StatusOK, toolCompletion)
	llm := NewClient("test-key", srv.URL)

	msg, err := llm.Complete(context.Background(), Request{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: "what time is it?"}},
		Tools: []ToolSpec{{
			Name:        "current_time",
			Description: "Returns the current time",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"tz": map[string]any{"type": "string"}}},
		}},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	want := ToolCall{ID: "call_1", Name: "current_time", Arguments: `{"tz":"UTC"}`}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0] != want {
		t.Fatalf("tool calls = %+v, want [%+v]", msg.ToolCalls, want)
	}

	tools := got.body["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || fn["name"] != "current_time" || fn["description"] != "Returns the current time" {
		t.Errorf("tools = %v", tools)
	}
	if fn["parameters"].(map[string]any)["type"] != "object" {
		t.Errorf("parameters = %v", fn["parameters"])
	}
}

func TestClient_Complete_SendsToolResultsBack(t *testing.T) {
	srv, got := fakeLLMServer(t, http.StatusOK, textCompletion)
	llm := NewClient("test-key", srv.URL)

	_, err := llm.Complete(context.Background(), Request{
		Model: "m",
		Messages: []Message{
			{Role: RoleUser, Content: "what time is it?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "current_time", Arguments: "{}"}}},
			{Role: RoleTool, ToolCallID: "call_1", Content: "12:00"},
		},
	})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}

	messages := got.body["messages"].([]any)
	assistant := messages[1].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	call := calls[0].(map[string]any)
	if assistant["role"] != "assistant" || call["id"] != "call_1" || call["function"].(map[string]any)["name"] != "current_time" {
		t.Errorf("assistant message = %v", assistant)
	}
	tool := messages[2].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "call_1" || tool["content"] != "12:00" {
		t.Errorf("tool message = %v", tool)
	}
}

func TestClient_Complete_Errors(t *testing.T) {
	t.Run("http error does not leak the api key", func(t *testing.T) {
		srv, _ := fakeLLMServer(t, http.StatusUnauthorized, `{"error":{"message":"invalid api key","code":401}}`)
		llm := NewClient("sk-very-secret", srv.URL)

		_, err := llm.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		if err == nil {
			t.Fatal("want an error")
		}
		if strings.Contains(err.Error(), "sk-very-secret") {
			t.Fatalf("error leaks the api key: %v", err)
		}
	})

	t.Run("no choices", func(t *testing.T) {
		srv, _ := fakeLLMServer(t, http.StatusOK, `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[]}`)
		llm := NewClient("k", srv.URL)

		_, err := llm.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		if err == nil || !strings.Contains(err.Error(), "no choices") {
			t.Fatalf("got %v, want a no-choices error", err)
		}
	})

	t.Run("unsupported role", func(t *testing.T) {
		llm := NewClient("k", "http://127.0.0.1:1")

		_, err := llm.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: "wizard", Content: "hi"}}})
		if err == nil || !strings.Contains(err.Error(), "unsupported message role") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestClient_Complete_WithoutAPIKeyAndTrailingSlash(t *testing.T) {
	srv, got := fakeLLMServer(t, http.StatusOK, textCompletion)
	llm := NewClient("", srv.URL+"/")

	if _, err := llm.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("complete: %v", err)
	}

	if got.path != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", got.path)
	}
	if auth := got.header.Get("Authorization"); auth != "" {
		t.Errorf("no api key was configured, but Authorization = %q", auth)
	}
}

func TestClient_IgnoresOpenAIEnvironment(t *testing.T) {
	srv, got := fakeLLMServer(t, http.StatusOK, textCompletion)
	t.Setenv("OPENAI_API_KEY", "leak-api-key")
	t.Setenv("OPENAI_ADMIN_KEY", "leak-admin-key")
	t.Setenv("OPENAI_ORG_ID", "leak-org")
	t.Setenv("OPENAI_PROJECT_ID", "leak-project")
	t.Setenv("OPENAI_CUSTOM_HEADERS", "X-Leak: leak-header")
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1/")

	llm := NewClient("test-key", srv.URL)
	if _, err := llm.Complete(context.Background(), Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("the request must reach the configured base url: %v", err)
	}

	for name, values := range got.header {
		for _, v := range values {
			if strings.Contains(v, "leak") {
				t.Errorf("header %s carries an OPENAI_* value: %q", name, v)
			}
		}
	}
	if got.header.Get("Authorization") != "Bearer test-key" {
		t.Errorf("Authorization = %q", got.header.Get("Authorization"))
	}
}
