package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func newTestLogger(buf *bytes.Buffer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level}))
}

func TestWhatsApp_FormatsMessage(t *testing.T) {
	var buf bytes.Buffer
	wa := WhatsApp(newTestLogger(&buf, slog.LevelInfo))

	wa.Infof("connected to %s in %d ms", "server", 42)

	out := buf.String()
	if !strings.Contains(out, "level=INFO") || !strings.Contains(out, `msg="connected to server in 42 ms"`) {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestWhatsApp_SubAddsModule(t *testing.T) {
	var buf bytes.Buffer
	wa := WhatsApp(newTestLogger(&buf, slog.LevelInfo))

	wa.Sub("Database").Warnf("slow query")

	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "module=Database") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestMaskJIDs(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"Successfully paired 6281234567890:38@s.whatsapp.net", "Successfully paired 62****890:38@s.whatsapp.net"},
		{"Unavailable message ABC from 218528607151@lid (type: x)", "Unavailable message ABC from 21****151@lid (type: x)"},
		{"from 6281234567890@s.whatsapp.net to 6289876543210@s.whatsapp.net", "from 62****890@s.whatsapp.net to 62****210@s.whatsapp.net"},
		{"Uploading 812 new prekeys", "Uploading 812 new prekeys"},
		{"Stored 3675 message secret keys", "Stored 3675 message secret keys"},
		{"id AC233625E9F0051538C93B058ED4BC07 ok", "id AC233625E9F0051538C93B058ED4BC07 ok"},
	}
	for _, tt := range tests {
		if got := MaskJIDs(tt.in); got != tt.want {
			t.Errorf("MaskJIDs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWhatsApp_MasksJIDs(t *testing.T) {
	var buf bytes.Buffer
	wa := WhatsApp(newTestLogger(&buf, slog.LevelInfo))

	wa.Infof("Successfully paired %s", "6281234567890:38@s.whatsapp.net")

	out := buf.String()
	if strings.Contains(out, "6281234567890") || !strings.Contains(out, "62****890") {
		t.Fatalf("phone number must be masked, got %q", out)
	}
}

func TestWhatsApp_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	wa := WhatsApp(newTestLogger(&buf, slog.LevelInfo))

	wa.Debugf("hidden %d", 1)

	if buf.Len() != 0 {
		t.Fatalf("debug should be filtered at info level, got %q", buf.String())
	}
}
