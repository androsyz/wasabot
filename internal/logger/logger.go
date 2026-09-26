package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"

	waLog "go.mau.fi/whatsmeow/util/log"
)

func New(level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h)
}

// a JID user part (a phone number or LID) followed by ":device" or "@server"
var jidUser = regexp.MustCompile(`\b(\d{2})\d{4,}(\d{3})([:@])`)

// MaskJIDs hides the middle of phone numbers and LIDs inside JIDs, e.g. 6281234567890@s.whatsapp.net
// becomes 62****890@s.whatsapp.net.
func MaskJIDs(s string) string {
	return jidUser.ReplaceAllString(s, "${1}****${2}${3}")
}

// WhatsApp adapts log to whatsmeow's printf-style logger and masks JIDs, since whatsmeow logs
// phone numbers even at info level. Sub-loggers become a "module" attribute.
func WhatsApp(log *slog.Logger) waLog.Logger {
	return waAdapter{log: log}
}

type waAdapter struct {
	log *slog.Logger
}

func (a waAdapter) Debugf(msg string, args ...any) { a.logf(slog.LevelDebug, msg, args) }
func (a waAdapter) Infof(msg string, args ...any)  { a.logf(slog.LevelInfo, msg, args) }
func (a waAdapter) Warnf(msg string, args ...any)  { a.logf(slog.LevelWarn, msg, args) }
func (a waAdapter) Errorf(msg string, args ...any) { a.logf(slog.LevelError, msg, args) }

func (a waAdapter) Sub(module string) waLog.Logger {
	return waAdapter{log: a.log.With("module", module)}
}

// whatsmeow calls Debugf on hot paths, so skip formatting when the level is off.
func (a waAdapter) logf(level slog.Level, msg string, args []any) {
	ctx := context.Background()
	if !a.log.Enabled(ctx, level) {
		return
	}
	a.log.Log(ctx, level, MaskJIDs(fmt.Sprintf(msg, args...)))
}
