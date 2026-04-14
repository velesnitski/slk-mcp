package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
	"github.com/velesnitski/slk-mcp/internal/tools"
)

var version = "0.1.0"

func main() {
	transport := flag.String("transport", "stdio", "Transport: stdio, sse, streamable-http")
	host := flag.String("host", "0.0.0.0", "Host to bind to")
	port := flag.Int("port", 8000, "Port to bind to")
	flag.Parse()

	cfg := config.Load()
	if cfg.Token == "" {
		fmt.Fprintln(os.Stderr, "ERROR: SLACK_TOKEN is required (xoxb-... Bot User OAuth Token)")
		os.Exit(1)
	}

	client := slack.NewClient(cfg)

	s := server.NewMCPServer(
		"slack",
		version,
		server.WithToolCapabilities(true),
	)

	tools.RegisterAll(s, client, cfg)

	switch *transport {
	case "stdio":
		if err := server.ServeStdio(s); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	case "sse":
		addr := fmt.Sprintf("%s:%d", *host, *port)
		sseServer := server.NewSSEServer(s,
			server.WithBaseURL(fmt.Sprintf("http://%s", addr)),
			server.WithKeepAliveInterval(30*time.Second),
		)
		log.Printf("slk-mcp %s listening on %s (SSE)", version, addr)
		if err := sseServer.Start(addr); err != nil {
			log.Fatalf("SSE server error: %v", err)
		}
	case "streamable-http":
		addr := fmt.Sprintf("%s:%d", *host, *port)
		httpServer := server.NewStreamableHTTPServer(s)
		log.Printf("slk-mcp %s listening on %s (HTTP)", version, addr)
		if err := httpServer.Start(addr); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	default:
		log.Fatalf("Unknown transport: %s", *transport)
	}
}

func init() {
	// Suppress default mcp logging noise
	_ = mcp.NewTool
}
