// Command slk-mcp is an MCP server that exposes Slack to MCP-compatible
// clients (Claude Code, Cursor, GitHub Copilot, JetBrains).
//
// Transports: stdio (default), sse, streamable-http.
//
// Configuration is env-driven; see README.md for all variables.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/lifecycle"
	"github.com/velesnitski/slk-mcp/internal/logger"
	"github.com/velesnitski/slk-mcp/internal/slack"
	"github.com/velesnitski/slk-mcp/internal/tools"
)

// version is stamped at build time via -ldflags "-X main.version=x.y.z".
var version = "0.4.12"

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	transport := flag.String("transport", "stdio", "Transport: stdio, sse, streamable-http")
	host := flag.String("host", "0.0.0.0", "Host to bind HTTP transports to")
	port := flag.Int("port", 8000, "Port to bind HTTP transports to")
	logLevel := flag.String("log-level", envOr("SLACK_LOG_LEVEL", "info"), "Log level: debug, info, warn, error")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	log := logger.Setup(*logLevel)

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	client := slack.New(cfg, log)
	switch {
	case cfg.PostsAsUser():
		log.Info("token mode: user-only",
			"note", "posts and reactions will appear as the authenticated user")
	case !client.HasUserToken():
		log.Info("token mode: bot-only",
			"hint", "set SLACK_USER_TOKEN=xoxp-... to enable unread/mentions tools")
	default:
		log.Info("token mode: bot + user")
	}

	mcpServer := server.NewMCPServer(
		"slack",
		version,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)
	tools.NewHub(client, cfg, log).RegisterAll(mcpServer)

	log.Info("slk-mcp ready",
		"version", version,
		"transport", *transport,
		"bot_token", cfg.HasBotToken(),
		"user_token", client.HasUserToken(),
		"read_only", cfg.ReadOnly,
		"channels_configured", len(cfg.Channels),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch *transport {
	case "stdio":
		return runStdio(ctx, mcpServer, log)
	case "sse":
		return runSSE(ctx, mcpServer, log, fmt.Sprintf("%s:%d", *host, *port))
	case "streamable-http":
		return runStreamableHTTP(ctx, mcpServer, log, fmt.Sprintf("%s:%d", *host, *port))
	default:
		return fmt.Errorf("unknown transport: %s", *transport)
	}
}

// runStdio runs the stdio transport. ServeStdio is blocking and the
// upstream library exits on stdin EOF, but some MCP hosts disconnect
// without closing the pipe — in that case the kernel reparents us to
// PID 1 / launchd, and the parent watcher trips and we exit on its
// behalf.
func runStdio(ctx context.Context, s *server.MCPServer, log *slog.Logger) error {
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	go lifecycle.WatchParent(
		watchCtx,
		log,
		os.Getppid,
		lifecycle.DefaultParentPollInterval,
		func() {
			// We can't unblock server.ServeStdio cleanly, but exiting
			// the process is correct: the host is gone.
			log.Info("stdio transport: parent process gone, exiting")
			os.Exit(0)
		},
	)

	done := make(chan error, 1)
	go func() {
		done <- server.ServeStdio(s)
	}()
	select {
	case <-ctx.Done():
		log.Info("stdio transport: signal received, exiting")
		return nil
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	}
}

func runSSE(ctx context.Context, s *server.MCPServer, log *slog.Logger, addr string) error {
	sseServer := server.NewSSEServer(s,
		server.WithBaseURL("http://"+addr),
		server.WithKeepAliveInterval(30*time.Second),
	)
	log.Info("listening", "addr", addr, "transport", "sse")

	errCh := make(chan error, 1)
	go func() {
		errCh <- sseServer.Start(addr)
	}()

	select {
	case <-ctx.Done():
		log.Info("sse transport: signal received, shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return sseServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("sse server: %w", err)
		}
		return nil
	}
}

func runStreamableHTTP(ctx context.Context, s *server.MCPServer, log *slog.Logger, addr string) error {
	httpServer := server.NewStreamableHTTPServer(s)
	log.Info("listening", "addr", addr, "transport", "streamable-http")

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Start(addr)
	}()

	select {
	case <-ctx.Done():
		log.Info("http transport: signal received, shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
