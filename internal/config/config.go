package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
)

const (
	defaultLLMBaseURL = "https://api.openai.com/v1"
	defaultLLMModel   = "gpt-4o-mini"
)

type Config struct {
	Addr      string
	DBPath    string
	LogLevel  slog.Level
	LogFormat string

	LLMBaseURL string
	LLMAPIKey  string // secret; may be empty for local servers
	LLMModel   string
}

func Load() (Config, error) {
	cfg := Config{
		Addr:       getenv("WASABOT_ADDR", ":8080"),
		DBPath:     getenv("WASABOT_DB_PATH", "wasabot.db"),
		LogFormat:  getenv("WASABOT_LOG_FORMAT", "text"),
		LLMBaseURL: getenv("WASABOT_LLM_BASE_URL", defaultLLMBaseURL),
		LLMAPIKey:  getenv("WASABOT_LLM_API_KEY", ""),
		LLMModel:   getenv("WASABOT_LLM_MODEL", defaultLLMModel),
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(getenv("WASABOT_LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("WASABOT_LOG_LEVEL: %w", err)
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		return Config{}, fmt.Errorf("WASABOT_LOG_FORMAT must be text or json, got %q", cfg.LogFormat)
	}
	if u, err := url.Parse(cfg.LLMBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("WASABOT_LLM_BASE_URL must be an http(s) URL, got %q", cfg.LLMBaseURL)
	}
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
