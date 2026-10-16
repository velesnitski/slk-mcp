package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// edit_canvas — ADR 124.
//
// A canvas edit cannot be undone from the API, so the tool is built to
// refuse rather than guess: a section is addressed by text that must
// match exactly one section, a whole-canvas replace needs an explicit
// flag, a channel with several canvases and no selector is an error that
// lists them, and dry_run resolves everything without writing.

// canvasOps maps each canvases.edit operation to what it needs.
var canvasOps = map[string]struct{ section, content, title bool }{
	"insert_at_end":   {content: true},
	"insert_at_start": {content: true},
	"insert_after":    {section: true, content: true},
	"insert_before":   {section: true, content: true},
	"replace":         {content: true}, // section optional, see replace_all
	"delete":          {section: true},
	"rename":          {title: true},
}

var canvasSectionTypes = map[string]bool{"h1": true, "h2": true, "h3": true, "any_header": true}

type editCanvasParams struct {
	canvas, channel     string
	sel                 canvasSelector
	operation, markdown string
	title               string
	sectionContains     string
	sectionType         string
	replaceAll, dryRun  bool
}

// validate checks the operation's inputs before any Slack call. Pure.
func (p editCanvasParams) validate() error {
	need, ok := canvasOps[p.operation]
	if !ok {
		return fmt.Errorf("unknown operation %q (insert_at_end, insert_at_start, insert_after, insert_before, replace, delete, rename)", p.operation)
	}
	if p.canvas == "" && p.channel == "" {
		return fmt.Errorf("pass canvas (an F… id or canvas link) or channel")
	}
	if need.content && p.markdown == "" {
		return fmt.Errorf("%s needs markdown", p.operation)
	}
	if need.title && p.title == "" {
		return fmt.Errorf("rename needs title")
	}
	if need.section && p.sectionContains == "" {
		return fmt.Errorf("%s needs section_contains: text that appears in exactly one section", p.operation)
	}
	if p.operation == "replace" && p.sectionContains == "" && !p.replaceAll {
		return fmt.Errorf("replace without section_contains overwrites the WHOLE canvas; pass replace_all=true if that is intended")
	}
	if p.operation == "replace" && p.sectionContains != "" && p.replaceAll {
		return fmt.Errorf("pass either section_contains or replace_all, not both")
	}
	if !need.section && p.operation != "replace" && p.sectionContains != "" {
		return fmt.Errorf("%s takes no section_contains", p.operation)
	}
	if p.sectionType != "" && !canvasSectionTypes[p.sectionType] {
		return fmt.Errorf("section_type must be h1, h2, h3 or any_header")
	}
	if p.sectionType != "" && p.sectionContains == "" {
		return fmt.Errorf("section_type narrows section_contains; pass both")
	}
	return nil
}

func (h *Hub) registerEditCanvas(s *server.MCPServer) {
	h.register(s, toolDef{
		Name:              "edit_canvas",
		Writes:            true,
		RequiresUserToken: true,
		Description: "Edit a Slack canvas as you (canvases.edit, needs the canvases:write scope). Target it by `canvas` (an F… id or a canvas link) or by `channel` — the attached canvas tab, or one shared canvas picked with `match`/`date` as in read_canvas. " +
			"Operations: insert_at_end (default), insert_at_start, insert_after / insert_before / delete (need `section_contains`), replace (a section via `section_contains`, or the whole canvas with replace_all=true), rename (`title`). " +
			"`section_contains` must match exactly one section, otherwise nothing is changed and the count is reported. Edits cannot be undone through the API — use dry_run=true to see what would change, and read_canvas to check the result.",
		Opts: []mcp.ToolOption{
			mcp.WithString("canvas", mcp.Description("Canvas file id (F…) or a canvas link (…/docs/T…/F… or …/files/U…/F…). Wins over channel.")),
			mcp.WithString("channel", mcp.Description("Channel or DM whose canvas to edit: the attached canvas tab, else its only shared canvas. With several canvases, pick one with match/date.")),
			mcp.WithString("match", mcp.Description("With channel: pick the canvas whose title contains this text (case-insensitive).")),
			mcp.WithString("date", mcp.Description("With channel: pick the canvas whose title carries this date, YYYY-MM-DD (same spellings as read_canvas).")),
			mcp.WithString("operation", mcp.Description("insert_at_end (default), insert_at_start, insert_after, insert_before, replace, delete, rename.")),
			mcp.WithString("markdown", mcp.Description("Content to insert or replace with, in canvas markdown (headings, lists, checklists `- [ ]`, links). Mention a person with ![](@U…), a channel with ![](#C…).")),
			mcp.WithString("title", mcp.Description("New canvas title, for rename.")),
			mcp.WithString("section_contains", mcp.Description("Text that identifies the target section (a heading or a line); must match exactly one section.")),
			mcp.WithString("section_type", mcp.Description("Narrow section_contains to headings: h1, h2, h3 or any_header.")),
			mcp.WithBoolean("replace_all", mcp.Description("With operation=replace and no section_contains: overwrite the whole canvas. Default: false.")),
			mcp.WithBoolean("dry_run", mcp.Description("Resolve the canvas and the section and report what would change, without editing. Default: false.")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		},
		Handle: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			p := editCanvasParams{
				canvas:  strings.TrimSpace(req.GetString("canvas", "")),
				channel: strings.TrimSpace(req.GetString("channel", "")),
				sel: canvasSelector{
					date:  strings.TrimSpace(req.GetString("date", "")),
					match: strings.TrimSpace(req.GetString("match", "")),
				},
				operation:       strings.TrimSpace(req.GetString("operation", "insert_at_end")),
				markdown:        strings.TrimSpace(req.GetString("markdown", "")),
				title:           strings.TrimSpace(req.GetString("title", "")),
				sectionContains: strings.TrimSpace(req.GetString("section_contains", "")),
				sectionType:     strings.TrimSpace(req.GetString("section_type", "")),
				replaceAll:      req.GetBool("replace_all", false),
				dryRun:          req.GetBool("dry_run", false),
			}
			scoped, wsName, errRes := h.scopedWorkspace(req.GetString("workspace", ""))
			if errRes != nil {
				return errRes, nil
			}
			out, err := scoped.runEditCanvas(ctx, p)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(out + h.wsLabel(wsName)), nil
		},
	})
}

func (h *Hub) runEditCanvas(ctx context.Context, p editCanvasParams) (string, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	file, err := h.resolveCanvasTarget(ctx, p)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(file.Title)
	if title == "" {
		title = "(untitled)"
	}

	edit := slack.CanvasEdit{Operation: p.operation, Markdown: p.markdown, Title: p.title}
	if p.sectionContains != "" {
		var types []string
		if p.sectionType != "" {
			types = []string{p.sectionType}
		}
		ids, err := h.Canvas().LookupCanvasSections(ctx, file.ID, p.sectionContains, types)
		if err != nil {
			return "", err
		}
		switch len(ids) {
		case 0:
			return "", fmt.Errorf("no section of canvas %q contains %q — nothing changed", title, p.sectionContains)
		case 1:
			edit.SectionID = ids[0]
		default:
			return "", fmt.Errorf("%d sections of canvas %q contain %q — nothing changed; use longer text or section_type", len(ids), title, p.sectionContains)
		}
	}

	what := describeCanvasEdit(p)
	if p.dryRun {
		return fmt.Sprintf("dry run — would %s in canvas %q (%s); nothing changed%s", what, title, file.ID, canvasLinkSuffix(file)), nil
	}
	if err := h.Canvas().EditCanvas(ctx, file.ID, []slack.CanvasEdit{edit}); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited canvas %q (%s): %s%s", title, file.ID, what, canvasLinkSuffix(file)), nil
}

// describeCanvasEdit says in one clause what the edit does. Pure.
func describeCanvasEdit(p editCanvasParams) string {
	sec := fmt.Sprintf("the section containing %q", p.sectionContains)
	size := fmt.Sprintf("%d chars", len([]rune(p.markdown)))
	switch p.operation {
	case "insert_at_start":
		return "insert " + size + " at the start"
	case "insert_after":
		return "insert " + size + " after " + sec
	case "insert_before":
		return "insert " + size + " before " + sec
	case "replace":
		if p.replaceAll {
			return "replace the WHOLE canvas with " + size
		}
		return "replace " + sec + " with " + size
	case "delete":
		return "delete " + sec
	case "rename":
		return fmt.Sprintf("rename it to %q", p.title)
	default:
		return "insert " + size + " at the end"
	}
}

func canvasLinkSuffix(f goslack.File) string {
	if f.Permalink == "" {
		return ""
	}
	return "\n" + f.Permalink
}

// reCanvasRef pulls the canvas file id out of a canvas or file link.
var reCanvasRef = regexp.MustCompile(`/(?:docs/T[A-Z0-9]+|files/[UWB][A-Z0-9]+)/(F[A-Z0-9]+)`)
var reCanvasID = regexp.MustCompile(`^F[A-Z0-9]{6,}$`)

// parseCanvasRef accepts a bare F… id or a canvas/file link. Pure.
func parseCanvasRef(s string) (string, error) {
	if reCanvasID.MatchString(s) {
		return s, nil
	}
	if m := reCanvasRef.FindStringSubmatch(s); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("canvas must be an F… id or a canvas link, got %q", s)
}

// resolveCanvasTarget finds the one canvas the edit is for. Through a
// channel it never guesses between several canvases: with no selector it
// takes the attached tab, else the only shared canvas, else lists them.
func (h *Hub) resolveCanvasTarget(ctx context.Context, p editCanvasParams) (goslack.File, error) {
	if p.canvas != "" {
		id, err := parseCanvasRef(p.canvas)
		if err != nil {
			return goslack.File{}, err
		}
		f, err := h.Messages().FileInfo(ctx, id)
		if err != nil {
			return goslack.File{}, fmt.Errorf("canvas %s: files.info: %w", id, err)
		}
		if f.Filetype != "" && f.Filetype != "canvas" && f.Filetype != "quip" {
			return goslack.File{}, fmt.Errorf("file %s is a %s, not a canvas", id, f.Filetype)
		}
		if f.ID == "" {
			f.ID = id
		}
		return f, nil
	}

	channelID, err := h.resolveConversation(ctx, p.channel)
	if err != nil {
		return goslack.File{}, err
	}
	if p.sel.active() {
		files, err := h.canvasSet(ctx, channelID)
		if err != nil {
			return goslack.File{}, err
		}
		variants, err := canvasDateVariants(p.sel.date)
		if err != nil {
			return goslack.File{}, err
		}
		if pick := selectCanvas(files, p.sel.match, variants); pick != nil {
			return *pick, nil
		}
		if len(files) == 0 {
			return goslack.File{}, fmt.Errorf("no canvases in #%s", p.channel)
		}
		return goslack.File{}, fmt.Errorf("no canvas in #%s matches the selector; available:\n%s", p.channel, renderCanvasList(files))
	}

	tabID, _, err := h.Canvas().ChannelCanvas(ctx, channelID)
	if err != nil {
		return goslack.File{}, err
	}
	if tabID != "" {
		f, err := h.Messages().FileInfo(ctx, tabID)
		if err != nil {
			return goslack.File{}, fmt.Errorf("canvas %s: files.info: %w", tabID, err)
		}
		if f.ID == "" {
			f.ID = tabID
		}
		return f, nil
	}
	files, err := h.Canvas().CanvasFiles(ctx, channelID)
	if err != nil {
		return goslack.File{}, fmt.Errorf("canvas lookup (files.list): %w", err)
	}
	switch len(files) {
	case 0:
		return goslack.File{}, fmt.Errorf("#%s has no canvas — neither a canvas tab nor a shared canvas", p.channel)
	case 1:
		return files[0], nil
	default:
		return goslack.File{}, fmt.Errorf("#%s has %d canvases and no canvas tab — pick one with match, date or canvas:\n%s", p.channel, len(files), renderCanvasList(files))
	}
}
