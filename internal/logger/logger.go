// Package logger configures structured logging for slk-mcp.
//
// All logs go to stderr (never stdout) to keep the stdio MCP transport
// clean. Format is JSON so downstream tools can parse it.
package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// New returns a JSON slog.Logger writing to stderr at the given level.
// Accepts "debug", "info", "warn", "error"; defaults to info.
func New(levelStr string) *slog.Logger {
	return newTo(os.Stderr, levelStr)
}

func newTo(w io.Writer, levelStr string) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     parseLevel(levelStr),
		AddSource: false,
	})
	return slog.New(handler)
}

// maxLogBytes is the size past which the log file is rotated to "<path>.1"
// when the server starts. One generation is kept: the file exists to
// answer "why was the last sweep slow", not to be an archive.
const maxLogBytes = 10 << 20

// SetupWithFile installs a logger that writes to stderr and, when path is
// non-empty, also appends to that file. ADR 122.
//
// An MCP host keeps a server's stderr out of sight (Claude Code does not
// persist it), so per-phase timings logged there could not be read back.
// The file is opt-in (SLACK_LOG_FILE), created 0600 in a 0700 directory —
// logs name channels and people — and rotated once at startup. If the file
// cannot be opened the server still starts, logging to stderr only, and
// says why.
func SetupWithFile(levelStr, path string) *slog.Logger {
	if strings.TrimSpace(path) == "" {
		return Setup(levelStr)
	}
	f, err := openLogFile(path)
	if err != nil {
		l := Setup(levelStr)
		l.Warn("log file not opened; logging to stderr only", "path", path, "err", err)
		return l
	}
	l := newTo(io.MultiWriter(os.Stderr, f), levelStr)
	slog.SetDefault(l)
	return l
}

// openLogFile expands a leading "~/", creates the directory, rotates an
// oversized file, and opens it for appending.
func openLogFile(path string) (*os.File, error) {
	path = strings.TrimSpace(path)
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve ~: %w", err)
		}
		path = filepath.Join(home, rest)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogBytes {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("rotate: %w", err)
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
