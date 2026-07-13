package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// maxInlineImageBytes caps a single image returned inline as MCP
// ImageContent. base64 inflates by ~33% and MCP hosts bound result
// size, so an oversized image is NOT inlined — the tool returns its
// local path instead and the caller reads/downscales it on its own
// terms. 6 MB comfortably covers phone photos (typically 1–3 MB) while
// refusing to blow the result budget on a raw multi-MB PNG.
const maxInlineImageBytes = 6 << 20

// registerImageTools wires view_image — the visual counterpart of
// transcribe_audio. Where transcribe_audio turns a voice note into
// text, view_image turns a picture attachment into inline image content
// the model can actually see, downloaded with the server's own token.
func (h *Hub) registerImageTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("view_image") {
		return
	}
	s.AddTool(
		mcp.NewTool("view_image",
			mcp.WithDescription("Fetch image attachments (screenshots, photos, business cards) from a Slack message and return them inline so the model can see them. Downloaded with the server's own token (needs files:read). Oversized images fall back to a local file path instead of inlining. Pass a permalink, or channel + timestamp, or just a channel/DM to grab its newest image."),
			mcp.WithString("permalink", mcp.Description("Slack message permalink (…/archives/…/p…) OR a Slack file URL (…/files/…/F…/name) — either resolves the attachment on its own")),
			mcp.WithString("channel", mcp.Description("Channel name or ID, or a DM as @handle (optional if permalink is provided). With no timestamp, the newest matching attachment in this conversation is used.")),
			mcp.WithString("timestamp", mcp.Description("Message ts holding the image(s) (optional if permalink is provided)")),
			mcp.WithString("from", mcp.Description("Restrict latest-mode to one author: a @handle, or \"me\" for your own last image. Ignored when a permalink/timestamp is given.")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runViewImage(ctx,
				req.GetString("workspace", ""),
				req.GetString("channel", ""),
				req.GetString("timestamp", ""),
				req.GetString("permalink", ""),
				req.GetString("from", ""),
				""), nil
		},
	)
}

// runViewImage downloads a message's image attachments and returns them
// as inline MCP image content. Each image under the size cap is
// base64-embedded (and its temp file removed — the bytes are now in the
// response); an oversized one keeps its temp file and is reported by
// path so the caller can read it deliberately. A leading text block
// summarises what was returned so the result is self-describing even
// before the images render.
func (h *Hub) runViewImage(ctx context.Context, workspace, channel, timestamp, permalink, from, destDir string) *mcp.CallToolResult {
	saved, skipped, wsName, errRes := h.fetchFiles(ctx, workspace, channel, timestamp, permalink, from, destDir, "slk-image", isImageFile)
	if errRes != nil {
		return errRes
	}

	var summary strings.Builder
	fmt.Fprintf(&summary, "%d image(s)%s:", len(saved), h.wsLabel(wsName))
	content := []mcp.Content{}

	for _, sf := range saved {
		data, err := os.ReadFile(sf.Path)
		if err != nil {
			fmt.Fprintf(&summary, "\n- %s — read failed: %v", sf.Path, err)
			continue
		}
		if len(data) > maxInlineImageBytes {
			// Too big to inline; leave the file in place and hand back
			// the path so the caller can read/downscale it itself.
			fmt.Fprintf(&summary, "\n- %s (%s, %d bytes) — too large to inline; read the file at this path",
				sf.Path, sf.Mimetype, sf.Size)
			continue
		}
		os.Remove(sf.Path) // bytes are in the response now
		fmt.Fprintf(&summary, "\n- %s (%s, %d bytes) — inlined below", filepath.Base(sf.Path), sf.Mimetype, sf.Size)
		content = append(content, mcp.ImageContent{
			Type:     "image",
			Data:     base64.StdEncoding.EncodeToString(data),
			MIMEType: sf.Mimetype,
		})
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&summary, "\nskipped non-image: %s", strings.Join(skipped, ", "))
	}

	// Text summary first, then the images — so the result reads sensibly
	// even in a client that lists content blocks linearly.
	all := append([]mcp.Content{mcp.TextContent{Type: "text", Text: summary.String()}}, content...)
	return &mcp.CallToolResult{Content: all}
}
