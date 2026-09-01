package tools

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
)

// canvasMaxBytes caps how much of a channel canvas we download and
// render. Canvases are documents, not feeds — a generous ceiling keeps a
// runaway doc from blowing the MCP result while covering any realistic
// team canvas.
const canvasMaxBytes = 200_000

func (h *Hub) registerCanvasTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("read_canvas") {
		return
	}
	s.AddTool(
		mcp.NewTool("read_canvas",
			mcp.WithDescription("Read a canvas in a channel (or DM). Slack canvases are file-backed documents, not messages, so a regular digest only shows they exist — this resolves the channel's canvases (attached tab AND shared canvas files), picks one, downloads it, and returns its text. Meeting-notes canvases are usually titled with a date — pass `date` to check whether notes for that day exist: a miss lists the canvases that DO exist instead of a bare error."),
			mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops or devops), a DM as @handle or bare U… user id, or a canonical C/G/D conversation id")),
			mcp.WithString("date", mcp.Description("Pick the canvas whose TITLE contains this date, tried in common spellings (2026-07-23 → 23.07.2026, 23.07.26, 23.07, 23/07/2026). Format: YYYY-MM-DD. The reliable selector for per-meeting notes canvases — titles carry the meeting date, file Created does not.")),
			mcp.WithString("match", mcp.Description("Pick the canvas whose title contains this substring (case-insensitive), e.g. 'Tech Meet'. Combines with date (both must match when both set).")),
			mcp.WithBoolean("list_only", mcp.Description("Just list the channel's canvases (title + created), without downloading any. Default: false.")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			channel, err := req.RequireString("channel")
			if err != nil {
				return mcp.NewToolResultError("channel is required"), nil
			}
			scoped, _, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
			if errRes != nil {
				return errRes, nil
			}
			txt, err := scoped.readCanvas(ctx, channel, canvasSelector{
				date:     strings.TrimSpace(req.GetString("date", "")),
				match:    strings.TrimSpace(req.GetString("match", "")),
				listOnly: req.GetBool("list_only", false),
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(txt), nil
		},
	)
}

// readCanvas resolves a channel's canvas and renders its text. Lookup
// cascade (each step legitimately finds canvases the previous can't):
//
//  1. the channel-attached canvas tab — conversations.info
//     `properties.canvas`, tried on every available identity (bot vs
//     user visibility differs);
//  2. canvas-type FILES shared into the channel — files.list
//     types=canvas — because a "canvas in the channel" is often a
//     standalone document posted there, which never appears on
//     properties.canvas.
//
// The body is then fetched like any other Slack file (files.info →
// download url_private), reusing the audio pipeline's authenticated
// download primitive.
// canvasSelector narrows which of a channel's canvases to read.
// Meeting-notes canvases are conventionally titled with a date
// ("23.07.2026 Tech Meet"), so `date` matches common spellings of a day
// against titles; `match` is a plain substring; listOnly skips the
// download entirely.
type canvasSelector struct {
	date     string // YYYY-MM-DD
	match    string
	listOnly bool
}

func (s canvasSelector) active() bool { return s.date != "" || s.match != "" || s.listOnly }

func (h *Hub) readCanvas(ctx context.Context, channel string, sel canvasSelector) (string, error) {
	channelID, err := h.resolveConversation(ctx, channel)
	if err != nil {
		return "", err
	}

	if sel.active() {
		return h.readCanvasSelected(ctx, channel, channelID, sel)
	}

	var file goslack.File
	fileID, isEmpty, err := h.Canvas().ChannelCanvas(ctx, channelID)
	switch {
	case err != nil:
		return "", err
	case isEmpty && fileID != "":
		return fmt.Sprintf("Canvas for #%s is empty.", channel), nil
	case fileID != "":
		f, ferr := h.Messages().FileInfo(ctx, fileID)
		if ferr != nil {
			return "", fmt.Errorf("canvas files.info (%s): %w", fileID, ferr)
		}
		file = f
	default:
		// No attached canvas tab — fall back to canvases shared into the
		// channel as files.
		files, ferr := h.Canvas().CanvasFiles(ctx, channelID)
		if ferr != nil {
			return "", fmt.Errorf("canvas lookup (files.list): %w", ferr)
		}
		newest := pickNewestCanvas(files)
		if newest == nil {
			return "", fmt.Errorf("no canvas found in #%s — neither a channel canvas tab nor a shared canvas file", channel)
		}
		file = *newest
	}
	return h.renderCanvasFile(ctx, channel, file)
}

// readCanvasSelected gathers the channel's FULL canvas set (shared files
// ∪ the attached canvas tab, deduped) and applies the selector. A miss
// is an ANSWER, not an error: "no canvas for that date" plus the list of
// canvases that do exist — one call resolves "did the meeting notes for
// day X land?" either way.
func (h *Hub) readCanvasSelected(ctx context.Context, channel, channelID string, sel canvasSelector) (string, error) {
	files, err := h.Canvas().CanvasFiles(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("canvas lookup (files.list): %w", err)
	}
	if tabID, _, terr := h.Canvas().ChannelCanvas(ctx, channelID); terr == nil && tabID != "" {
		seen := false
		for _, f := range files {
			if f.ID == tabID {
				seen = true
				break
			}
		}
		if !seen {
			if f, ferr := h.Messages().FileInfo(ctx, tabID); ferr == nil {
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		return fmt.Sprintf("no canvases in #%s at all", channel), nil
	}

	if sel.listOnly {
		return fmt.Sprintf("# Canvases in #%s\n%s", channel, renderCanvasList(files)), nil
	}

	variants, verr := canvasDateVariants(sel.date)
	if verr != nil {
		return "", verr
	}
	pick := selectCanvas(files, sel.match, variants)
	if pick == nil {
		want := sel.match
		if sel.date != "" {
			if want != "" {
				want += " + "
			}
			want += sel.date
		}
		return fmt.Sprintf("no canvas matching %q in #%s; available:\n%s", want, channel, renderCanvasList(files)), nil
	}
	return h.renderCanvasFile(ctx, channel, *pick)
}

// renderCanvasFile downloads a resolved canvas file and renders it.
func (h *Hub) renderCanvasFile(ctx context.Context, channel string, file goslack.File) (string, error) {
	dl := file.URLPrivateDownload
	if dl == "" {
		dl = file.URLPrivate
	}
	if dl == "" {
		return "", fmt.Errorf("canvas file %s has no download URL (needs files:read?)", file.ID)
	}

	var buf bytes.Buffer
	if err := h.Messages().DownloadFile(ctx, dl, &buf); err != nil {
		return "", fmt.Errorf("canvas download: %w", err)
	}

	body, truncated := canvasToText(buf.Bytes(), file.Mimetype, canvasMaxBytes)
	// Resolve mentions like every other read surface does. Without this a
	// canvas renders raw IDs, which is unreadable on its own and actively
	// misleading next to the unread summary's "mentions you" flag: the
	// reader is told they were named and then cannot find where.
	body = format.RenderCanvasText(body, h.resolveTextRefs(ctx, body))
	title := strings.TrimSpace(file.Title)
	if title == "" {
		title = "Canvas"
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# %s — #%s\n\n%s", title, channel, body)
	if truncated {
		out.WriteString("\n\n_(canvas truncated)_")
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// canvasDateVariants expands an ISO date into the spellings meeting
// canvases actually use in titles: "2026-07-23" → 23.07.2026, 23.07.26,
// 2026-07-23, 23.07, 23/07/2026, plus unpadded 23.7.2026 / 23.7 when
// applicable. Empty input → nil, nil (no date filtering). Pure.
func canvasDateVariants(iso string) ([]string, error) {
	if iso == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return nil, fmt.Errorf("date must be YYYY-MM-DD: %w", err)
	}
	variants := []string{
		t.Format("02.01.2006"),
		t.Format("02.01.06"),
		t.Format("2006-01-02"),
		t.Format("02.01"),
		t.Format("02/01/2006"),
	}
	// Unpadded day/month spellings ("23.7.2026", "2.07") — people type
	// them; add only when they differ from the padded forms.
	unpadded := fmt.Sprintf("%d.%d.%d", t.Day(), int(t.Month()), t.Year())
	if unpadded != variants[0] {
		variants = append(variants, unpadded, fmt.Sprintf("%d.%d", t.Day(), int(t.Month())))
	}
	return variants, nil
}

// selectCanvas picks the newest file whose title satisfies BOTH the
// substring match (case-insensitive; empty = any) and the date variants
// (title contains any variant; nil = any). Pure.
func selectCanvas(files []goslack.File, match string, dateVariants []string) *goslack.File {
	match = strings.ToLower(match)
	var matched []goslack.File
	for _, f := range files {
		title := strings.ToLower(f.Title)
		if match != "" && !strings.Contains(title, match) {
			continue
		}
		if len(dateVariants) > 0 {
			ok := false
			for _, v := range dateVariants {
				if strings.Contains(f.Title, v) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		matched = append(matched, f)
	}
	return pickNewestCanvas(matched)
}

// renderCanvasList renders one line per canvas: title + created date,
// newest first. Pure.
func renderCanvasList(files []goslack.File) string {
	sorted := append([]goslack.File{}, files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Created > sorted[j].Created })
	var b strings.Builder
	for _, f := range sorted {
		title := strings.TrimSpace(f.Title)
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "- %s (created %s)\n", title, time.Unix(int64(f.Created), 0).Format("2006-01-02"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// pickNewestCanvas returns the most recently created file, or nil for
// an empty list. Pure — files.list order is not contractual, so we sort
// by Created ourselves.
func pickNewestCanvas(files []goslack.File) *goslack.File {
	var newest *goslack.File
	for i := range files {
		if newest == nil || files[i].Created > newest.Created {
			newest = &files[i]
		}
	}
	return newest
}

var (
	reCanvasScript = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	reCanvasBreak  = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/h[1-6]|/tr)\s*/?>`)
	reCanvasLI     = regexp.MustCompile(`(?i)<li[^>]*>`)
	reCanvasTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reCanvasBlanks = regexp.MustCompile(`\n{3,}`)
)

// canvasToText renders raw canvas bytes to readable text. Slack canvases
// download as HTML (older) or markdown (newer); this reduces HTML to
// text and passes markdown/plain through untouched. Enforces a byte cap
// and reports whether it truncated. Pure — unit-tested without any API.
func canvasToText(raw []byte, mimetype string, maxBytes int) (text string, truncated bool) {
	if len(raw) > maxBytes {
		raw = raw[:maxBytes]
		truncated = true
	}
	s := string(raw)

	looksHTML := strings.Contains(strings.ToLower(mimetype), "html") ||
		strings.Contains(s, "</") || strings.Contains(s, "<p") || strings.Contains(s, "<div")
	if looksHTML {
		s = reCanvasScript.ReplaceAllString(s, "")
		s = reCanvasLI.ReplaceAllString(s, "• ")
		s = reCanvasBreak.ReplaceAllString(s, "\n")
		s = reCanvasTag.ReplaceAllString(s, "")
		s = html.UnescapeString(s)
	}

	// Normalise whitespace: trim per-line trailing space, collapse 3+
	// blank lines to a paragraph break.
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t\r")
	}
	s = reCanvasBlanks.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(s), truncated
}
