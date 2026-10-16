package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// ADR 124: edit_canvas.

// ecTab sets up channel "alpha" with canvas tab F1 titled "Team notes".
func ecTab(f *fakeSlack) {
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Team notes","filetype":"canvas","permalink":"https://example.invalid/docs/T0/F1"}}`)
}

// ecRecord captures the forms posted to a method.
type ecRecord struct {
	mu    sync.Mutex
	forms []map[string]string
}

func (r *ecRecord) handler(body string) func(*http.Request) string {
	return func(req *http.Request) string {
		r.mu.Lock()
		defer r.mu.Unlock()
		m := map[string]string{}
		for k := range req.Form {
			m[k] = req.Form.Get(k)
		}
		r.forms = append(r.forms, m)
		return body
	}
}

func (r *ecRecord) only(t *testing.T) map[string]string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.forms) != 1 {
		t.Fatalf("expected one call, got %d", len(r.forms))
	}
	return r.forms[0]
}

func ecChanges(t *testing.T, form map[string]string) []map[string]any {
	t.Helper()
	var ch []map[string]any
	if err := json.Unmarshal([]byte(form["changes"]), &ch); err != nil {
		t.Fatalf("changes is not JSON: %v (%q)", err, form["changes"])
	}
	return ch
}

func TestEditCanvas_AppendsToTheChannelTabByDefault(t *testing.T) {
	f := newFakeSlack(t)
	ecTab(f)
	var edit ecRecord
	f.OnFunc("canvases.edit", edit.handler(`{"ok":true}`))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{"channel": "alpha", "markdown": "- [ ] follow up"})

	if res.IsError {
		t.Fatalf("edit failed: %s", res.Text())
	}
	form := edit.only(t)
	if form["canvas_id"] != "F1" {
		t.Errorf("canvas_id = %q", form["canvas_id"])
	}
	ch := ecChanges(t, form)
	if ch[0]["operation"] != "insert_at_end" || ch[0]["section_id"] != nil {
		t.Errorf("change = %v", ch[0])
	}
	if dc := ch[0]["document_content"].(map[string]any); dc["markdown"] != "- [ ] follow up" || dc["type"] != "markdown" {
		t.Errorf("document_content = %v", dc)
	}
	if f.Called("canvases.sections.lookup") {
		t.Error("an append must not look up sections")
	}
	if got := res.Text(); !strings.Contains(got, `edited canvas "Team notes" (F1)`) || !strings.Contains(got, "/docs/T0/F1") {
		t.Errorf("result = %q", got)
	}
}

func TestEditCanvas_InsertAfterTheOneMatchingSection(t *testing.T) {
	f := newFakeSlack(t)
	ecTab(f)
	var lookup, edit ecRecord
	f.OnFunc("canvases.sections.lookup", lookup.handler(`{"ok":true,"sections":[{"id":"temp:C:abc"}]}`))
	f.OnFunc("canvases.edit", edit.handler(`{"ok":true}`))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{
		"channel": "alpha", "operation": "insert_after", "markdown": "new line",
		"section_contains": "Agenda", "section_type": "h2",
	})

	if res.IsError {
		t.Fatalf("edit failed: %s", res.Text())
	}
	var crit map[string]any
	_ = json.Unmarshal([]byte(lookup.only(t)["criteria"]), &crit)
	if crit["contains_text"] != "Agenda" || crit["section_types"].([]any)[0] != "h2" {
		t.Errorf("criteria = %v", crit)
	}
	if ch := ecChanges(t, edit.only(t)); ch[0]["section_id"] != "temp:C:abc" || ch[0]["operation"] != "insert_after" {
		t.Errorf("change = %v", ch[0])
	}
}

func TestEditCanvas_AmbiguousOrMissingSectionChangesNothing(t *testing.T) {
	for name, sections := range map[string]string{
		"two matches": `[{"id":"s1"},{"id":"s2"}]`,
		"no match":    `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeSlack(t)
			ecTab(f)
			f.On("canvases.sections.lookup", `{"ok":true,"sections":`+sections+`}`)
			h := newFakeHub(t, f)

			res := dcCallTool(t, h, "edit_canvas", map[string]any{
				"channel": "alpha", "operation": "delete", "section_contains": "Agenda",
			})

			if !res.IsError || !strings.Contains(res.Text(), "nothing changed") {
				t.Fatalf("expected a refusal, got %q", res.Text())
			}
			if f.Called("canvases.edit") {
				t.Error("canvases.edit must not be called when the section is not unique")
			}
		})
	}
}

func TestEditCanvas_DryRunResolvesButDoesNotWrite(t *testing.T) {
	f := newFakeSlack(t)
	ecTab(f)
	f.On("canvases.sections.lookup", `{"ok":true,"sections":[{"id":"s1"}]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{
		"channel": "alpha", "operation": "replace", "section_contains": "Agenda",
		"markdown": "## Agenda\nupdated", "dry_run": true,
	})

	if res.IsError || !strings.HasPrefix(res.Text(), "dry run — would replace the section containing \"Agenda\"") {
		t.Fatalf("result = %q", res.Text())
	}
	if f.Called("canvases.edit") {
		t.Error("dry_run wrote to the canvas")
	}
}

func TestEditCanvas_WholeReplaceNeedsTheExplicitFlag(t *testing.T) {
	f := newFakeSlack(t)
	ecTab(f)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{"channel": "alpha", "operation": "replace", "markdown": "x"})

	if !res.IsError || !strings.Contains(res.Text(), "replace_all=true") {
		t.Fatalf("result = %q", res.Text())
	}
	if len(f.Calls()) != 0 {
		t.Errorf("validation must fail before any Slack call; calls=%v", f.Calls())
	}
}

func TestEditCanvas_SeveralCanvasesAndNoSelectorIsRefused(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.On("files.list", `{"ok":true,"files":[
		{"id":"F1","title":"Notes one","created":100},
		{"id":"F2","title":"Notes two","created":200}]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{"channel": "alpha", "markdown": "x"})

	if !res.IsError || !strings.Contains(res.Text(), "has 2 canvases") || !strings.Contains(res.Text(), "Notes two") {
		t.Fatalf("result = %q", res.Text())
	}
	if f.Called("canvases.edit") {
		t.Error("an ambiguous channel must not be edited")
	}
}

func TestEditCanvas_MatchPicksOneOfSeveral(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.On("files.list", `{"ok":true,"files":[
		{"id":"F1","title":"Notes one","created":100},
		{"id":"F2","title":"Notes two","created":200}]}`)
	var edit ecRecord
	f.OnFunc("canvases.edit", edit.handler(`{"ok":true}`))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{"channel": "alpha", "match": "one", "markdown": "x"})

	if res.IsError {
		t.Fatalf("edit failed: %s", res.Text())
	}
	if got := edit.only(t)["canvas_id"]; got != "F1" {
		t.Errorf("edited %q, want F1", got)
	}
}

func TestEditCanvas_RenameByLinkSendsTitleContent(t *testing.T) {
	f := newFakeSlack(t)
	f.On("files.info", `{"ok":true,"file":{"id":"F0000001","title":"Old","filetype":"canvas"}}`)
	var edit ecRecord
	f.OnFunc("canvases.edit", edit.handler(`{"ok":true}`))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{
		"canvas": "https://example.invalid/docs/T0000000/F0000001", "operation": "rename", "title": "New",
	})

	if res.IsError {
		t.Fatalf("rename failed: %s", res.Text())
	}
	ch := ecChanges(t, edit.only(t))
	if ch[0]["operation"] != "rename" || ch[0]["document_content"] != nil {
		t.Errorf("change = %v", ch[0])
	}
	if tc := ch[0]["title_content"].(map[string]any); tc["markdown"] != "New" {
		t.Errorf("title_content = %v", tc)
	}
}

func TestEditCanvas_NotACanvasIsRefused(t *testing.T) {
	f := newFakeSlack(t)
	f.On("files.info", `{"ok":true,"file":{"id":"F0000001","title":"photo","filetype":"png"}}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{"canvas": "F0000001", "markdown": "x"})

	if !res.IsError || !strings.Contains(res.Text(), "not a canvas") || f.Called("canvases.edit") {
		t.Fatalf("result = %q calls=%v", res.Text(), f.Calls())
	}
}

func TestEditCanvas_SlackErrorNamesTheMissingScope(t *testing.T) {
	f := newFakeSlack(t)
	ecTab(f)
	f.On("canvases.edit", `{"ok":false,"error":"missing_scope","needed":"canvases:write"}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "edit_canvas", map[string]any{"channel": "alpha", "markdown": "x"})

	if !res.IsError || !strings.Contains(res.Text(), "needs scope canvases:write") {
		t.Fatalf("result = %q", res.Text())
	}
}

func TestEditCanvas_NotOfferedReadOnlyOrWithoutUserToken(t *testing.T) {
	if !dcHas(dcRegisterAll(t), "edit_canvas") {
		t.Fatal("edit_canvas missing from the catalogue")
	}
	if dcHas(dcRegisterAll(t, func(c *config.Config) { c.ReadOnly = true }), "edit_canvas") {
		t.Error("edit_canvas offered in read-only mode")
	}
	if dcHas(dcRegisterAll(t, func(c *config.Config) { c.UserToken = "" }), "edit_canvas") {
		t.Error("edit_canvas offered without a user token")
	}
}

func TestEditCanvasParams_Validate(t *testing.T) {
	base := func() editCanvasParams {
		return editCanvasParams{channel: "a", operation: "insert_at_end", markdown: "x"}
	}
	cases := map[string]struct {
		mut  func(*editCanvasParams)
		want string
	}{
		"ok":                 {func(*editCanvasParams) {}, ""},
		"unknown op":         {func(p *editCanvasParams) { p.operation = "move" }, "unknown operation"},
		"no target":          {func(p *editCanvasParams) { p.channel = "" }, "pass canvas"},
		"no content":         {func(p *editCanvasParams) { p.markdown = "" }, "needs markdown"},
		"delete no section":  {func(p *editCanvasParams) { p.operation = "delete" }, "needs section_contains"},
		"rename no title":    {func(p *editCanvasParams) { p.operation = "rename" }, "needs title"},
		"section on append":  {func(p *editCanvasParams) { p.sectionContains = "x" }, "takes no section_contains"},
		"bad section type":   {func(p *editCanvasParams) { p.operation = "delete"; p.sectionContains = "x"; p.sectionType = "h4" }, "section_type must be"},
		"type alone":         {func(p *editCanvasParams) { p.sectionType = "h1" }, "pass both"},
		"replace both modes": {func(p *editCanvasParams) { p.operation = "replace"; p.sectionContains = "x"; p.replaceAll = true }, "not both"},
		"replace all ok":     {func(p *editCanvasParams) { p.operation = "replace"; p.replaceAll = true }, ""},
	}
	for name, c := range cases {
		p := base()
		c.mut(&p)
		err := p.validate()
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}

func TestParseCanvasRef(t *testing.T) {
	for in, want := range map[string]string{
		"F0000001": "F0000001",
		"https://example.invalid/docs/T0000000/F0000002":              "F0000002",
		"https://example.invalid/files/U0000000/F0000003/notes_title": "F0000003",
	} {
		if got, err := parseCanvasRef(in); err != nil || got != want {
			t.Errorf("%q → %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "notes", "https://example.invalid/archives/C1/p1"} {
		if _, err := parseCanvasRef(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
