package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/export"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

const (
	exportDefaultHours = 168 // one week
	exportMaxPerChan   = 500
	exportThreadCap    = 200
)

func (h *Hub) registerExportTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("export_conversations") {
		return
	}
	s.AddTool(
		mcp.NewTool("export_conversations",
			mcp.WithDescription("Append conversations to a local append-only JSONL corpus and return the file path. Captures losslessly (no summarising, no dropping): thread structure, reactions with their actors, edit flags, attachments, permalinks, and reply_count vs replies_fetched so a later reader can tell a short thread from a truncated one. Re-running over an overlapping window does not duplicate. Requires SLACK_USER_TOKEN."),
			mcp.WithNumber("hours", mcp.Description("Lookback window (default: 168 = one week)")),
			mcp.WithString("dir", mcp.Description("Output directory (default: $HOME/slk-export). One file per workspace: <dir>/<workspace>.jsonl")),
			mcp.WithString("channels", mcp.Description("Comma-separated channel names to capture. Empty (default) selects the channels and DMs the operator actually posted in during the window — not every joined channel.")),
			mcp.WithBoolean("include_dms", mcp.Description("Include DM and multi-party-DM conversations (default: true)")),
			mcp.WithBoolean("redact", mcp.Description("Replace credential-shaped spans with a stable hash placeholder so the same secret stays linkable across records without reaching disk (default: true). Set false only for a corpus you control the disk of.")),
			mcp.WithNumber("max_per_channel", mcp.Description("Max top-level messages per channel (default: 500)")),
			mcp.WithString("workspace", mcp.Description("Limit to a single workspace by its configured label. Default: every configured workspace.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			p := exportParams{
				hours:      int(req.GetFloat("hours", exportDefaultHours)),
				dir:        strings.TrimSpace(req.GetString("dir", "")),
				channels:   splitCSV(req.GetString("channels", "")),
				includeDMs: req.GetBool("include_dms", true),
				redact:     req.GetBool("redact", true),
				maxPer:     int(req.GetFloat("max_per_channel", exportMaxPerChan)),
			}
			return h.runExport(ctx, p, req.GetString("workspace", "")), nil
		},
	)
}

type exportParams struct {
	hours      int
	dir        string
	channels   []string
	includeDMs bool
	redact     bool
	maxPer     int
}

// runExport walks the requested workspaces, appending each one's records
// to its own corpus file.
func (h *Hub) runExport(ctx context.Context, p exportParams, workspace string) *mcp.CallToolResult {
	targets := h.workspaceTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	if p.hours <= 0 {
		p.hours = exportDefaultHours
	}
	if p.maxPer <= 0 {
		p.maxPer = exportMaxPerChan
	}
	dir, err := exportDir(p.dir)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("export: create %s: %v", dir, err))
	}

	var lines []string
	for _, ws := range targets {
		scoped := h.withClient(ws.Client)
		if !scoped.client.HasUserToken() {
			lines = append(lines, fmt.Sprintf("%s: skipped (no user token)", ws.Name))
			continue
		}
		path := filepath.Join(dir, workspaceFilename(ws.Name)+".jsonl")
		written, scanned, partial, err := scoped.exportWorkspace(ctx, p, ws.Name, path)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: error: %v", ws.Name, err))
			continue
		}
		note := ""
		if partial > 0 {
			note = fmt.Sprintf(", %d thread(s) captured partially", partial)
		}
		lines = append(lines, fmt.Sprintf("%s: +%d new of %d scanned%s → %s", ws.Name, written, scanned, note, path))
	}
	return mcp.NewToolResultText("# Export\n" + strings.Join(lines, "\n"))
}

// exportWorkspace captures one workspace into `path`.
func (h *Hub) exportWorkspace(ctx context.Context, p exportParams, wsName, path string) (written, scanned, partial int, err error) {
	channels, err := h.exportChannels(ctx, p)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(channels) == 0 {
		return 0, 0, 0, nil
	}
	teamURL, _ := h.Unread().TeamURL(ctx)

	oldest := float64(time.Now().Unix() - int64(p.hours)*3600)
	var recs []export.Record
	for _, ch := range channels {
		if !p.includeDMs && (slack.IsDirectMessage(ch) || ch.IsMpIM) {
			continue
		}
		msgs, herr := h.Messages().History(ctx, slack.HistoryParams{
			ChannelID: ch.ID, OldestTS: oldest, Limit: p.maxPer,
		})
		if herr != nil {
			h.log.Warn("export: history failed", "channel", ch.ID, "err", herr)
			continue
		}
		scanned += len(msgs)
		for _, m := range msgs {
			replies := h.exportReplies(ctx, ch.ID, m)
			if m.ReplyCount > 0 && len(replies) < m.ReplyCount {
				partial++
			}
			recs = append(recs, h.toRecord(ctx, wsName, ch, m, teamURL, len(replies), p.redact))
			for _, r := range replies {
				scanned++
				recs = append(recs, h.toRecord(ctx, wsName, ch, r, teamURL, 0, p.redact))
			}
		}
	}

	seen := loadCorpusKeys(path)
	fresh := export.Dedup(recs, seen)
	if len(fresh) == 0 {
		return 0, scanned, partial, nil
	}
	sort.SliceStable(fresh, func(i, j int) bool { return tsLessStr(fresh[i].TS, fresh[j].TS) })

	f, ferr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if ferr != nil {
		return 0, scanned, partial, fmt.Errorf("export: open %s: %w", path, ferr)
	}
	defer f.Close()
	if werr := export.Write(f, fresh); werr != nil {
		return 0, scanned, partial, werr
	}
	return len(fresh), scanned, partial, nil
}

// exportChannels resolves the capture set: explicit names when given,
// otherwise the channels the operator actually posted in.
func (h *Hub) exportChannels(ctx context.Context, p exportParams) ([]goslack.Channel, error) {
	if len(p.channels) == 0 {
		return h.Unread().ParticipationChannels(ctx, p.hours)
	}
	out := make([]goslack.Channel, 0, len(p.channels))
	for _, name := range p.channels {
		id, err := h.Channels().ResolveID(ctx, name)
		if err != nil {
			h.log.Warn("export: channel not resolved", "channel", name, "err", err)
			continue
		}
		ch := goslack.Channel{}
		ch.ID = id
		ch.Name = strings.TrimPrefix(name, "#")
		out = append(out, ch)
	}
	return out, nil
}

// exportReplies fetches a thread's replies, dropping the parent echo
// Slack includes as the first element.
func (h *Hub) exportReplies(ctx context.Context, channelID string, parent goslack.Message) []goslack.Message {
	if parent.ReplyCount == 0 || parent.ThreadTimestamp == "" {
		return nil
	}
	replies, err := h.Messages().ThreadReplies(ctx, channelID, parent.ThreadTimestamp)
	if err != nil {
		h.log.Warn("export: thread replies failed", "channel", channelID, "thread", parent.ThreadTimestamp, "err", err)
		return nil
	}
	out := make([]goslack.Message, 0, len(replies))
	for _, r := range replies {
		if r.Timestamp == parent.Timestamp {
			continue
		}
		out = append(out, r)
		if len(out) >= exportThreadCap {
			break
		}
	}
	return out
}

// toRecord converts one Slack message into a corpus record.
func (h *Hub) toRecord(ctx context.Context, wsName string, ch goslack.Channel, m goslack.Message, teamURL string, repliesFetched int, redact bool) export.Record {
	text := m.Text
	n := 0
	if redact {
		text, n = export.Redact(text)
	}
	rec := export.Record{
		Workspace:      wsName,
		ChannelID:      ch.ID,
		Channel:        ch.Name,
		Kind:           channelKind(ch),
		TS:             m.Timestamp,
		ThreadTS:       m.ThreadTimestamp,
		User:           m.User,
		Text:           text,
		Subtype:        m.SubType,
		Edited:         m.Edited != nil,
		Reactions:      toReactions(m.Reactions),
		Files:          toFileRefs(m.Files),
		ReplyCount:     m.ReplyCount,
		RepliesFetched: repliesFetched,
		Permalink:      export.Permalink(teamURL, ch.ID, m.Timestamp, m.ThreadTimestamp),
		Redacted:       n,
	}
	if m.User != "" {
		rec.UserName = h.Users().Name(ctx, m.User)
	}
	return rec
}

// toReactions keeps who reacted, not just how many. In a channel where
// nobody writes "approved", a ✅ from the right person IS the decision.
func toReactions(rs []goslack.ItemReaction) []export.Reaction {
	if len(rs) == 0 {
		return nil
	}
	out := make([]export.Reaction, 0, len(rs))
	for _, r := range rs {
		out = append(out, export.Reaction{Name: r.Name, Users: r.Users})
	}
	return out
}

func toFileRefs(fs []goslack.File) []export.FileRef {
	if len(fs) == 0 {
		return nil
	}
	out := make([]export.FileRef, 0, len(fs))
	for _, f := range fs {
		out = append(out, export.FileRef{ID: f.ID, Name: f.Name, Mimetype: f.Mimetype})
	}
	return out
}

// channelKind labels the conversation type so a reader can separate
// public discussion from a DM without guessing at the ID prefix. Pure.
func channelKind(ch goslack.Channel) string {
	switch {
	case ch.IsMpIM:
		return "mpim"
	case ch.IsIM || strings.HasPrefix(ch.ID, "D"):
		return "im"
	case ch.IsPrivate || strings.HasPrefix(ch.ID, "G"):
		return "private"
	default:
		return "channel"
	}
}

// exportDir resolves the output directory, defaulting under $HOME. A
// corpus is long-lived, so it gets an explicit named home rather than
// the temp dir, which is swept out from under it. Pure apart from the
// HOME lookup.
func exportDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("export: no dir given and home dir unavailable: %w", err)
	}
	return filepath.Join(home, "slk-export"), nil
}

// workspaceFilename maps a workspace label to a corpus filename. Reuses
// sanitizeFilename and swaps its audio-specific fallback. Pure.
func workspaceFilename(name string) string {
	if strings.TrimSpace(name) == "" {
		return "workspace"
	}
	out := sanitizeFilename(name)
	if out == "audio" && !strings.EqualFold(strings.TrimSpace(name), "audio") {
		return "workspace"
	}
	return out
}

// tsLessStr orders Slack timestamps numerically. Pure.
func tsLessStr(a, b string) bool {
	fa, _ := strconv.ParseFloat(a, 64)
	fb, _ := strconv.ParseFloat(b, 64)
	return fa < fb
}

// splitCSV splits and trims a comma-separated argument. Pure.
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadCorpusKeys reads the keys already in a corpus. A missing file is
// the normal first-run case, not an error.
func loadCorpusKeys(path string) map[string]struct{} {
	f, err := os.Open(path)
	if err != nil {
		return make(map[string]struct{})
	}
	defer f.Close()
	return export.ReadKeys(f)
}
