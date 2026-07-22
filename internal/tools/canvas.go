package tools

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
			mcp.WithDescription("Read the canvas attached to a channel (or DM). Slack canvases are file-backed documents, not messages, so a regular digest only shows they exist — this resolves the channel's canvas, downloads it, and returns its text."),
			mcp.WithString("channel", mcp.Required(), mcp.Description("Channel name (#devops or devops), a DM as @handle or bare U… user id, or a canonical C/G/D conversation id")),
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
			txt, err := scoped.readCanvas(ctx, channel)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(txt), nil
		},
	)
}

// readCanvas resolves a channel's canvas file, downloads it, and renders
// its text. The canvas file_id lives on conversations.info
// (properties.canvas); the body is fetched like any other Slack file
// (files.info → download url_private), reusing the audio pipeline's
// authenticated download primitive.
func (h *Hub) readCanvas(ctx context.Context, channel string) (string, error) {
	channelID, err := h.resolveConversation(ctx, channel)
	if err != nil {
		return "", err
	}
	info, err := h.Channels().Info(ctx, channelID)
	if err != nil {
		return "", err
	}
	if info.Properties == nil || info.Properties.Canvas.FileId == "" {
		return "", fmt.Errorf("no canvas attached to #%s", channel)
	}
	if info.Properties.Canvas.IsEmpty {
		return fmt.Sprintf("Canvas for #%s is empty.", channel), nil
	}

	fileID := info.Properties.Canvas.FileId
	file, err := h.Messages().FileInfo(ctx, fileID)
	if err != nil {
		return "", fmt.Errorf("canvas files.info (%s): %w", fileID, err)
	}
	dl := file.URLPrivateDownload
	if dl == "" {
		dl = file.URLPrivate
	}
	if dl == "" {
		return "", fmt.Errorf("canvas file %s has no download URL (needs files:read?)", fileID)
	}

	var buf bytes.Buffer
	if err := h.Messages().DownloadFile(ctx, dl, &buf); err != nil {
		return "", fmt.Errorf("canvas download: %w", err)
	}

	body, truncated := canvasToText(buf.Bytes(), file.Mimetype, canvasMaxBytes)
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
