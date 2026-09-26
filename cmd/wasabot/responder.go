package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/androsyz/wasabot/internal/agent"
	"github.com/androsyz/wasabot/internal/bot"
	"github.com/androsyz/wasabot/internal/config"
	"github.com/androsyz/wasabot/internal/store"
	"github.com/androsyz/wasabot/internal/tools"
)

const historyLimit = 20

// newResponder also returns a short description of the agent for the dashboard.
func newResponder(cfg config.Config, log *slog.Logger, messages *store.Messages) (bot.Responder, string, error) {
	if !cfg.LLMEnabled() {
		log.Warn("no LLM configured, replying with an echo; set WASABOT_LLM_API_KEY, or WASABOT_LLM_BASE_URL for a local server")
		return echo, "Echo (no LLM)", nil
	}

	def, source, err := loadAgent(cfg.AgentFile)
	if err != nil {
		return nil, "", err
	}
	ag, err := agent.New(agent.Options{
		LLM:          agent.NewClient(cfg.LLMAPIKey, cfg.LLMBaseURL),
		Definition:   def,
		DefaultModel: cfg.LLMModel,
		Tools:        availableTools(),
		Log:          log,
	})
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", describeAgent(cfg.AgentFile), err)
	}

	log.Info("LLM agent enabled", "agent", def.Name, "tools", def.Tools)
	model := cmp.Or(def.Model, cfg.LLMModel)
	return bot.NewAgentResponder(ag, messages, historyLimit), fmt.Sprintf("'%s' | %s", source, model), nil
}

// availableTools is every tool wasabot provides; an agent file selects some of them by name.
func availableTools() []agent.Tool {
	return []agent.Tool{tools.NewCurrentTime()}
}

// loadAgent reads the agent file, or returns the agent built into the binary when path is empty.
// The second result names where it came from, for the dashboard.
func loadAgent(path string) (agent.Definition, string, error) {
	if path == "" {
		def, err := agent.DefaultDefinition()
		return def, "built-in", err
	}
	def, err := agent.LoadDefinition(path)
	return def, filepath.Base(path), err
}

func describeAgent(path string) string {
	if path == "" {
		return "built-in agent"
	}
	return "agent file " + path
}

func echo(_ context.Context, _ int64, msg bot.Message) (string, error) {
	return "you said: " + msg.Text, nil
}
