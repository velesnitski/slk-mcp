package logger

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

// ADR 122: the opt-in log file.

func TestSetupWithFile_WritesToTheFileWithPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "slk.log")
	l := SetupWithFile("info", path)
	l.Info("sweep phase", "phase", "unread")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), `"phase":"unread"`) {
		t.Fatalf("file content: %s", raw)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600 — logs name people and channels", st.Mode().Perm())
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", dir.Mode().Perm())
	}
}

func TestSetupWithFile_AppendsAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slk.log")
	SetupWithFile("info", path).Info("first")
	SetupWithFile("info", path).Info("second")
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "first") || !strings.Contains(string(raw), "second") {
		t.Fatalf("a restart must append, not truncate: %s", raw)
	}
}

func TestSetupWithFile_RotatesAnOversizedFileOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "slk.log")
	if err := os.WriteFile(path, make([]byte, maxLogBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	SetupWithFile("info", path).Info("fresh")

	old, err := os.Stat(path + ".1")
	if err != nil || old.Size() != maxLogBytes+1 {
		t.Fatalf("the oversized file must move to .1: %v", err)
	}
	cur, _ := os.ReadFile(path)
	if len(cur) > 1024 || !strings.Contains(string(cur), "fresh") {
		t.Fatalf("the live file must start fresh, got %d bytes", len(cur))
	}
}

func TestSetupWithFile_ExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	SetupWithFile("info", "~/.cache/slk/slk.log").Info("x")
	if _, err := os.Stat(filepath.Join(home, ".cache", "slk", "slk.log")); err != nil {
		t.Fatalf("~ not expanded: %v", err)
	}
}

func TestSetupWithFile_AnUnopenableFileStillStartsTheServer(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A path under a regular file cannot be created.
	if l := SetupWithFile("info", filepath.Join(blocker, "x.log")); l == nil {
		t.Fatal("a logger is always returned")
	}
}

func TestSetupWithFile_EmptyPathIsStderrOnly(t *testing.T) {
	if l := SetupWithFile("info", "  "); l == nil {
		t.Fatal("nil logger")
	}
}
