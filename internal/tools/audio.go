package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// registerAudioTools wires download_audio — fetching voice-note (or any
// audio) attachments from a message to local temp files so the MCP
// client can transcribe them with a local speech-to-text tool (e.g.
// whisper). The Slack token stays inside the server process: only a
// local file path crosses back to the client, never credentials.
func (h *Hub) registerAudioTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("download_audio") {
		return
	}
	s.AddTool(
		mcp.NewTool("download_audio",
			mcp.WithDescription("Download audio attachments (voice messages) from a Slack message into local temp files for transcription. Pass a permalink, or channel + timestamp."),
			mcp.WithString("permalink", mcp.Description("Slack message permalink — resolves channel and timestamp in one go")),
			mcp.WithString("channel", mcp.Description("Channel name or ID (optional if permalink is provided)")),
			mcp.WithString("timestamp", mcp.Description("Message ts holding the audio (optional if permalink is provided)")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runDownloadAudio(ctx,
				req.GetString("workspace", ""),
				req.GetString("channel", ""),
				req.GetString("timestamp", ""),
				req.GetString("permalink", ""),
				""), nil
		},
	)
}

// isAudioFile reports whether a Slack attachment is audio. Voice notes
// arrive as audio/mp4 (.m4a); the mimetype prefix also covers uploaded
// mp3 / ogg / wav / flac without enumerating filetypes.
func isAudioFile(f goslack.File) bool {
	return strings.HasPrefix(f.Mimetype, "audio/")
}

// sanitizeFilename keeps a filename shell- and filesystem-safe: only
// alphanumerics, dot, dash, and underscore survive; everything else
// (spaces, path separators, unicode) collapses to '_'. An empty result
// falls back to "audio" so the joined path never ends in a bare prefix.
func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "audio"
	}
	return out
}

// looksLikeHTML sniffs the first bytes of a downloaded file. Slack
// serves its sign-in page with HTTP 200 when the token lacks the
// files:read scope, so a "successful" download can silently be an HTML
// document — without this guard it would be handed to a transcriber as
// corrupt audio.
func looksLikeHTML(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	head := strings.TrimSpace(strings.ToLower(string(buf[:n])))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}

// savedAudio describes one downloaded attachment for rendering.
type savedAudio struct {
	Path     string
	Mimetype string
	Size     int64
}

// downloadAudioFiles filters files down to the audio attachments and
// streams each into destDir. Factored free of the Hub (taking the
// MessageClient contract) so tests exercise the filter/IO loop with a
// fake client and a t.TempDir(). Non-audio files are reported in
// skipped, not treated as errors — a voice note often travels with an
// image preview and the caller only cares about the sound.
func downloadAudioFiles(ctx context.Context, msgs MessageClient, files []goslack.File, destDir string) (saved []savedAudio, skipped []string, err error) {
	for _, f := range files {
		if !isAudioFile(f) {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", f.Name, f.Mimetype))
			continue
		}
		url := f.URLPrivateDownload
		if url == "" {
			url = f.URLPrivate
		}
		if url == "" {
			skipped = append(skipped, f.Name+" (no private URL)")
			continue
		}
		path := filepath.Join(destDir, fmt.Sprintf("slk-audio-%s-%s", f.ID, sanitizeFilename(f.Name)))
		out, cerr := os.Create(path)
		if cerr != nil {
			return nil, skipped, fmt.Errorf("create %s: %w", path, cerr)
		}
		if derr := msgs.DownloadFile(ctx, url, out); derr != nil {
			out.Close()
			os.Remove(path)
			return nil, skipped, fmt.Errorf("download %s: %w", f.Name, derr)
		}
		if cerr := out.Close(); cerr != nil {
			return nil, skipped, fmt.Errorf("close %s: %w", path, cerr)
		}
		if looksLikeHTML(path) {
			os.Remove(path)
			return nil, skipped, fmt.Errorf("download %s: Slack returned an HTML sign-in page instead of the file — the token is missing the files:read scope (add it under OAuth & Permissions and reinstall the app)", f.Name)
		}
		var size int64
		if info, serr := os.Stat(path); serr == nil {
			size = info.Size()
		}
		saved = append(saved, savedAudio{Path: path, Mimetype: f.Mimetype, Size: size})
	}
	return saved, skipped, nil
}

// fetchAudioFiles is the shared front half of download_audio and
// transcribe_audio: resolve the workspace and target message (permalink
// wins the usual way: explicit args override, permalink fills gaps),
// then download the audio attachments into destDir. On failure the
// returned *mcp.CallToolResult is non-nil and ready to hand back.
func (h *Hub) fetchAudioFiles(ctx context.Context, workspace, channel, timestamp, permalink, destDir string) (saved []savedAudio, skipped []string, wsName string, errRes *mcp.CallToolResult) {
	ws := h.workspaceTarget(workspace)
	if ws == nil {
		return nil, nil, "", mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	scoped := h.withClient(ws.Client)

	if strings.TrimSpace(permalink) != "" {
		p, err := slack.ParseSlackPermalink(permalink)
		if err != nil {
			return nil, nil, "", mcp.NewToolResultError("permalink could not be parsed: " + err.Error())
		}
		if p != nil {
			if channel == "" {
				channel = p.ChannelID
			}
			if timestamp == "" {
				timestamp = p.TS
			}
		}
	}
	if channel == "" || timestamp == "" {
		return nil, nil, "", mcp.NewToolResultError("provide a permalink, or channel + timestamp")
	}

	channelID, err := scoped.Channels().ResolveID(ctx, channel)
	if err != nil {
		return nil, nil, "", mcp.NewToolResultError(err.Error())
	}
	msg, err := scoped.Messages().MessageAt(ctx, channelID, timestamp)
	if err != nil {
		return nil, nil, "", mcp.NewToolResultError(err.Error())
	}
	if len(msg.Files) == 0 {
		return nil, nil, "", mcp.NewToolResultError("message has no file attachments")
	}

	if destDir == "" {
		destDir = os.TempDir()
	}
	saved, skipped, err = downloadAudioFiles(ctx, scoped.Messages(), msg.Files, destDir)
	if err != nil {
		return nil, nil, "", mcp.NewToolResultError(err.Error())
	}
	if len(saved) == 0 {
		return nil, nil, "", mcp.NewToolResultError("no audio attachments on this message; files present: " + strings.Join(skipped, ", "))
	}
	return saved, skipped, ws.Name, nil
}

// runDownloadAudio downloads a message's audio attachments and reports
// their local paths. destDir overrides os.TempDir() in tests; the tool
// handler passes "".
func (h *Hub) runDownloadAudio(ctx context.Context, workspace, channel, timestamp, permalink, destDir string) *mcp.CallToolResult {
	saved, skipped, wsName, errRes := h.fetchAudioFiles(ctx, workspace, channel, timestamp, permalink, destDir)
	if errRes != nil {
		return errRes
	}

	var b strings.Builder
	fmt.Fprintf(&b, "downloaded %d audio file(s)%s:\n", len(saved), h.wsLabel(wsName))
	for _, s := range saved {
		fmt.Fprintf(&b, "- %s (%s, %d bytes)\n", s.Path, s.Mimetype, s.Size)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "skipped non-audio: %s\n", strings.Join(skipped, ", "))
	}
	b.WriteString("transcribe locally, e.g.: whisper-cli -m <model.bin> -l <lang> <path>")
	return mcp.NewToolResultText(b.String())
}
