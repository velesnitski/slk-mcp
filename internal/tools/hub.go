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
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// Hub bundles the dependencies every tool handler reads from.
// Construct with NewHub; consumers should treat it as immutable once
// returned.
//
// Multi-workspace model: `client` and `cfg` always point at the PRIMARY
// workspace (registry[0]), so every existing handler that reads h.client
// / h.cfg keeps targeting the primary with zero changes. `registry`
// holds all workspaces; the digest tools loop over it, and withClient
// produces a shallow Hub copy retargeted at a non-primary workspace.
type Hub struct {
	client   *slack.Client
	cfg      *config.Config
	log      *slog.Logger
	registry []slack.Workspace
}

// NewHub builds a Hub around a single already-constructed client. The
// registry collapses to that one client (labelled from the config's
// primary workspace), so single-workspace callers and existing tests
// need no changes. main.go owns the lifecycle of the dependencies — Hub
// never closes the client or the logger.
func NewHub(client *slack.Client, cfg *config.Config, log *slog.Logger) *Hub {
	return &Hub{
		client:   client,
		cfg:      cfg,
		log:      log,
		registry: []slack.Workspace{{Name: primaryWorkspaceName(cfg), Client: client}},
	}
}

// NewHubWithRegistry builds a Hub serving every workspace in the
// registry. registry[0] is the primary and becomes h.client; cfg is the
// shared (primary) config carrying the global scalars. Panics on an
// empty registry — that is a programming error (NewRegistry always
// returns at least one workspace for a validated config).
func NewHubWithRegistry(registry []slack.Workspace, cfg *config.Config, log *slog.Logger) *Hub {
	if len(registry) == 0 {
		panic("tools.NewHubWithRegistry: empty registry")
	}
	return &Hub{
		client:   registry[0].Client,
		cfg:      cfg,
		log:      log,
		registry: registry,
	}
}

// primaryWorkspaceName returns the label for workspace[0], defaulting to
// "primary" when no workspaces are configured (hand-built test configs).
func primaryWorkspaceName(cfg *config.Config) string {
	if len(cfg.Workspaces) > 0 && cfg.Workspaces[0].Name != "" {
		return cfg.Workspaces[0].Name
	}
	return "primary"
}

// Workspaces returns the registry (primary first). Read-only.
func (h *Hub) Workspaces() []slack.Workspace { return h.registry }

// multiWorkspace reports whether more than one workspace is served, which
// is what flips digest output into labelled per-workspace sections.
func (h *Hub) multiWorkspace() bool { return len(h.registry) > 1 }

// withClient returns a shallow copy of the Hub retargeted at ws. Because
// every Hub accessor and helper reads h.client / h.cfg, swapping both
// (cfg comes from the client it was built with) makes the entire handler
// surface operate against that workspace with no per-call plumbing. The
// copy shares log and registry; nothing is mutated, so it is safe to use
// concurrently with the parent.
func (h *Hub) withClient(c *slack.Client) *Hub {
	cp := *h
	cp.client = c
	cp.cfg = c.Config()
	return &cp
}

// workspaceTargets resolves the `workspace` tool argument into the set of
// workspaces a digest should cover. An empty name means "all workspaces".
// A non-empty name is matched case-insensitively against the labels; an
// unknown name returns a nil slice so the handler can report it.
func (h *Hub) workspaceTargets(name string) []slack.Workspace {
	name = strings.TrimSpace(name)
	if name == "" {
		return h.registry
	}
	for _, ws := range h.registry {
		if strings.EqualFold(ws.Name, name) {
			return []slack.Workspace{ws}
		}
	}
	return nil
}

// workspaceNames returns the configured labels, primary first, for use in
// "unknown workspace" error messages.
func (h *Hub) workspaceNames() []string {
	names := make([]string, len(h.registry))
	for i, ws := range h.registry {
		names[i] = ws.Name
	}
	return names
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
