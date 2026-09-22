package logger

import (
	"context"
	"log/slog"
	"testing"
)

func TestParseLevel_KnownNames(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestParseLevel_CaseAndSpaceInsensitive(t *testing.T) {
	// The level arrives from a flag default that reads an environment
	// variable, so stray case and whitespace are the normal case, not
	// the exception.
	for _, in := range []string{"DEBUG", " debug ", "Debug", "\tDEBUG\n"} {
		if got := parseLevel(in); got != slog.LevelDebug {
			t.Errorf("parseLevel(%q) = %v; want Debug", in, got)
		}
	}
}

func TestParseLevel_UnknownFallsBackToInfo(t *testing.T) {
	// An unrecognised level must not silence the server. Falling back
	// to Info keeps operational logs; falling back to Error would hide
	// them, and erroring out would make a typo fatal.
	for _, in := range []string{"", "verbose", "trace", "off", "5"} {
		if got := parseLevel(in); got != slog.LevelInfo {
			t.Errorf("parseLevel(%q) = %v; want Info", in, got)
		}
	}
}

func TestNew_HonoursLevel(t *testing.T) {
	debug := New("debug")
	if !debug.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug logger should have debug enabled")
	}

	warn := New("warn")
	if warn.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("warn logger should not have info enabled")
	}
	if !warn.Enabled(context.Background(), slog.LevelError) {
		t.Error("warn logger should have error enabled")
	}
}

func TestSetup_InstallsDefaultAndReturnsSameLevel(t *testing.T) {
	// Setup mutates process-global state; restore it so test order
	// cannot leak a level into another package's expectations.
	original := slog.Default()
	t.Cleanup(func() { slog.SetDefault(original) })

	l := Setup("error")
	if l == nil {
		t.Fatal("Setup returned nil")
	}
	if slog.Default().Enabled(context.Background(), slog.LevelWarn) {
		t.Error("default logger should be at error level after Setup(\"error\")")
	}
	if !slog.Default().Enabled(context.Background(), slog.LevelError) {
		t.Error("default logger should still log errors")
	}
}
