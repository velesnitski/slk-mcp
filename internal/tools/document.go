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
	"github.com/velesnitski/slk-mcp/internal/export"
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
			mcp.WithDescription("Read attachments from a Slack message and return their contents inline: text (HTML, Markdown, TXT, CSV, JSON, source files, and .log/.conf/.ovpn artifacts Slack serves as octet-stream) and .xlsx workbooks, which are flattened per sheet WITH their cell comments — a spreadsheet sent for review carries the review in its comments. HTML is converted to plain text; credential-shaped spans are redacted. Pass a permalink, or channel + timestamp, or just a channel/DM to grab its newest document. list_only shows every attachment, marking the ones that are not text. Use view_image for pictures and transcribe_audio for voice notes."),
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

	// Text files are deleted straight after rendering, but a PDF has to
	// survive the call so the caller can open it — so this writes into
	// the shared temp dir rather than one we remove wholesale.
	saved, skipped, wsName, errRes := h.fetchFiles(ctx, workspace, channel, timestamp, permalink, from, os.TempDir(), "slk-doc", isReadableDocument)
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
	// A listing must show everything the messages carry. Filtering it to
	// readable files makes an incomplete list look complete: the caller
	// sees the documents, concludes that is all there is, and never learns
	// the logs posted beside them exist. Reads still filter.
	accept := isReadableDocument
	if listOnly {
		accept = anyFile
	}
	msgs, merr := scoped.Messages().RecentFileMessages(ctx, channelID, accept, fromID, docScanLimit)
	if merr != nil {
		return h.scopeResult(wsName, merr)
	}

	candidates := collectDocuments(msgs, match, accept)
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

	files := make([]goslack.File, 0, len(candidates))
	for _, c := range candidates {
		files = append(files, c.File)
	}
	saved, _, derr := downloadFiles(ctx, scoped.Messages(), files, os.TempDir(), "slk-doc", isReadableDocument)
	if derr != nil {
		return mcp.NewToolResultError(derr.Error())
	}
	return mcp.NewToolResultText(renderDocuments(saved, h.wsLabel(wsName), maxChars))
}

// anyFile accepts every attachment — the listing filter. Pure.
func anyFile(goslack.File) bool { return true }

// collectDocuments flattens the accepted attachments out of messages
// (already newest-first), keeping only those whose filename contains
// match when one is given. Pure.
func collectDocuments(msgs []goslack.Message, match string, accept func(goslack.File) bool) []docCandidate {
	needle := strings.ToLower(strings.TrimSpace(match))
	var out []docCandidate
	for i := range msgs {
		for _, f := range msgs[i].Files {
			if !accept(f) {
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
// Every attachment is listed, readable or not, each marked — an omission
// here reads as "there is nothing else", which is the one thing a listing
// must never imply falsely. Pure.
func renderDocumentList(candidates []docCandidate, wsLabel string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d attachment(s)%s, newest first:\n", len(candidates), wsLabel)
	for _, c := range candidates {
		fmt.Fprintf(&b, "- %s (%s, %d bytes) ts=%s", c.File.Name, c.File.Mimetype, c.File.Size, c.TS)
		if !isReadableDocument(c.File) {
			b.WriteString("  [not text — try view_image or transcribe_audio]")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nPass one of these as timestamp=, or narrow with match=, to read it.\n")
	return b.String()
}

// renderDocuments turns downloaded files into the inline text body
// shared by both resolution paths. Text files are read, rendered and
// then deleted — nothing needs to outlive the call. A PDF is left on
// disk and reported by path instead: parsing PDF in Go would mean a
// dependency and a lossy text extraction, when the caller already has a
// reader that renders PDFs properly.
func renderDocuments(saved []savedFile, wsLabel string, maxChars int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d document(s)%s:\n", len(saved), wsLabel)
	for _, f := range saved {
		if isPDFMimetype(f.Mimetype) {
			fmt.Fprintf(&b, "\n--- %s (%s, %d bytes) ---\nsaved to: %s\nBinary document, not flattened to text. Open this path with a PDF-capable reader.\n",
				displayName(f.Path), f.Mimetype, f.Size, f.Path)
			continue
		}
		raw, rerr := os.ReadFile(f.Path)
		os.Remove(f.Path)
		if rerr != nil {
			fmt.Fprintf(&b, "\n--- %s: read failed: %v\n", f.Path, rerr)
			continue
		}
		var text string
		var sheetsCut bool
		if isSpreadsheetMimetype(f.Mimetype) || strings.HasSuffix(strings.ToLower(f.Path), ".xlsx") {
			var xerr error
			text, sheetsCut, xerr = xlsxToText(raw)
			if xerr != nil {
				fmt.Fprintf(&b, "\n--- %s (%s, %d bytes): %v\n", displayName(f.Path), f.Mimetype, f.Size, xerr)
				continue
			}
		} else {
			text = documentToText(string(raw), f.Mimetype)
		}
		// Before truncation, not after: a cap that happens to bisect a
		// private key still spills half of one. Reading a .ovpn or a .conf
		// is now in scope, so the key material in them must not reach the
		// transcript verbatim.
		text, secrets := export.Redact(text)
		text, truncated := truncateText(text, maxChars)
		fmt.Fprintf(&b, "\n--- %s (%s, %d bytes)", displayName(f.Path), f.Mimetype, f.Size)
		if secrets > 0 {
			fmt.Fprintf(&b, " — %d secret(s) redacted", secrets)
		}
		if sheetsCut {
			b.WriteString(" — TRUNCATED at the workbook cell cap")
		}
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

// genericMimetypes carry no information: Slack falls back to them for any
// upload it cannot classify. They are not a statement that the bytes are
// binary, so the filename is allowed to decide instead.
var genericMimetypes = map[string]bool{
	"":                         true,
	"application/octet-stream": true,
	"binary/octet-stream":      true,
	"application/binary":       true,
}

// documentExtensions are filename suffixes that carry text. Last tier and
// the one that decides in practice: Slack serves an OpenVPN client log as
// application/octet-stream, so mimetype and filetype both said "not a
// document" and the file was skipped — silently, which is what made it
// dangerous. Operational evidence arrives as .log and .conf far more often
// than as anything Slack has a filetype for.
var documentExtensions = map[string]bool{
	"txt": true, "log": true, "out": true, "err": true,
	"md": true, "markdown": true, "html": true, "htm": true,
	"json": true, "csv": true, "tsv": true, "yaml": true, "yml": true,
	"xml": true, "ini": true, "toml": true, "properties": true,
	"conf": true, "cfg": true, "ovpn": true, "diff": true, "patch": true,
	"sql": true, "sh": true, "bash": true, "zsh": true, "py": true,
	"go": true, "js": true, "ts": true, "css": true, "rb": true,
	"php": true, "java": true, "c": true, "h": true, "cpp": true,
}

// fileExtension returns a filename's lowercase extension without the dot.
// Pure.
func fileExtension(name string) string {
	i := strings.LastIndex(name, ".")
	if i < 0 || i == len(name)-1 {
		return ""
	}
	return strings.ToLower(name[i+1:])
}

// isDocumentFile reports whether a Slack attachment is text this tool can
// render, in three tiers: mimetype, then Slack's own filetype (snippets
// and posts often ship with an empty mimetype), then the filename.
//
// The filename tier is consulted ONLY when the mimetype is generic. An
// explicit image/audio/video/zip mimetype is a statement about the bytes,
// and a misleading extension must not be able to override it. Pure.
func isDocumentFile(f goslack.File) bool {
	m := strings.ToLower(strings.TrimSpace(f.Mimetype))
	if strings.HasPrefix(m, "text/") || documentMimetypes[m] {
		return true
	}
	if documentFiletypes[strings.ToLower(strings.TrimSpace(f.Filetype))] {
		return true
	}
	if !genericMimetypes[m] {
		return false
	}
	return documentExtensions[fileExtension(strings.TrimSpace(f.Name))]
}

// isSpreadsheetMimetype reports whether a mimetype names an .xlsx
// workbook. Pure.
func isSpreadsheetMimetype(mimetype string) bool {
	return strings.Contains(strings.ToLower(mimetype), "spreadsheetml.sheet")
}

// isPDFMimetype reports whether a mimetype names a PDF. Pure.
func isPDFMimetype(mimetype string) bool {
	return strings.Contains(strings.ToLower(mimetype), "pdf")
}

// isPDFFile reports whether an attachment is a PDF. PDFs are documents
// in every sense that matters — decks, proposals and reports circulate
// as PDF — but they are binary, so this tool fetches them rather than
// flattening them to text. Pure.
func isPDFFile(f goslack.File) bool {
	return isPDFMimetype(f.Mimetype) ||
		strings.EqualFold(strings.TrimSpace(f.Filetype), "pdf")
}

// isSpreadsheetFile reports whether an attachment is an .xlsx workbook.
// Binary like a PDF, but unlike a PDF it flattens to useful text, so it
// is rendered inline rather than handed back as a path. Pure.
func isSpreadsheetFile(f goslack.File) bool {
	if strings.Contains(strings.ToLower(f.Mimetype), "spreadsheetml.sheet") {
		return true
	}
	return fileExtension(strings.TrimSpace(f.Name)) == "xlsx" ||
		strings.EqualFold(strings.TrimSpace(f.Filetype), "xlsx")
}

// isReadableDocument is what read_document accepts: text it can render
// inline, spreadsheets it flattens, plus PDFs it hands back as a local
// path. Pure.
func isReadableDocument(f goslack.File) bool {
	return isDocumentFile(f) || isPDFFile(f) || isSpreadsheetFile(f)
}

// expectsTextBody reports whether an attachment is *supposed* to contain
// text. It exists to keep downloadFiles' sign-in-page guard honest: that
// guard rejects any download whose first bytes are HTML, which is exactly
// right for audio/video/images but would reject a genuine .html
// attachment as a scope failure. Pure.
func expectsTextBody(f goslack.File) bool {
	return isDocumentFile(f) && !isSpreadsheetFile(f)
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
