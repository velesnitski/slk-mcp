package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// threadKey identifies a unique thread for dedup when fetching parents
// across many search hits. Two replies in the same thread of the same
// channel share one key, so we never call conversations.replies twice.
func threadKey(m goslack.SearchMessage) string {
	return m.Channel.ID + "|" + format.ExtractThreadTS(m)
}

// fetchThreadParents enriches a slice of search hits with their thread
// parents. Only hits that are *replies* (thread_ts != ts) trigger a
// fetch; top-level messages and thread parents are skipped.
//
// Returns a map keyed by threadKey(m) so the caller can match a hit
// back to its parent without re-doing the lookup. Best-effort: if a
// single fetch errors, the loop continues — the worst case is a
// missing parent line, not a failed request.
func (h *Hub) fetchThreadParents(ctx context.Context, matches []goslack.SearchMessage) map[string]goslack.Message {
	parents := make(map[string]goslack.Message)
	seen := make(map[string]struct{})
	for _, m := range matches {
		threadTS := format.ExtractThreadTS(m)
		if threadTS == "" || threadTS == m.Timestamp || m.Channel.ID == "" {
			continue // not a reply, or the parent itself
		}
		key := threadKey(m)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		replies, err := h.Messages().ThreadReplies(ctx, m.Channel.ID, threadTS)
		if err != nil || len(replies) == 0 {
			h.log.Debug("fetch thread parent failed", "channel", m.Channel.ID, "ts", threadTS, "err", err)
			continue
		}
		// conversations.replies returns the parent as element [0].
		parents[key] = replies[0]
	}
	return parents
}

// buildUserMessagesQuery assembles the Slack search query for
// get_user_messages. Factored out for unit testing so the date-bound
// behaviour stays pinned even if the surrounding handler shifts.
//
// since/until pass straight through to Slack's own after:/before:
// operators. Validation (date format) is the caller's job.
func buildUserMessagesQuery(user, channel, since, until string) string {
	parts := []string{"from:@" + user}
	if channel != "" {
		parts = append(parts, "in:#"+strings.TrimPrefix(channel, "#"))
	}
	if since != "" {
		parts = append(parts, "after:"+since)
	}
	if until != "" {
		parts = append(parts, "before:"+until)
	}
	return strings.Join(parts, " ")
}

func (h *Hub) registerThreadTools(s *server.MCPServer) {
	if !h.cfg.IsDisabled("get_thread") {
		s.AddTool(
			mcp.NewTool("get_thread",
				mcp.WithDescription("Fetch all replies in a thread. Pass either (channel + thread_ts) or a Slack permalink."),
				mcp.WithString("channel", mcp.Description("Channel name, a DM as @handle or bare U… user id, or a canonical conversation id (optional if permalink is provided)")),
				mcp.WithString("thread_ts", mcp.Description("Thread root timestamp (optional if permalink is provided)")),
				mcp.WithString("permalink", mcp.Description("Slack permalink to any message in the thread — fills channel and thread_ts in one go")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
				mcp.WithBoolean("full_text", mcp.Description("Render reply bodies in full instead of truncating long ones to a compact preview (default: false). Use when ingesting a thread verbatim — e.g. into a knowledge base.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel := req.GetString("channel", "")
				threadTS := req.GetString("thread_ts", "")
				permalink := req.GetString("permalink", "")
				fullText := req.GetBool("full_text", false)

				scoped, _, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
				if errRes != nil {
					return errRes, nil
				}

				channel, threadTS, errRes = resolveMessageRef(permalink, channel, threadTS, true)
				if errRes != nil {
					return errRes, nil
				}

				channelID, err := scoped.resolveConversation(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				replies, err := scoped.Messages().ThreadReplies(ctx, channelID, threadTS)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				users := scoped.resolveRefs(ctx, replies)

				var b strings.Builder
				fmt.Fprintf(&b, "thread #%s (%d msgs)\n", channel, len(replies))
				for _, m := range replies {
					if fullText {
						b.WriteString(format.MessageLineFull(m, users[m.User], users))
					} else {
						b.WriteString(format.MessageLine(m, users[m.User], users))
					}
					b.WriteByte('\n')
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if !h.cfg.IsDisabled("get_user_messages") {
		s.AddTool(
			mcp.NewTool("get_user_messages",
				mcp.WithDescription("Recent messages from a user. Uses workspace search. "+
					"Pass since=/until= (YYYY-MM-DD) for absolute-time scans — preferred over "+
					"get_unread_summary when verifying that a user posted by a deadline, since "+
					"unread state depends on the caller's last_read mark. "+
					"Set with_thread_context=true to inline the thread parent for each hit "+
					"that is itself a reply — turns fragmentary search results "+
					"(\"ok\", \"got it\") into self-explanatory lines."),
				mcp.WithString("user", mcp.Required(), mcp.Description("Username or display name")),
				mcp.WithString("channel", mcp.Description("Optional channel name to restrict search")),
				mcp.WithNumber("limit", mcp.Description("Max hits (default: 30)")),
				mcp.WithString("since", mcp.Description("Lower bound, YYYY-MM-DD. Maps to Slack search after:")),
				mcp.WithString("until", mcp.Description("Upper bound, YYYY-MM-DD. Maps to Slack search before:")),
				mcp.WithBoolean("with_thread_context", mcp.Description("If true, for each hit that is a thread reply, inline the thread parent on a continuation line. Costs one conversations.replies call per unique thread (default: false).")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				user, err := req.RequireString("user")
				if err != nil {
					return mcp.NewToolResultError("user is required"), nil
				}
				channel := req.GetString("channel", "")
				limit := int(req.GetFloat("limit", 30))
				since := req.GetString("since", "")
				until := req.GetString("until", "")
				withThreadCtx := req.GetBool("with_thread_context", false)
				for _, d := range []struct{ name, val string }{{"since", since}, {"until", until}} {
					if d.val == "" {
						continue
					}
					if _, perr := time.Parse("2006-01-02", d.val); perr != nil {
						return mcp.NewToolResultError(d.name + " must be YYYY-MM-DD"), nil
					}
				}

				query := buildUserMessagesQuery(user, channel, since, until)
				matches, err := h.Search().Messages(ctx, query, limit)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if len(matches) == 0 {
					return mcp.NewToolResultText(fmt.Sprintf("no messages from %s", user)), nil
				}

				var parents map[string]goslack.Message
				if withThreadCtx {
					parents = h.fetchThreadParents(ctx, matches)
				}

				var b strings.Builder
				fmt.Fprintf(&b, "%d msgs from %s\n", len(matches), user)
				for _, m := range matches {
					b.WriteString(format.SearchResult(m))
					b.WriteByte('\n')
					if parent, ok := parents[threadKey(m)]; ok {
						parentName := h.Users().Name(ctx, parent.User)
						b.WriteString(format.ThreadContextLine("↑", parent, parentName))
						b.WriteByte('\n')
					}
				}
				return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
			},
		)
	}

	if h.cfg.ReadOnly {
		return
	}

	if !h.cfg.IsDisabled("post_message") {
		s.AddTool(
			mcp.NewTool("post_message",
				mcp.WithDescription("Post a message to a channel. Supports thread replies. With several workspaces configured, pass `workspace` to target a non-primary one (default: primary). Pass `skip_if_recent` (minutes) to suppress a duplicate — if you already posted the identical text in that channel within the window, the post is skipped instead of repeated."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
				mcp.WithString("text", mcp.Required(), mcp.Description("Message text (Slack markdown)")),
				mcp.WithString("thread_ts", mcp.Description("Optional thread timestamp to reply in thread")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
				mcp.WithNumber("skip_if_recent", mcp.Description("Dedup guard (minutes, default 0=off). If >0, skip posting when the authenticated user already posted the identical text in this channel within the last N minutes. Best-effort: needs a user token to identify self; if it can't, it posts.")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, err := req.RequireString("channel")
				if err != nil {
					return mcp.NewToolResultError("channel is required"), nil
				}
				text, err := req.RequireString("text")
				if err != nil {
					return mcp.NewToolResultError("text is required"), nil
				}
				return h.runPostMessage(ctx,
					req.GetString("workspace", ""),
					channel, text,
					req.GetString("thread_ts", ""),
					int(req.GetFloat("skip_if_recent", 0))), nil
			},
		)
	}

	if !h.cfg.IsDisabled("add_reaction") {
		s.AddTool(
			mcp.NewTool("add_reaction",
				mcp.WithDescription("Add an emoji reaction to a message."),
				mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name")),
				mcp.WithString("timestamp", mcp.Required(), mcp.Description("Message ts")),
				mcp.WithString("emoji", mcp.Required(), mcp.Description("Emoji name without colons (e.g. thumbsup)")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				channel, _ := req.RequireString("channel")
				timestamp, _ := req.RequireString("timestamp")
				emoji, _ := req.RequireString("emoji")

				scoped, wsName, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
				if errRes != nil {
					return errRes, nil
				}
				channelID, err := scoped.Channels().ResolveID(ctx, channel)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				if err := scoped.Messages().AddReaction(ctx, channelID, timestamp, emoji); err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return mcp.NewToolResultText(fmt.Sprintf("added :%s: on #%s%s", emoji, channel, h.wsLabel(wsName))), nil
			},
		)
	}

	if !h.cfg.IsDisabled("delete_message") {
		s.AddTool(
			mcp.NewTool("delete_message",
				mcp.WithDescription("Delete a message via chat.delete. Pass either a Slack permalink (fills channel + timestamp in one paste — e.g. straight from search/digest output), OR channel + timestamp. IRREVERSIBLE. With a user token Slack only allows deleting messages the authenticated user posted (a bot token, only the bot's own) — that ownership check is the safety boundary. With several workspaces configured, pass `workspace` to target a non-primary one (default: primary)."),
				mcp.WithString("channel", mcp.Description("Channel name (#general, general) or ID — required unless permalink is given")),
				mcp.WithString("timestamp", mcp.Description("Message ts to delete (e.g. the value returned by post_message) — required unless permalink is given")),
				mcp.WithString("permalink", mcp.Description("Slack message permalink — resolves channel + timestamp on its own")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.runDeleteMessage(ctx,
					req.GetString("workspace", ""),
					req.GetString("channel", ""),
					req.GetString("timestamp", ""),
					req.GetString("permalink", "")), nil
			},
		)
	}
}

// runDeleteMessage deletes one message, targeting it by permalink (which
// resolves both channel and ts in a single paste) or by channel + ts.
// Factored out of the tool closure so the input-validation and
// workspace-routing paths — all of which return before any Slack call —
// are unit-testable without a live server. The permalink, when supplied,
// wins: it already carries a canonical channel ID, so no name lookup runs.
func (h *Hub) runDeleteMessage(ctx context.Context, workspace, channel, timestamp, permalink string) *mcp.CallToolResult {
	channelID := ""
	if strings.TrimSpace(permalink) != "" {
		p, err := slack.ParseSlackPermalink(permalink)
		if err != nil {
			return mcp.NewToolResultError("invalid permalink: " + err.Error())
		}
		channelID, timestamp = p.ChannelID, p.TS
	}
	if channelID == "" && strings.TrimSpace(channel) == "" {
		return mcp.NewToolResultError("provide a permalink, or channel + timestamp")
	}
	if strings.TrimSpace(timestamp) == "" {
		return mcp.NewToolResultError("provide a permalink, or channel + timestamp")
	}

	scoped, wsName, errRes := h.scopedWorkspace(workspace)
	if errRes != nil {
		return errRes
	}

	// Resolve the name only when the permalink didn't already hand us an ID.
	if channelID == "" {
		id, err := scoped.Channels().ResolveID(ctx, channel)
		if err != nil {
			return mcp.NewToolResultError(err.Error())
		}
		channelID = id
	}

	if err := scoped.Messages().Delete(ctx, channelID, timestamp); err != nil {
		return mcp.NewToolResultError(deleteErrorHint(err))
	}

	where := strings.TrimPrefix(channel, "#")
	if where != "" {
		where = "#" + where
	} else {
		where = channelID
	}
	return mcp.NewToolResultText(fmt.Sprintf("deleted message %s in %s%s", timestamp, where, h.wsLabel(wsName)))
}

// deleteErrorHint turns Slack's terse delete failures into something
// actionable — the most common one (cant_delete_message) almost always
// means the message wasn't authored by this token's identity.
func deleteErrorHint(err error) string {
	e := err.Error()
	switch {
	case strings.Contains(e, "cant_delete_message"):
		return e + " — with a user/bot token you can only delete messages that identity posted"
	case strings.Contains(e, "message_not_found"):
		return e + " — no message at that channel + timestamp (already deleted, or wrong ts/channel)"
	default:
		return e
	}
}

// runPostMessage resolves the target workspace (named, or primary when the
// arg is empty), then posts into it. Factored out of the tool closure so
// the workspace-routing — including the unknown-label error path, which
// returns before any Slack call — is unit-testable without a live server.
func (h *Hub) runPostMessage(ctx context.Context, workspace, channel, text, threadTS string, skipIfRecentMin int) *mcp.CallToolResult {
	scoped, wsName, errRes := h.scopedWorkspace(workspace)
	if errRes != nil {
		return errRes
	}
	channelID, err := scoped.Channels().ResolveID(ctx, channel)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	// Dedup guard: skip when the authenticated user already posted the
	// identical text here within the window. Best-effort — fails open
	// (posts) when self can't be identified or history can't be read.
	if skipIfRecentMin > 0 && scoped.recentSelfDuplicate(ctx, channelID, text, skipIfRecentMin) {
		return mcp.NewToolResultText(fmt.Sprintf("skipped #%s%s — identical message already posted by you within %dm (skip_if_recent)", channel, h.wsLabel(wsName), skipIfRecentMin))
	}
	ts, err := scoped.Messages().Post(ctx, channelID, text, threadTS)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultText(fmt.Sprintf("posted to #%s%s (ts: %s)", channel, h.wsLabel(wsName), ts))
}

// recentSelfDuplicate reports whether the authenticated user already
// posted the identical (trimmed) text into channelID within the last
// withinMin minutes. It is the engine behind post_message's
// skip_if_recent guard and is deliberately best-effort: it needs a user
// token to resolve "self", and any failure (no user token, auth.test or
// history error) returns false so the caller still posts — a missed
// dedup is recoverable, a refused post is not. h must already be scoped
// to the target workspace.
func (h *Hub) recentSelfDuplicate(ctx context.Context, channelID, text string, withinMin int) bool {
	if !h.client.HasUserToken() {
		return false // can't attribute authorship → don't suppress
	}
	self, err := h.Unread().Self(ctx)
	if err != nil || self == "" {
		return false
	}
	oldest := time.Now().Add(-time.Duration(withinMin) * time.Minute)
	msgs, err := h.Messages().History(ctx, slack.HistoryParams{
		ChannelID: channelID,
		OldestTS:  float64(oldest.Unix()),
		Limit:     100,
	})
	if err != nil {
		return false
	}
	want := strings.TrimSpace(text)
	for _, m := range msgs {
		if m.User == self && strings.TrimSpace(m.Text) == want {
			return true
		}
	}
	return false
}
