package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// registerSearchTools is the pilot for the table-driven registration
// shape (see hub.go: toolDef / register / wrap). Other register*
// methods will migrate one at a time; this file is the reference for
// what the destination shape looks like.
func (h *Hub) registerSearchTools(s *server.MCPServer) {
	h.register(s,
		toolDef{
			Name:        "search_messages",
			Description: "Workspace search (Slack syntax: from:@user, in:#channel, has:link, before:/after:DATE). Each hit includes thread_ts + permalink so callers can chain into get_thread. A hit is an ISOLATED message — pass with_context=true to inline the surrounding messages so it can be read in context (a `from:@user` search never shows the other side of the conversation).",
			Opts: []mcp.ToolOption{
				mcp.WithString("query", mcp.Required(), mcp.Description("Slack search query")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 20)")),
				mcp.WithBoolean("full_text", mcp.Description("Disable the 200-char body truncation (default: false). Use when issue IDs or URLs sit at the end of the body.")),
				mcp.WithBoolean("with_context", mcp.Description("For each hit, inline a few messages before and after it from the same channel/DM (default: false). Use to interpret a hit in context — e.g. a `from:@user` search shows only that user's line, not the reply it answered.")),
				mcp.WithNumber("context_messages", mcp.Description("How many messages to inline on each side when with_context=true (default: 3)")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			},
			Handle: h.handleSearchMessages,
		},
		toolDef{
			Name:        "find_decisions",
			Description: "Scan channels for messages that look like decisions (keywords + reactions).",
			Opts: []mcp.ToolOption{
				mcp.WithString("channels", mcp.Description("Comma-separated channel names; uses SLACK_CHANNELS if empty")),
				mcp.WithNumber("hours", mcp.Description("Lookback window (default: 72)")),
			},
			Handle: h.handleFindDecisions,
		},
	)
}

func (h *Hub) handleSearchMessages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("query is required"), nil
	}
	scoped, _, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
	if errRes != nil {
		return errRes, nil
	}
	limit := int(req.GetFloat("limit", 20))
	fullText := req.GetBool("full_text", false)
	withContext := req.GetBool("with_context", false)
	ctxN := int(req.GetFloat("context_messages", 3))

	matches, err := scoped.Search().Messages(ctx, q, limit)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(matches) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("no hits for: %s", q)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d hits for: %s\n", len(matches), q)
	shown := map[string]struct{}{}
	for _, m := range matches {
		b.WriteString(format.SearchResultExt(m, fullText))
		b.WriteByte('\n')
		// Reuse the get_mentions context machinery: a search hit is one
		// isolated message, and interpreting it (especially a from:@user
		// hit) usually needs the surrounding turns — the failure mode this
		// closes is reading one side of a two-sided exchange.
		if withContext && m.Channel.ID != "" {
			before, after := scoped.fetchMentionContext(ctx, m.Channel.ID, m.Timestamp, ctxN)
			users := scoped.Users().NamesFor(ctx, append(collectUserIDs(before), collectUserIDs(after)...))
			writeContextLines(&b, "    ↳ ", before, users, m.Channel.ID, shown)
			writeContextLines(&b, "    ↪ ", after, users, m.Channel.ID, shown)
		}
	}
	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
}

func (h *Hub) handleFindDecisions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	list, _, err := h.resolveTargetChannels(ctx, req.GetString("channels", ""))
	if err != nil {
		return mcp.NewToolResultError("auto-discover channels: " + err.Error()), nil
	}
	if len(list) == 0 {
		return mcp.NewToolResultError("no channels available — pass channels, set SLACK_CHANNELS, or join some channels"), nil
	}
	hours := int(req.GetFloat("hours", 72))
	oldest := time.Now().Add(-time.Duration(hours) * time.Hour)

	var decisions []string
	for _, ch := range list {
		channelID, err := h.Channels().ResolveID(ctx, ch)
		if err != nil {
			decisions = append(decisions, fmt.Sprintf("- #%s error: %v", ch, err))
			continue
		}
		msgs, err := h.Messages().History(ctx, slack.HistoryParams{
			ChannelID: channelID,
			OldestTS:  float64(oldest.Unix()),
			Limit:     h.cfg.MaxMessagesPerChannel,
		})
		if err != nil {
			decisions = append(decisions, fmt.Sprintf("- #%s error: %v", ch, err))
			continue
		}
		users := h.resolveRefs(ctx, msgs)
		decisions = append(decisions, detectDecisions(h.cfg, ch, msgs, users, format.DecisionLine)...)
	}

	if len(decisions) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("no decisions found in last %dh", hours)), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d decisions (last %dh)\n", len(decisions), hours)
	for _, d := range decisions {
		b.WriteString(d)
		b.WriteByte('\n')
	}
	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
}

// Compile-time guard: server.ToolHandlerFunc is the contract the
// table seam carries. If mark3labs ever changes that signature the
// build breaks here, not deep inside a tool registration.
var (
	_ server.ToolHandlerFunc = (*Hub)(nil).handleSearchMessages
	_ server.ToolHandlerFunc = (*Hub)(nil).handleFindDecisions
)
