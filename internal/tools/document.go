package tools

import (
	"context"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
)

// docMaxChars caps how much of a document is returned inline. Proposals
// and exported pages routinely run past a model's useful window, and a
// silent flood is worse than an explicit truncation note.
const docMaxChars = 40_000

// registerDocumentTools wires read_document — the textual counterpart of
// view_image. Slack conversations carry decisions as attachments (an
// exported HTML proposal, a .md spec, a .csv export), and without this
// the only readable attachments were pictures and audio: a colleague's
// "tell me what you think" about an .html file could not be answered
// without leaving the session.
func (h *Hub) registerDocumentTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("read_document") {
		return
	}
	s.AddTool(
		mcp.NewTool("read_document",
			mcp.WithDescription("Read text-based attachments (HTML, Markdown, TXT, CSV, JSON, source files) from a Slack message and return their contents inline. HTML is converted to plain text. Pass a permalink, or channel + timestamp, or just a channel/DM to grab its newest document. Use view_image for pictures and transcribe_audio for voice notes."),
			mcp.WithString("permalink", mcp.Description("Slack message permalink (…/archives/…/p…) OR a Slack file URL (…/files/…/F…/name) — either resolves the attachment on its own")),
			mcp.WithString("channel", mcp.Description("Channel name or ID, or a DM as @handle (optional if permalink is provided). With no timestamp, the newest matching attachment in this conversation is used.")),
			mcp.WithString("timestamp", mcp.Description("Message ts holding the document (optional if permalink is provided)")),
			mcp.WithString("from", mcp.Description("Restrict latest-mode to one author: a @handle, or \"me\" for your own last document. Ignored when a permalink/timestamp is given.")),
			mcp.WithString("match", mcp.Description("Pick by filename instead of recency: case-insensitive substring of the attachment name (e.g. \"playbook\"). Use when two documents were posted together and you need the earlier one.")),
			mcp.WithNumber("limit", mcp.Description("How many recent documents to return (default: 1). Raise it to read several attachments from one conversation in a single call.")),
			mcp.WithBoolean("list_only", mcp.Description("List the recent documents (name, type, size, timestamp) without downloading them, so you can choose which to read.")),
			mcp.WithNumber("max_chars", mcp.Description("Per-document inline cap (default: 40000). Truncation is reported explicitly, never silent.")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runReadDocument(ctx,
				req.GetString("workspace", ""),
				req.GetString("channel", ""),
				req.GetString("timestamp", ""),
				req.GetString("permalink", ""),
				req.GetString("from", ""),
				req.GetString("match", ""),
				req.GetInt("limit", 1),
				req.GetBool("list_only", false),
				req.GetInt("max_chars", docMaxChars)), nil
		},
	)
}

// runReadDocument downloads the matching attachments into a private temp
// dir, renders each as plain text, and removes the files before
// returning — unlike download_audio, nothing here needs to outlive the
// call, so no artifact is left on disk.
func (h *Hub) runReadDocument(ctx context.Context, workspace, channel, timestamp, permalink, from, match string, limit int, listOnly bool, maxChars int) *mcp.CallToolResult {
	if maxChars <= 0 {
		maxChars = docMaxChars
	}
	// Selector mode. "The newest attachment" is the wrong answer when two
	// documents were posted seconds apart and the caller wants the
	// earlier one — reachable before only by hunting down its timestamp.
	// A permalink or timestamp still names an exact message and wins.
	if strings.TrimSpace(permalink) == "" && strings.TrimSpace(timestamp) == "" &&
		strings.TrimSpace(channel) != "" &&
		(strings.TrimSpace(match) != "" || listOnly || limit > 1) {
		return h.readRecentDocuments(ctx, workspace, channel, from, match, limit, listOnly, maxChars)
	}

	destDir, err := os.MkdirTemp("", "slk-doc-")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("temp dir: %v", err))
	}
	defer os.RemoveAll(destDir)

	saved, skipped, wsName, errRes := h.fetchFiles(ctx, workspace, channel, timestamp, permalink, from, destDir, "slk-doc", isDocumentFile)
	if errRes != nil {
		return errRes
	}

	body := renderDocuments(saved, h.wsLabel(wsName), maxChars)
	if len(skipped) > 0 {
		body += fmt.Sprintf("\nnot a document, skipped: %s\n", strings.Join(skipped, ", "))
	}
	return mcp.NewToolResultText(body)
}

// docScanLimit caps how many document-bearing messages the selector
// considers. Generous enough to cover a working day in a busy channel,
// bounded so a quiet `match` typo cannot walk the whole history.
const docScanLimit = 25

// docCandidate pairs an attachment with the message that carries it, so
// a listing can hand back the timestamp needed to fetch it exactly.
type docCandidate struct {
	File goslack.File
	TS   string
}

// readRecentDocuments resolves several documents from one conversation
// and either lists them or reads the first `limit`.
func (h *Hub) readRecentDocuments(ctx context.Context, workspace, channel, from, match string, limit int, listOnly bool, maxChars int) *mcp.CallToolResult {
	scoped, wsName, errRes := h.scopedWorkspace(workspace)
	if errRes != nil {
		return errRes
	}
	channelID, cerr := scoped.resolveConversation(ctx, channel)
	if cerr != nil {
		return h.scopeResult(wsName, cerr)
	}
	fromID, ferr := scoped.resolveAuthor(ctx, from)
	if ferr != nil {
		return h.scopeResult(wsName, ferr)
	}
	msgs, merr := scoped.Messages().RecentFileMessages(ctx, channelID, isDocumentFile, fromID, docScanLimit)
	if merr != nil {
		return h.scopeResult(wsName, merr)
	}

	candidates := collectDocuments(msgs, match)
	if len(candidates) == 0 {
		if strings.TrimSpace(match) != "" {
			return mcp.NewToolResultError(fmt.Sprintf("no recent document matches %q in this conversation — call again with list_only=true to see what is there", match))
		}
		return mcp.NewToolResultError("no recent document in this conversation")
	}
	if listOnly {
		return mcp.NewToolResultText(renderDocumentList(candidates, h.wsLabel(wsName)))
	}
	if limit <= 0 {
		limit = 1
	}
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	destDir, err := os.MkdirTemp("", "slk-doc-")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("temp dir: %v", err))
	}
	defer os.RemoveAll(destDir)

	files := make([]goslack.File, 0, len(candidates))
	for _, c := range candidates {
		files = append(files, c.File)
	}
	saved, _, derr := downloadFiles(ctx, scoped.Messages(), files, destDir, "slk-doc", isDocumentFile)
	if derr != nil {
		return mcp.NewToolResultError(derr.Error())
	}
	return mcp.NewToolResultText(renderDocuments(saved, h.wsLabel(wsName), maxChars))
}

// collectDocuments flattens the matching attachments out of messages
// (already newest-first), keeping only those whose filename contains
// match when one is given. Pure.
func collectDocuments(msgs []goslack.Message, match string) []docCandidate {
	needle := strings.ToLower(strings.TrimSpace(match))
	var out []docCandidate
	for i := range msgs {
		for _, f := range msgs[i].Files {
			if !isDocumentFile(f) {
				continue
			}
			if needle != "" && !strings.Contains(strings.ToLower(f.Name), needle) {
				continue
			}
			out = append(out, docCandidate{File: f, TS: msgs[i].Timestamp})
		}
	}
	return out
}

// renderDocumentList shows what is available without spending a download
// on it, including each message ts so the caller can fetch one exactly.
// Pure.
func renderDocumentList(candidates []docCandidate, wsLabel string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d document(s)%s, newest first:\n", len(candidates), wsLabel)
	for _, c := range candidates {
		fmt.Fprintf(&b, "- %s (%s, %d bytes) ts=%s\n", c.File.Name, c.File.Mimetype, c.File.Size, c.TS)
	}
	b.WriteString("\nPass one of these as timestamp=, or narrow with match=, to read it.\n")
	return b.String()
}

// renderDocuments turns downloaded files into the inline text body
// shared by both resolution paths.
func renderDocuments(saved []savedFile, wsLabel string, maxChars int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d document(s)%s:\n", len(saved), wsLabel)
	for _, f := range saved {
		raw, rerr := os.ReadFile(f.Path)
		if rerr != nil {
			fmt.Fprintf(&b, "\n--- %s: read failed: %v\n", f.Path, rerr)
			continue
		}
		text := documentToText(string(raw), f.Mimetype)
		text, truncated := truncateText(text, maxChars)
		fmt.Fprintf(&b, "\n--- %s (%s, %d bytes)", displayName(f.Path), f.Mimetype, f.Size)
		if truncated {
			fmt.Fprintf(&b, " — TRUNCATED to %d chars", maxChars)
		}
		b.WriteString(" ---\n")
		b.WriteString(text)
		b.WriteString("\n")
	}
	return b.String()
}

// displayName trims the temp-dir prefix off a saved path so the rendered
// header shows the attachment, not our scratch layout. Pure.
func displayName(path string) string {
	i := strings.LastIndex(path, string(os.PathSeparator))
	if i < 0 {
		return path
	}
	return path[i+1:]
}

// documentFiletypes are Slack `filetype` values that are textual even
// when the mimetype is missing or generic — Slack snippets and posts
// routinely arrive that way.
var documentFiletypes = map[string]bool{
	"text": true, "markdown": true, "md": true, "html": true, "htm": true,
	"json": true, "csv": true, "tsv": true, "yaml": true, "yml": true,
	"xml": true, "log": true, "diff": true, "patch": true, "sql": true,
	"javascript": true, "js": true, "ts": true, "go": true, "python": true,
	"py": true, "shell": true, "sh": true, "php": true, "ruby": true,
	"java": true, "c": true, "cpp": true, "css": true, "post": true,
}

// documentMimetypes are the non-"text/" mimetypes that still carry text.
var documentMimetypes = map[string]bool{
	"application/json": true, "application/xml": true,
	"application/xhtml+xml": true, "application/javascript": true,
	"application/x-yaml": true, "application/yaml": true,
	"application/x-sh": true, "application/sql": true,
}

// isDocumentFile reports whether a Slack attachment is text this tool can
// render. Mimetype decides first; Slack's own filetype is the fallback
// because snippets and posts often ship with an empty mimetype. Pure.
func isDocumentFile(f goslack.File) bool {
	m := strings.ToLower(strings.TrimSpace(f.Mimetype))
	if strings.HasPrefix(m, "text/") || documentMimetypes[m] {
		return true
	}
	return documentFiletypes[strings.ToLower(strings.TrimSpace(f.Filetype))]
}

// expectsTextBody reports whether an attachment is *supposed* to contain
// text. It exists to keep downloadFiles' sign-in-page guard honest: that
// guard rejects any download whose first bytes are HTML, which is exactly
// right for audio/video/images but would reject a genuine .html
// attachment as a scope failure. Pure.
func expectsTextBody(f goslack.File) bool {
	return isDocumentFile(f)
}

var (
	htmlCommentRe     = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlScriptStyleRe = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</\s*(script|style)\s*>`)
	htmlBreakRe       = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li|/tr|/h[1-6]|/section|/article|/table|/ul|/ol)\b[^>]*>`)
	htmlTagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRunRe        = regexp.MustCompile(`[ \t]{2,}`)
	blankRunRe        = regexp.MustCompile(`\n{3,}`)
)

// documentToText renders a downloaded document as plain text: HTML is
// stripped, everything else passes through. The mimetype is a hint, not
// a contract — an exported page is often served as text/plain — so the
// body is sniffed too. Pure.
func documentToText(body, mimetype string) string {
	m := strings.ToLower(mimetype)
	isHTML := strings.Contains(m, "html")
	if !isHTML {
		head := strings.ToLower(strings.TrimSpace(body))
		if len(head) > 1024 {
			head = head[:1024]
		}
		isHTML = strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
	}
	if isHTML {
		return htmlToText(body)
	}
	return strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
}

// htmlToText flattens an HTML document to readable plain text: comments,
// scripts and styles are dropped whole (their contents are not prose),
// block-level closers become newlines so structure survives, remaining
// tags are removed, and entities are decoded. Pure.
func htmlToText(s string) string {
	s = htmlCommentRe.ReplaceAllString(s, "")
	s = htmlScriptStyleRe.ReplaceAllString(s, "")
	s = htmlBreakRe.ReplaceAllString(s, "\n")
	s = htmlTagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = spaceRunRe.ReplaceAllString(s, " ")

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	s = strings.Join(lines, "\n")
	s = blankRunRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// truncateText caps s at max characters (not bytes, so multi-byte text
// is never cut mid-rune) and reports whether it had to. Pure.
func truncateText(s string, max int) (string, bool) {
	if max <= 0 {
		return s, false
	}
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}
