package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

func mnLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mnServer() *server.MCPServer {
	return server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
}

func TestEnvOr_PrefersEnvironment(t *testing.T) {
	t.Setenv("SLK_TEST_ENVOR", "from-env")
	if got := envOr("SLK_TEST_ENVOR", "fallback"); got != "from-env" {
		t.Fatalf("got %q; want from-env", got)
	}
}

func TestEnvOr_FallsBackWhenUnset(t *testing.T) {
	if got := envOr("SLK_TEST_ENVOR_UNSET", "fallback"); got != "fallback" {
		t.Fatalf("got %q; want fallback", got)
	}
}

func TestEnvOr_FallsBackWhenEmpty(t *testing.T) {
	// An exported-but-empty variable is indistinguishable from unset
	// for configuration purposes: an empty log level is not a level.
	t.Setenv("SLK_TEST_ENVOR_EMPTY", "")
	if got := envOr("SLK_TEST_ENVOR_EMPTY", "fallback"); got != "fallback" {
		t.Fatalf("got %q; want fallback", got)
	}
}

// Both HTTP transports must return cleanly when the process receives a
// signal — the caller treats a non-nil error as a fatal exit, so a
// graceful shutdown reported as an error would make every Ctrl-C look
// like a crash.

func TestRunSSE_ShutsDownCleanlyOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runSSE(ctx, mnServer(), mnLog(), "127.0.0.1:0") }()

	// Give Start a moment to bind before cancelling, so the shutdown
	// path runs against a live listener rather than racing the bind.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown reported an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runSSE did not return after context cancel")
	}
}

func TestRunStreamableHTTP_ShutsDownCleanlyOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runStreamableHTTP(ctx, mnServer(), mnLog(), "127.0.0.1:0") }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("graceful shutdown reported an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runStreamableHTTP did not return after context cancel")
	}
}

func TestRunSSE_ReturnsErrorOnUnusableAddress(t *testing.T) {
	// A bind failure must surface. Port 1 is privileged and the host
	// portion is invalid, so Start fails immediately rather than
	// blocking — and the error must reach the caller, not be swallowed
	// into a clean exit that looks like a normal shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runSSE(ctx, mnServer(), mnLog(), "256.256.256.256:1") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a bind error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runSSE did not report the bind failure")
	}
}

func TestRunStreamableHTTP_ReturnsErrorOnUnusableAddress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runStreamableHTTP(ctx, mnServer(), mnLog(), "256.256.256.256:1") }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a bind error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runStreamableHTTP did not report the bind failure")
	}
}

func TestRunStdio_ReturnsOnContextCancel(t *testing.T) {
	// ServeStdio cannot be unblocked, so the ctx branch is the only
	// clean exit; it must not be reported as an error.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runStdio(ctx, mnServer(), mnLog()) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("signal exit reported an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runStdio did not return after context cancel")
	}
}
