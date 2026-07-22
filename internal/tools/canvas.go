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
	goslack "github.com/slack-go/slack"
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
func (h *Hub) readCanvas(ctx context.Context, channel string) (string, error) {
	channelID, err := h.resolveConversation(ctx, channel)
	if err != nil {
		return "", err
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
