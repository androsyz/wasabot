package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // timezone names work even where the host has no tz database

	"github.com/androsyz/wasabot/internal/agent"
)

type CurrentTime struct {
	now func() time.Time
}

func NewCurrentTime() *CurrentTime {
	return &CurrentTime{now: time.Now}
}

func (*CurrentTime) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name:        "current_time",
		Description: "Returns the current date and time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timezone": map[string]any{
					"type":        "string",
					"description": "IANA timezone name such as Asia/Jakarta. Defaults to UTC.",
				},
			},
		},
	}
}

func (t *CurrentTime) Run(_ context.Context, arguments string) (string, error) {
	var args struct {
		Timezone string `json:"timezone"`
	}
	if strings.TrimSpace(arguments) != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("arguments must be a JSON object such as {\"timezone\": \"Asia/Jakarta\"}: %w", err)
		}
	}

	loc := time.UTC
	if args.Timezone != "" {
		var err error
		if loc, err = time.LoadLocation(args.Timezone); err != nil {
			return "", fmt.Errorf("unknown timezone %q; use an IANA name such as Asia/Jakarta", args.Timezone)
		}
	}
	return fmt.Sprintf("%s (%s)", t.now().In(loc).Format("Monday 2006-01-02 15:04:05 -07:00"), loc), nil
}
