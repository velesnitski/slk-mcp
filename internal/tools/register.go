package tools

import (
	"context"
	"strings"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/format"
)

// parseChannelList splits a comma-separated input. Returns nil if input is empty.
func parseChannelList(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
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

// resolveTargetChannels picks the list of channels for a tool call, in
// priority order:
//
//  1. explicit input (the tool's `channels` argument);
//  2. SLACK_CHANNELS configured at startup;
//  3. auto-discovery — every channel the active identity has joined,
//     capped at SLACK_AUTODISCOVER_LIMIT (default 50).
//
// Returns the resolved list, the source label ("input"/"config"/"auto"),
// and any error encountered during auto-discovery.
func (h *Hub) resolveTargetChannels(ctx context.Context, input string) ([]string, string, error) {
	if list := parseChannelList(input); len(list) > 0 {
		return list, "input", nil
	}
	if len(h.cfg.Channels) > 0 {
		return h.cfg.Channels, "config", nil
	}

	names, err := h.client.JoinedChannelNames(ctx, h.cfg.AutodiscoverLimit)
	if err != nil {
		return nil, "auto", err
	}
	h.log.Info("auto-discovered channels",
		"count", len(names),
		"limit", h.cfg.AutodiscoverLimit,
	)
	return names, "auto", nil
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

// resolveRefs builds the unified id→name map that the format package
// uses to resolve `<@USERID>` and `<#CHANNELID>` references inside
// rendered message bodies. Users are always resolved; channels are
// looked up via the reverse-id cache (cheap) with API fallback for
// unseen IDs (one conversations.info call each).
//
// The two maps are merged because Slack ID prefixes (U/W vs C/G) keep
// their namespaces disjoint — see format.RenderText.
func (h *Hub) resolveRefs(ctx context.Context, messages []goslack.Message) map[string]string {
	users := h.Users().NamesFor(ctx, collectUserIDs(messages))
	channels := h.Channels().NamesForIDs(ctx, format.CollectMentionedChannelIDs(messages))
	return mergeRefs(users, channels)
}

// resolveTextRefs is resolveRefs for a body that is not a message —
// a canvas, a document — where the IDs can only be found by scanning
// the text itself. Splits by Slack's prefix rule (U/W are people,
// C/G/D are conversations) so each ID is asked of the right service.
func (h *Hub) resolveTextRefs(ctx context.Context, text string) map[string]string {
	var userIDs, channelIDs []string
	for _, id := range format.CollectRefIDsInText(text) {
		switch id[0] {
		case 'U', 'W':
			userIDs = append(userIDs, id)
		default:
			channelIDs = append(channelIDs, id)
		}
	}
	if len(userIDs) == 0 && len(channelIDs) == 0 {
		return map[string]string{}
	}
	return mergeRefs(
		h.Users().NamesFor(ctx, userIDs),
		h.Channels().NamesForIDs(ctx, channelIDs),
	)
}

// mergeRefs merges name lookup maps for users and channels into one,
// always returning a usable (non-nil) map. Channel and user IDs cannot
// collide due to Slack's prefix rules (U/W vs C/G/D), so a single map
// is safe.
func mergeRefs(users, channels map[string]string) map[string]string {
	if len(channels) == 0 {
		if users == nil {
			return map[string]string{}
		}
		return users
	}
	if users == nil {
		users = make(map[string]string, len(channels))
	}
	for k, v := range channels {
		users[k] = v
	}
	return users
}
