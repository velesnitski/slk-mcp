package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// errFilesReadScope marks a download that came back as Slack's HTML
// sign-in page (HTTP 200) instead of the file — the tell-tale of a token
// without files:read. It's a sentinel so finishFetch can rewrite it into
// a workspace-aware hint (which workspace's token to fix), rather than
// leaking a generic message that doesn't say where to look.
var errFilesReadScope = errors.New("slack returned an HTML sign-in page instead of the file — token missing files:read")

// audioScopeMarkers are the substrings Slack uses for authorization
// failures the audio/image pipeline can hit. Matching these (and only
// these) keeps a genuine not-found / transient error from being
// mislabelled as a scope problem.
var audioScopeMarkers = []string{
	"missing_scope",
	"not_allowed_token_type",
	"no_permission",
	"not_authed",
	"invalid_auth",
	"token_revoked",
	"account_inactive",
}

// looksLikeScopeError reports whether err is a Slack authorization
// failure (missing OAuth scope or unusable token) rather than something
// transient or a plain not-found — so only these earn the scope hint.
func looksLikeScopeError(err error) bool {
	if err == nil {
		return false
	}
	e := strings.ToLower(err.Error())
	for _, marker := range audioScopeMarkers {
		if strings.Contains(e, marker) {
			return true
		}
	}
	return false
}

// audioScopeError turns a Slack authorization failure from the
// audio/image surface into a workspace-aware, actionable message naming
// the scopes the pipeline needs. Non-scope errors pass through verbatim
// so real failures aren't reframed. wsLabel is the " [name]" suffix
// (empty in single-workspace mode). Mirrors statusErrorHint (status.go)
// for the file surface — the failure itself is the cheapest scope probe,
// so we decorate on failure rather than pay an extra round-trip up front.
func audioScopeError(wsLabel string, err error) string {
	e := err.Error()
	if !looksLikeScopeError(err) {
		return e
	}
	return fmt.Sprintf("%s — the%s workspace token is missing an OAuth scope for this operation. The audio/image tools need files:read (download the file), im:history (read a DM to find the latest note), and users:read + im:write (resolve an @handle to a DM). Add the missing scope under OAuth & Permissions, reinstall the app, then refresh the token.", e, wsLabel)
}

// scopeResult wraps an error as a tool result, decorating Slack
// authorization failures with the workspace-aware scope hint.
func (h *Hub) scopeResult(wsName string, err error) *mcp.CallToolResult {
	return mcp.NewToolResultError(audioScopeError(h.wsLabel(wsName), err))
}

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
			mcp.WithDescription("Download audio attachments (voice messages) from a Slack message into local temp files for transcription. Pass a permalink, or channel + timestamp, or just a channel/DM to grab its newest voice note."),
			mcp.WithString("permalink", mcp.Description("Slack message permalink (…/archives/…/p…) OR a Slack file URL (…/files/…/F…/name) — either resolves the attachment on its own")),
			mcp.WithString("channel", mcp.Description("Channel name or ID, or a DM as @handle (optional if permalink is provided). With no timestamp, the newest matching attachment in this conversation is used.")),
			mcp.WithString("timestamp", mcp.Description("Message ts holding the audio (optional if permalink is provided)")),
			mcp.WithString("from", mcp.Description("Restrict latest-mode to one author: a @handle, or \"me\" for your own last voice note. Ignored when a permalink/timestamp is given.")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runDownloadAudio(ctx,
				req.GetString("workspace", ""),
				req.GetString("channel", ""),
				req.GetString("timestamp", ""),
				req.GetString("permalink", ""),
				req.GetString("from", ""),
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

// isImageFile reports whether a Slack attachment is an image
// (image/png, image/jpeg, …). Used by view_image to filter a message's
// attachments down to the pictures the caller wants to see.
func isImageFile(f goslack.File) bool {
	return strings.HasPrefix(f.Mimetype, "image/")
}

// isTranscribableFile widens isAudioFile to video: recorded huddles and
// Slack clips arrive as video/mp4|webm, and ffmpeg extracts the audio
// track with the same command that converts voice notes. Used by
// transcribe_audio only — download_audio keeps its literal contract of
// fetching audio files.
func isTranscribableFile(f goslack.File) bool {
	return isAudioFile(f) || strings.HasPrefix(f.Mimetype, "video/")
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

// confinedTempPath builds the local temp path for a downloaded
// attachment and proves it stays inside destDir. Both untrusted inputs
// — the Slack file ID and name — are run through sanitizeFilename so a
// slash or ".." cannot survive into the joined path, and a final
// containment check (filepath.Rel must not escape) is defence-in-depth
// against any future format change: a write must never land outside the
// intended directory. Mirrors the yt-mcp ADR-027 confinement pattern
// after GHSA-99mq-fjjc-6v9j flagged caller-influenced paths as a class.
func confinedTempPath(destDir, prefix, fileID, fileName string) (string, error) {
	base := fmt.Sprintf("%s-%s-%s", prefix, sanitizeFilename(fileID), sanitizeFilename(fileName))
	path := filepath.Join(destDir, base)
	rel, err := filepath.Rel(destDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("refusing to write outside the temp dir: %q", base)
	}
	return path, nil
}

// savedFile describes one downloaded attachment for rendering.
type savedFile struct {
	Path     string
	Mimetype string
	Size     int64
}

// downloadFiles filters files down to the attachments accepted by
// the caller's predicate and streams each into destDir. Factored free
// of the Hub (taking the MessageClient contract) so tests exercise the
// filter/IO loop with a fake client and a t.TempDir(). Rejected files
// are reported in skipped, not treated as errors — a voice note often
// travels with an image preview and the caller only cares about the
// sound.
func downloadFiles(ctx context.Context, msgs MessageClient, files []goslack.File, destDir, prefix string, accept func(goslack.File) bool) (saved []savedFile, skipped []string, err error) {
	for _, f := range files {
		if !accept(f) {
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
		path, perr := confinedTempPath(destDir, prefix, f.ID, f.Name)
		if perr != nil {
			return nil, skipped, perr
		}
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
			return nil, skipped, fmt.Errorf("download %s: %w", f.Name, errFilesReadScope)
		}
		var size int64
		if info, serr := os.Stat(path); serr == nil {
			size = info.Size()
		}
		saved = append(saved, savedFile{Path: path, Mimetype: f.Mimetype, Size: size})
	}
	return saved, skipped, nil
}

// fetchFiles is the shared front half of download_audio and
// transcribe_audio: resolve the workspace and target message (permalink
// wins the usual way: explicit args override, permalink fills gaps),
// then download the attachments matched by accept into destDir. On
// failure the returned *mcp.CallToolResult is non-nil and ready to
// hand back.
func (h *Hub) fetchFiles(ctx context.Context, workspace, channel, timestamp, permalink, from, destDir, prefix string, accept func(goslack.File) bool) (saved []savedFile, skipped []string, wsName string, errRes *mcp.CallToolResult) {
	scoped, wsName, errRes := h.scopedWorkspace(workspace)
	if errRes != nil {
		return nil, nil, "", errRes
	}

	if destDir == "" {
		destDir = os.TempDir()
	}

	// File-URL fast path: a "…/files/<user>/<F…>/name" link points
	// straight at an attachment, so resolve it by ID (files.info) and
	// skip channel/message lookup entirely. This is what a right-click
	// "Copy link" on an uploaded voice memo gives — search never
	// indexes those, so a message permalink isn't always obtainable.
	if fileID, ok := slack.ParseSlackFileURL(permalink); ok {
		f, ferr := scoped.Messages().FileInfo(ctx, fileID)
		if ferr != nil {
			return nil, nil, "", h.scopeResult(wsName, ferr)
		}
		return finishFetch(ctx, scoped, []goslack.File{f}, destDir, prefix, accept, wsName)
	}

	// Latest-mode: channel alone (no permalink, no ts) means "the newest
	// matching attachment in this conversation" — the "read my last voice
	// note in this DM" path, with no ts to hunt for. `from` restricts by
	// author (a handle, or "me").
	if strings.TrimSpace(permalink) == "" && strings.TrimSpace(timestamp) == "" && strings.TrimSpace(channel) != "" {
		channelID, cerr := scoped.resolveConversation(ctx, channel)
		if cerr != nil {
			return nil, nil, "", h.scopeResult(wsName, cerr)
		}
		fromID, ferr := scoped.resolveAuthor(ctx, from)
		if ferr != nil {
			return nil, nil, "", h.scopeResult(wsName, ferr)
		}
		msg, merr := scoped.Messages().LatestFileMessage(ctx, channelID, accept, fromID)
		if merr != nil {
			return nil, nil, "", h.scopeResult(wsName, merr)
		}
		return finishFetch(ctx, scoped, msg.Files, destDir, prefix, accept, wsName)
	}

	channel, timestamp, errRes = resolveMessageRef(permalink, channel, timestamp, false)
	if errRes != nil {
		return nil, nil, "", errRes
	}

	channelID, err := scoped.Channels().ResolveID(ctx, channel)
	if err != nil {
		return nil, nil, "", h.scopeResult(wsName, err)
	}
	msg, err := scoped.Messages().MessageAt(ctx, channelID, timestamp)
	if err != nil {
		return nil, nil, "", h.scopeResult(wsName, err)
	}
	// The resolved message carries a matching attachment directly.
	if hasMatchingFile(msg.Files, accept) {
		return finishFetch(ctx, scoped, msg.Files, destDir, prefix, accept, wsName)
	}
	// Otherwise the attachment may live on a reply in the same thread —
	// the common case for a voice note when the caller only has the
	// thread-root (or a sibling) permalink, whose own message has no
	// audio. Scan the thread for the newest matching attachment. A root
	// with replies carries ThreadTimestamp == its own ts; a bare message
	// falls back to its own ts (a one-message "thread" that simply finds
	// nothing).
	threadTS := msg.ThreadTimestamp
	if threadTS == "" {
		threadTS = msg.Timestamp
	}
	tmsg, terr := scoped.Messages().LatestFileInThread(ctx, channelID, threadTS, accept)
	if terr != nil {
		return nil, nil, "", mcp.NewToolResultError("this message has no matching attachment, and neither does any reply in its thread")
	}
	return finishFetch(ctx, scoped, tmsg.Files, destDir, prefix, accept, wsName)
}

// hasMatchingFile reports whether any of files satisfies accept — the
// "does this exact message carry the attachment we want?" check that
// decides whether fetchFiles uses the message directly or falls back to
// scanning its thread.
func hasMatchingFile(files []goslack.File, accept func(goslack.File) bool) bool {
	for _, f := range files {
		if accept(f) {
			return true
		}
	}
	return false
}

// finishFetch downloads the matched attachments and shapes the shared
// (saved, skipped, wsName, err) return, so every resolution path
// (file URL, latest-mode, message) ends the same way.
func finishFetch(ctx context.Context, scoped *Hub, files []goslack.File, destDir, prefix string, accept func(goslack.File) bool, wsName string) ([]savedFile, []string, string, *mcp.CallToolResult) {
	saved, skipped, err := downloadFiles(ctx, scoped.Messages(), files, destDir, prefix, accept)
	if err != nil {
		if errors.Is(err, errFilesReadScope) {
			return nil, nil, "", mcp.NewToolResultError(fmt.Sprintf("the%s workspace token is missing the files:read scope — Slack returned its sign-in page instead of the file. Add files:read under OAuth & Permissions, reinstall the app, then refresh the token.", scoped.wsLabel(wsName)))
		}
		return nil, nil, "", mcp.NewToolResultError(err.Error())
	}
	if len(saved) == 0 {
		return nil, nil, "", mcp.NewToolResultError("no matching attachment; files present: " + strings.Join(skipped, ", "))
	}
	return saved, skipped, wsName, nil
}

// runDownloadAudio downloads a message's audio attachments and reports
// their local paths. destDir overrides os.TempDir() in tests; the tool
// handler passes "".
func (h *Hub) runDownloadAudio(ctx context.Context, workspace, channel, timestamp, permalink, from, destDir string) *mcp.CallToolResult {
	// isTranscribableFile, not isAudioFile: a recorded huddle or Slack
	// video clip IS the voice message the caller means, and refusing it
	// with "no matching attachment" while transcribe_audio happily
	// resolves the same permalink is a contradiction the caller cannot
	// diagnose.
	saved, skipped, wsName, errRes := h.fetchFiles(ctx, workspace, channel, timestamp, permalink, from, destDir, "slk-audio", isTranscribableFile)
	if errRes != nil {
		return errRes
	}

	var b strings.Builder
	fmt.Fprintf(&b, "downloaded %d media file(s)%s:\n", len(saved), h.wsLabel(wsName))
	for _, s := range saved {
		fmt.Fprintf(&b, "- %s (%s, %d bytes)\n", s.Path, s.Mimetype, s.Size)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "skipped non-audio: %s\n", strings.Join(skipped, ", "))
	}
	b.WriteString("transcribe locally, e.g.: whisper-cli -m <model.bin> -l <lang> <path>")
	return mcp.NewToolResultText(b.String())
}
