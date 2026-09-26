package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

var envKeys = []string{
	"WASABOT_ADDR", "WASABOT_DB_PATH", "WASABOT_LOG_LEVEL", "WASABOT_LOG_FORMAT",
	"WASABOT_LLM_BASE_URL", "WASABOT_LLM_API_KEY", "WASABOT_LLM_MODEL", "WASABOT_AGENT_FILE",
	"WASABOT_FALLBACK_REPLY", "WASABOT_RATE_LIMIT", "WASABOT_MAX_MESSAGE_AGE", "WASABOT_UI_PREVIEW", "WASABOT_COOKIE_SECURE",
	"WASABOT_SETUP_CODE", "WASABOT_ADMIN_NAME", "WASABOT_ADMIN_EMAIL", "WASABOT_ADMIN_PASSWORD",
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
		Addr:       "127.0.0.1:8080",
		DBPath:     "wasabot.db",
		LogLevel:   slog.LevelInfo,
		LogFormat:  "text",
		LLMBaseURL: "https://api.openai.com/v1",
		LLMModel:   "gpt-4o-mini",
		AgentFile:  "",

		FallbackReply: "Sorry, I could not answer that right now. Please try again in a moment.",
		RateLimit:     10,
		MaxMessageAge: 10 * time.Minute,
		AdminName:     "Admin",
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
		"WASABOT_AGENT_FILE":   "/etc/wasabot/support.md",

		"WASABOT_FALLBACK_REPLY":  "Maaf, coba lagi ya.",
		"WASABOT_RATE_LIMIT":      "3",
		"WASABOT_MAX_MESSAGE_AGE": "90s",
		"WASABOT_UI_PREVIEW":      "true",
		"WASABOT_COOKIE_SECURE":   "1",
		"WASABOT_SETUP_CODE":      "my-secret-code",
		"WASABOT_ADMIN_NAME":      "Rina",
		"WASABOT_ADMIN_EMAIL":     "rina@example.com",
		"WASABOT_ADMIN_PASSWORD":  "correct horse battery",
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
		AgentFile:  "/etc/wasabot/support.md",

		FallbackReply: "Maaf, coba lagi ya.",
		RateLimit:     3,
		MaxMessageAge: 90 * time.Second,
		UIPreview:     true,
		CookieSecure:  true,

		SetupCode:     "my-secret-code",
		AdminName:     "Rina",
		AdminEmail:    "rina@example.com",
		AdminPassword: "correct horse battery",
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
		{"setup code too short", map[string]string{"WASABOT_SETUP_CODE": "1234567"}, "WASABOT_SETUP_CODE"},
		{"admin email without a password", map[string]string{"WASABOT_ADMIN_EMAIL": "a@example.com"}, "set together"},
		{"admin password without an email", map[string]string{"WASABOT_ADMIN_PASSWORD": "correct horse battery"}, "set together"},
		{"cookie flag not a bool", map[string]string{"WASABOT_COOKIE_SECURE": "sure"}, "WASABOT_COOKIE_SECURE"},
		{"preview flag not a bool", map[string]string{"WASABOT_UI_PREVIEW": "maybe"}, "WASABOT_UI_PREVIEW"},
		{"rate limit not a number", map[string]string{"WASABOT_RATE_LIMIT": "often"}, "WASABOT_RATE_LIMIT"},
		{"rate limit negative", map[string]string{"WASABOT_RATE_LIMIT": "-1"}, "WASABOT_RATE_LIMIT"},
		{"max age not a duration", map[string]string{"WASABOT_MAX_MESSAGE_AGE": "10 minutes"}, "WASABOT_MAX_MESSAGE_AGE"},
		{"max age negative", map[string]string{"WASABOT_MAX_MESSAGE_AGE": "-5m"}, "WASABOT_MAX_MESSAGE_AGE"},
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

func TestConfig_LLMEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"nothing set", nil, false},
		{"api key", map[string]string{"WASABOT_LLM_API_KEY": "k"}, true},
		{"custom base url without a key (local server)", map[string]string{"WASABOT_LLM_BASE_URL": "http://localhost:11434/v1"}, true},
		{"only a model", map[string]string{"WASABOT_LLM_MODEL": "gpt-4o"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setenv(t, tt.env)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got := cfg.LLMEnabled(); got != tt.want {
				t.Fatalf("LLMEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoad_ZeroDisablesRateLimitAndAgeLimit(t *testing.T) {
	setenv(t, map[string]string{"WASABOT_RATE_LIMIT": "0", "WASABOT_MAX_MESSAGE_AGE": "0"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RateLimit != 0 || cfg.MaxMessageAge != 0 {
		t.Fatalf("got rate limit %d and max age %v, want both 0 (disabled)", cfg.RateLimit, cfg.MaxMessageAge)
	}
}

func TestLoad_SecretsAreNotPartOfAnyError(t *testing.T) {
	setenv(t, map[string]string{
		"WASABOT_SETUP_CODE":     "short",
		"WASABOT_ADMIN_PASSWORD": "super-secret-password",
	})

	_, err := Load()

	if err == nil {
		t.Fatal("want an error")
	}
	for _, secret := range []string{"short", "super-secret-password"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("the error leaks a secret: %v", err)
		}
	}
}
