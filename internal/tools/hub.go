// Package tools wires MCP tool handlers to the Slack service layer.
//
// The package exports a single entry-point — Hub — which owns the
// shared dependencies (Slack client, config, structured logger) and
// hangs every register* method off itself. This replaces an earlier
// service-locator pattern (`Deps` struct threaded through free
// functions) and gives every handler a uniform place to attach
// cross-cutting middleware (timing, panic recovery, structured
// logging) via wrap().
package tools

import (
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// Hub bundles the dependencies every tool handler reads from.
// Construct with NewHub; consumers should treat it as immutable once
// returned.
type Hub struct {
	client *slack.Client
	cfg    *config.Config
	log    *slog.Logger
}

// NewHub builds a Hub from already-constructed dependencies. main.go
// owns the lifecycle of those — Hub never closes the client or the
// logger.
func NewHub(client *slack.Client, cfg *config.Config, log *slog.Logger) *Hub {
	return &Hub{client: client, cfg: cfg, log: log}
}

// Client / Config / Log expose dependencies for the very rare caller
// that needs them outside the registration flow (currently: nothing —
// the accessors exist for future telemetry / test helpers without
// forcing fields back to exported).
func (h *Hub) Client() *slack.Client  { return h.client }
func (h *Hub) Config() *config.Config { return h.cfg }
func (h *Hub) Log() *slog.Logger      { return h.log }

// Users / Channels / Messages / Search / Unread surface the
// narrow service contracts from contracts.go.
//
// Today they return the concrete *slack.XService directly — the
// interface return type is the seam. New handler code SHOULD call
// these accessors instead of reaching into h.client.X so that future
// tests can substitute fakes (compose a wrapper Hub type that
// overrides the accessor) without touching the call site.
//
// Existing handlers that use h.client.X.Method() continue to work;
// migrating them is incremental, not blocking.
func (h *Hub) Users() UserClient       { return h.client.Users }
func (h *Hub) Channels() ChannelClient { return h.client.Channels }
func (h *Hub) Messages() MessageClient { return h.client.Messages }
func (h *Hub) Search() SearchClient    { return h.client.Search }
func (h *Hub) Unread() UnreadClient    { return h.client.Unread }
func (h *Hub) Lists() ListClient       { return h.client.Lists }

// RegisterAll wires every tool category onto s. Order is not
// significant; tools register themselves conditionally based on
// config (read-only, disabled list, user-token availability).
func (h *Hub) RegisterAll(s *server.MCPServer) {
	h.registerChannelTools(s)
	h.registerDigestTools(s)
	h.registerListTools(s)
	h.registerSearchTools(s)
	h.registerThreadTools(s)
	h.registerUnreadTools(s)
	h.registerUserTools(s)
}

// toolDef is the table-driven shape used to register a tool with the
// MCP server through a single filter pipeline. Every migrated
// register* method builds a slice of toolDefs and hands them to
// (h *Hub).register — this centralises the IsDisabled / ReadOnly /
// RequiresUserToken checks that were previously duplicated inside
// every register function.
//
// First consumer: registerSearchTools (see search.go). Other
// register* methods migrate incrementally.
type toolDef struct {
	Name        string
	Description string
	Opts        []mcp.ToolOption
	// Writes flags tools that mutate Slack state. They are skipped
	// when SLACK_READ_ONLY is set.
	Writes bool
	// RequiresUserToken flags tools that depend on a user-scope OAuth
	// token (xoxp-…). They are skipped when only a bot token is
	// configured.
	RequiresUserToken bool
	Handle            server.ToolHandlerFunc
}

// register installs every def whose preconditions are satisfied. The
// filter order matters only for the resulting log line (we want
// "disabled because read-only" to be the first reason reported, not
// "disabled by env"). Currently no logging is emitted on skip — the
// log channel is kept clean — but the structure leaves a hook for
// future telemetry.
func (h *Hub) register(s *server.MCPServer, defs ...toolDef) {
	for _, t := range defs {
		if h.cfg.IsDisabled(t.Name) {
			continue
		}
		if t.Writes && h.cfg.ReadOnly {
			continue
		}
		if t.RequiresUserToken && !h.client.HasUserToken() {
			continue
		}
		opts := t.Opts
		if t.Description != "" {
			opts = append([]mcp.ToolOption{mcp.WithDescription(t.Description)}, opts...)
		}
		s.AddTool(mcp.NewTool(t.Name, opts...), h.wrap(t.Name, t.Handle))
	}
}

// wrap is the middleware seam. Today it's a pass-through; intended
// for future timing, panic recovery, or structured logging without
// touching individual handlers. Keep this function deliberately
// boring — non-obvious middleware behaviour is the kind of thing
// that ends up debugged in production at 3am.
//
// The `name` parameter is intentionally captured here even though
// the pass-through doesn't use it: future timing/logging middleware
// will key on it, and threading the name through register() now
// means the eventual upgrade is a one-file change.
func (h *Hub) wrap(name string, fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	_ = name
	return fn
}
