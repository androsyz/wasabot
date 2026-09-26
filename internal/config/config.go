package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"time"
)

const minSetupCodeLength = 8

const (
	defaultLLMBaseURL = "https://api.openai.com/v1"
	defaultLLMModel   = "gpt-4o-mini"
	defaultFallback   = "Sorry, I could not answer that right now. Please try again in a moment."
	defaultRateLimit  = "10"
	defaultMaxMsgAge  = "10m"
)

type Config struct {
	Addr      string
	DBPath    string
	LogLevel  slog.Level
	LogFormat string

	LLMBaseURL string
	LLMAPIKey  string // secret; may be empty for local servers
	LLMModel   string

	AgentFile string
	UIPreview bool // serve sample data for web pages that have no backend yet

	CookieSecure bool // mark cookies Secure even when TLS ends at a reverse proxy

	// First-run admin. SetupCode guards the setup page; AdminEmail and AdminPassword create the
	// admin at startup instead, for hosts where nobody can open a browser first or read the logs.
	SetupCode     string // secret
	AdminName     string
	AdminEmail    string
	AdminPassword string // secret

	FallbackReply string        // sent when the agent fails
	RateLimit     int           // messages answered per minute per chat; 0 disables
	MaxMessageAge time.Duration // older incoming messages are stored but not answered; 0 disables
}

func Load() (Config, error) {
	cfg := Config{
		Addr:       getenv("WASABOT_ADDR", "127.0.0.1:8080"),
		DBPath:     getenv("WASABOT_DB_PATH", "wasabot.db"),
		LogFormat:  getenv("WASABOT_LOG_FORMAT", "text"),
		LLMBaseURL: getenv("WASABOT_LLM_BASE_URL", defaultLLMBaseURL),
		LLMAPIKey:  getenv("WASABOT_LLM_API_KEY", ""),
		LLMModel:   getenv("WASABOT_LLM_MODEL", defaultLLMModel),
		AgentFile:  getenv("WASABOT_AGENT_FILE", ""), // empty means the built-in agent

		FallbackReply: getenv("WASABOT_FALLBACK_REPLY", defaultFallback),
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

	preview, err := strconv.ParseBool(getenv("WASABOT_UI_PREVIEW", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("WASABOT_UI_PREVIEW must be true or false")
	}
	cfg.UIPreview = preview

	secure, err := strconv.ParseBool(getenv("WASABOT_COOKIE_SECURE", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("WASABOT_COOKIE_SECURE must be true or false")
	}
	cfg.CookieSecure = secure

	cfg.SetupCode = getenv("WASABOT_SETUP_CODE", "")
	cfg.AdminName = getenv("WASABOT_ADMIN_NAME", "Admin")
	cfg.AdminEmail = getenv("WASABOT_ADMIN_EMAIL", "")
	cfg.AdminPassword = getenv("WASABOT_ADMIN_PASSWORD", "")
	if cfg.SetupCode != "" && len(cfg.SetupCode) < minSetupCodeLength {
		return Config{}, fmt.Errorf("WASABOT_SETUP_CODE must be at least %d characters", minSetupCodeLength)
	}
	if (cfg.AdminEmail == "") != (cfg.AdminPassword == "") {
		return Config{}, fmt.Errorf("WASABOT_ADMIN_EMAIL and WASABOT_ADMIN_PASSWORD must be set together")
	}

	limit, err := strconv.Atoi(getenv("WASABOT_RATE_LIMIT", defaultRateLimit))
	if err != nil || limit < 0 {
		return Config{}, fmt.Errorf("WASABOT_RATE_LIMIT must be a number of messages per minute, 0 or more")
	}
	cfg.RateLimit = limit

	maxAge, err := time.ParseDuration(getenv("WASABOT_MAX_MESSAGE_AGE", defaultMaxMsgAge))
	if err != nil || maxAge < 0 {
		return Config{}, fmt.Errorf("WASABOT_MAX_MESSAGE_AGE must be a duration such as 10m, 0 or more")
	}
	cfg.MaxMessageAge = maxAge
	return cfg, nil
}

// LLMEnabled is true when a key or a non-default base URL is set, so keyless local servers work.
func (c Config) LLMEnabled() bool {
	return c.LLMAPIKey != "" || c.LLMBaseURL != defaultLLMBaseURL
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
