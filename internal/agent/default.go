package agent

import (
	_ "embed"
	"fmt"
)

//go:embed default_agent.md
var defaultAgent []byte

// DefaultDefinition is the agent used when no agent file is configured, so a bare binary works.
func DefaultDefinition() (Definition, error) {
	def, err := ParseDefinition(defaultAgent)
	if err != nil {
		return Definition{}, fmt.Errorf("built-in agent: %w", err)
	}
	return def, nil
}
