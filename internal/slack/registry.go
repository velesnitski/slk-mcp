package slack

import (
	"log/slog"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// Workspace pairs a human label with the Slack client that talks to that
// workspace. Slack tokens are workspace-scoped, so each workspace needs
// its own *Client (its own credential and HTTP pools).
type Workspace struct {
	Name   string
	Client *Client
}

// NewRegistry builds one *Client per configured workspace, preserving
// order — registry[0] is the primary (its tokens mirror the legacy
// SLACK_TOKEN/SLACK_USER_TOKEN pair). A single-workspace config (the
// common case, and any hand-built test Config) yields a one-element
// registry, so callers never special-case the count.
//
// The caller must have run cfg.Validate() first.
func NewRegistry(cfg *config.Config, log *slog.Logger) []Workspace {
	views := cfg.WorkspaceViews()
	out := make([]Workspace, 0, len(views))
	for _, v := range views {
		out = append(out, Workspace{
			Name:   v.Name,
			Client: New(v.Cfg, log.With("workspace", v.Name)),
		})
	}
	return out
}
