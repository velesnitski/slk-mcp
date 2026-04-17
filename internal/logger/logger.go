// Package logger configures structured logging for slk-mcp.
//
// All logs go to stderr (never stdout) to keep the stdio MCP transport
// clean. Format is JSON so downstream tools can parse it.
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON slog.Logger writing to stderr at the given level.
// Accepts "debug", "info", "warn", "error"; defaults to info.
func New(levelStr string) *slog.Logger {
	level := parseLevel(levelStr)
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:     level,
		AddSource: false,
	})
	return slog.New(handler)
}

// Setup installs the logger as the default slog logger.
func Setup(levelStr string) *slog.Logger {
	l := New(levelStr)
	slog.SetDefault(l)
	return l
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
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
