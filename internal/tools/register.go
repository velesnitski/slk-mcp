// Package tools wires MCP tool handlers to the Slack service layer.
package tools

import (
	"log/slog"
	"strings"

	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// Deps is the surface every tool module needs.
type Deps struct {
	Client *slack.Client
	Cfg    *config.Config
	Log    *slog.Logger
}

// RegisterAll wires every tool module onto s. Tools that require a user
// token register themselves conditionally.
func RegisterAll(s *server.MCPServer, deps Deps) {
	registerChannelTools(s, deps)
	registerDigestTools(s, deps)
	registerSearchTools(s, deps)
	registerThreadTools(s, deps)
	registerUnreadTools(s, deps)
}

// parseChannelList splits a comma-separated input, falling back to defaults.
func parseChannelList(input string, defaults []string) []string {
	if strings.TrimSpace(input) == "" {
		return defaults
	}
	var result []string
	for _, ch := range strings.Split(input, ",") {
		ch = strings.TrimSpace(strings.TrimPrefix(ch, "#"))
		if ch != "" {
			result = append(result, ch)
		}
	}
	return result
}

// detectDecisions returns decision lines for messages matching configured
// keywords or reactions.
func detectDecisions(cfg *config.Config, channel string, messages []goslack.Message, users map[string]string, render func(goslack.Message, string, string, string) string) []string {
	var out []string
	for _, msg := range messages {
		reason, ok := matchDecision(cfg, msg)
		if !ok {
			continue
		}
		name := users[msg.User]
		if name == "" {
			name = msg.User
		}
		out = append(out, render(msg, channel, name, reason))
	}
	return out
}

func matchDecision(cfg *config.Config, msg goslack.Message) (string, bool) {
	text := strings.ToLower(msg.Text)
	for _, kw := range cfg.DecisionKeywords {
		if strings.Contains(text, kw) {
			return "keyword:" + kw, true
		}
	}
	if len(msg.Reactions) > 0 {
		wanted := make(map[string]struct{}, len(cfg.DecisionReactions))
		for _, r := range cfg.DecisionReactions {
			wanted[r] = struct{}{}
		}
		for _, r := range msg.Reactions {
			if _, ok := wanted[r.Name]; ok {
				return "reaction::" + r.Name + ":", true
			}
		}
	}
	return "", false
}

// collectUserIDs returns the unique set of user IDs in a message slice.
func collectUserIDs(messages []goslack.Message) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		if m.User == "" {
			continue
		}
		if _, ok := seen[m.User]; ok {
			continue
		}
		seen[m.User] = struct{}{}
		ids = append(ids, m.User)
	}
	return ids
}
