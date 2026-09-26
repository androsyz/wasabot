package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func fixedClock() *CurrentTime {
	instant := time.Date(2026, 9, 26, 13, 30, 5, 0, time.UTC)
	return &CurrentTime{now: func() time.Time { return instant }}
}

func TestCurrentTime_Run(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{"empty arguments default to UTC", "", "Saturday 2026-09-26 13:30:05 +00:00 (UTC)"},
		{"empty object", "{}", "Saturday 2026-09-26 13:30:05 +00:00 (UTC)"},
		{"jakarta", `{"timezone":"Asia/Jakarta"}`, "Saturday 2026-09-26 20:30:05 +07:00 (Asia/Jakarta)"},
		{"date rolls over in another zone", `{"timezone":"Pacific/Auckland"}`, "Sunday 2026-09-27 01:30:05 +12:00 (Pacific/Auckland)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fixedClock().Run(context.Background(), tt.args)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCurrentTime_Run_Errors(t *testing.T) {
	tests := []struct {
		name, args, wantErr string
	}{
		{"unknown timezone", `{"timezone":"Mars/Olympus"}`, `unknown timezone "Mars/Olympus"`},
		{"not json", `timezone=UTC`, "JSON object"},
		{"wrong type", `{"timezone": 5}`, "JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixedClock().Run(context.Background(), tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCurrentTime_Spec(t *testing.T) {
	spec := NewCurrentTime().Spec()

	if spec.Name != "current_time" || spec.Description == "" {
		t.Fatalf("spec = %+v", spec)
	}
	if spec.Parameters["type"] != "object" {
		t.Fatalf("parameters must be a JSON Schema object, got %v", spec.Parameters)
	}
}
