package config

import (
	"log/slog"
	"strings"
	"testing"
)

var envKeys = []string{
	"WASABOT_ADDR", "WASABOT_DB_PATH", "WASABOT_LOG_LEVEL", "WASABOT_LOG_FORMAT",
	"WASABOT_LLM_BASE_URL", "WASABOT_LLM_API_KEY", "WASABOT_LLM_MODEL",
}

// setenv starts from a clean slate so the developer's real environment cannot change the result.
func setenv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoad_Defaults(t *testing.T) {
	setenv(t, nil)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := Config{
		Addr:       ":8080",
		DBPath:     "wasabot.db",
		LogLevel:   slog.LevelInfo,
		LogFormat:  "text",
		LLMBaseURL: "https://api.openai.com/v1",
		LLMModel:   "gpt-4o-mini",
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoad_Overrides(t *testing.T) {
	setenv(t, map[string]string{
		"WASABOT_ADDR":         ":9000",
		"WASABOT_DB_PATH":      "/data/bot.db",
		"WASABOT_LOG_LEVEL":    "debug",
		"WASABOT_LOG_FORMAT":   "json",
		"WASABOT_LLM_BASE_URL": "http://localhost:11434/v1",
		"WASABOT_LLM_API_KEY":  "secret",
		"WASABOT_LLM_MODEL":    "llama3.1",
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	want := Config{
		Addr:       ":9000",
		DBPath:     "/data/bot.db",
		LogLevel:   slog.LevelDebug,
		LogFormat:  "json",
		LLMBaseURL: "http://localhost:11434/v1",
		LLMAPIKey:  "secret",
		LLMModel:   "llama3.1",
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"log level", map[string]string{"WASABOT_LOG_LEVEL": "loud"}, "WASABOT_LOG_LEVEL"},
		{"log format", map[string]string{"WASABOT_LOG_FORMAT": "xml"}, "WASABOT_LOG_FORMAT"},
		{"base url without scheme", map[string]string{"WASABOT_LLM_BASE_URL": "api.example.com/v1"}, "WASABOT_LLM_BASE_URL"},
		{"base url with wrong scheme", map[string]string{"WASABOT_LLM_BASE_URL": "ftp://example.com"}, "WASABOT_LLM_BASE_URL"},
		{"base url without host", map[string]string{"WASABOT_LLM_BASE_URL": "https://"}, "WASABOT_LLM_BASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setenv(t, tt.env)

			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error mentioning %s", err, tt.wantErr)
			}
		})
	}
}

func TestLoad_InvalidURLErrorDoesNotLeakTheAPIKey(t *testing.T) {
	setenv(t, map[string]string{
		"WASABOT_LLM_BASE_URL": "not-a-url",
		"WASABOT_LLM_API_KEY":  "sk-very-secret",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "sk-very-secret") {
		t.Fatalf("error leaks the api key: %v", err)
	}
}
