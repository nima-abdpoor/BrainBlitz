// Package logger constructs the structured logger used throughout the bot.
//
// Unlike the backend services' pkg/logger (a global slog singleton
// initialized once via a package-level sync.Once), this constructor returns
// a *slog.Logger instance for the caller to inject wherever it's needed.
// The bot has a single entry point and no compelling reason for global
// mutable state, and a plain instance is trivial to substitute with a
// discard logger or a buffer-backed one in tests.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New builds a structured JSON logger writing to stdout. JSON-to-stdout
// (rather than the backend's file-plus-rotation approach) matches how
// containerized processes are expected to log: the runtime captures stdout,
// so the bot doesn't need to own log file rotation itself.
func New(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(level),
	}))
}

// parseLevel maps a config string to a slog.Level, defaulting to Info for
// an empty or unrecognized value so a missing config key doesn't panic or
// silently suppress every log line.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
