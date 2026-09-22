package tools

import (
	"strings"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// dcChannelAlpha routes the channel-name lookup every read_canvas /
// read_document call starts with: "alpha" → C1.
func dcChannelAlpha(f *fakeSlack) {
	f.On("conversations.list", `{"ok":true,"channels":[{"id":"C1","name":"alpha"}]}`)
}

// dcCanvasTab routes conversations.info so the channel reports an
// attached canvas tab. isEmpty models Slack's `is_empty` flag.
func dcCanvasTab(f *fakeSlack, fileID string, isEmpty bool) {
	empty := "false"
	if isEmpty {
		empty = "true"
	}
	f.On("conversations.info", `{"ok":true,"channel":{"id":"C1","name":"alpha","properties":{"canvas":{"file_id":"`+fileID+`","is_empty":`+empty+`}}}}`)
}

// dcNoCanvasTab routes conversations.info to a channel with no canvas
// attached — not an error, just an absence.
func dcNoCanvasTab(f *fakeSlack) {
	f.On("conversations.info", `{"ok":true,"channel":{"id":"C1","name":"alpha"}}`)
}

// dcCanvasBody serves body at a download path and returns the URL to
// hang off a file's url_private_download.
func dcCanvasBody(f *fakeSlack, path, body string) string {
	f.On(path, body)
	return f.URL() + path
}

func TestReadCanvas_AttachedTabIsDownloadedAndRendered(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	url := dcCanvasBody(f, "dl/F1", "Agenda\n\nDecide the release date.")
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Weekly notes","mimetype":"text/markdown","url_private_download":"`+url+`"}}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if res.IsError {
		t.Fatalf("read_canvas failed: %s", got)
	}
	if !strings.HasPrefix(got, "# Weekly notes — #alpha") {
		t.Errorf("header missing or wrong:\n%s", got)
	}
	if !strings.Contains(got, "Decide the release date.") {
		t.Errorf("canvas body missing:\n%s", got)
	}
	// The attached tab short-circuits the files.list fallback.
	if f.Called("files.list") {
		t.Errorf("an attached canvas must not trigger the files.list fallback; calls=%v", f.Calls())
	}
}

func TestReadCanvas_EmptyTabSaysSoInsteadOfDownloading(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", true)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})

	if got := res.Text(); got != "Canvas for #alpha is empty." {
		t.Errorf("empty canvas rendered as %q", got)
	}
	if f.Called("files.info") {
		t.Errorf("an empty canvas must not be fetched; calls=%v", f.Calls())
	}
}

func TestReadCanvas_FallsBackToSharedCanvasFiles(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	newest := dcCanvasBody(f, "dl/F2", "Newest canvas body")
	f.On("files.list", `{"ok":true,"files":[
		{"id":"F1","title":"Older notes","mimetype":"text/markdown","created":100,"url_private_download":"`+f.URL()+`dl/F1"},
		{"id":"F2","title":"Newer notes","mimetype":"text/markdown","created":900,"url_private_download":"`+newest+`"}
	]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if res.IsError {
		t.Fatalf("read_canvas failed: %s", got)
	}
	// files.list order is not contractual, so the newest Created wins.
	if !strings.Contains(got, "# Newer notes — #alpha") {
		t.Errorf("expected the newest canvas, got:\n%s", got)
	}
	if !strings.Contains(got, "Newest canvas body") {
		t.Errorf("canvas body missing:\n%s", got)
	}
}

func TestReadCanvas_NoCanvasAnywhereIsAnExplicitError(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.On("files.list", `{"ok":true,"files":[]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a channel with no canvas must be an error, got %q", got)
	}
	// The message has to name both places that were checked, or the
	// reader cannot tell whether the lookup was complete.
	if !strings.Contains(got, "no canvas found in #alpha") ||
		!strings.Contains(got, "canvas tab") || !strings.Contains(got, "shared canvas file") {
		t.Errorf("error does not explain what was searched: %q", got)
	}
}

func TestReadCanvas_UntitledCanvasStillGetsAHeader(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	url := dcCanvasBody(f, "dl/F1", "body text")
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"   ","mimetype":"text/markdown","url_private_download":"`+url+`"}}`)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"}).Text()
	if !strings.HasPrefix(got, "# Canvas — #alpha") {
		t.Errorf("a blank title must fall back to \"Canvas\":\n%s", got)
	}
}

func TestReadCanvas_NoDownloadURLIsRefusedWithTheReason(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Weekly notes","mimetype":"text/markdown"}}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a canvas with no download URL must fail, got %q", got)
	}
	if !strings.Contains(got, "no download URL") || !strings.Contains(got, "files:read") {
		t.Errorf("error should point at the missing scope: %q", got)
	}
}

func TestReadCanvas_FilesInfoFailureNamesTheFile(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	f.OnError("files.info", "file_not_found")
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a files.info failure must surface, got %q", got)
	}
	if !strings.Contains(got, "canvas files.info (F1)") || !strings.Contains(got, "file_not_found") {
		t.Errorf("error lost the file id or the Slack code: %q", got)
	}
}

func TestReadCanvas_ListOnlyEnumeratesWithoutDownloading(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.On("files.list", `{"ok":true,"files":[
		{"id":"F1","title":"","created":100},
		{"id":"F2","title":"23.07.2026 Team Sync","created":900}
	]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha", "list_only": true})
	got := res.Text()

	if res.IsError {
		t.Fatalf("list_only failed: %s", got)
	}
	if !strings.Contains(got, "# Canvases in #alpha") {
		t.Errorf("listing header missing:\n%s", got)
	}
	if !strings.Contains(got, "23.07.2026 Team Sync") || !strings.Contains(got, "(untitled)") {
		t.Errorf("listing must name every canvas, untitled ones included:\n%s", got)
	}
	// Listing is the cheap answer: nothing may be fetched.
	if f.Called("files.info") {
		t.Errorf("list_only must not download anything; calls=%v", f.Calls())
	}
}

func TestReadCanvas_DateSelectorPicksTheMatchingCanvas(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	url := dcCanvasBody(f, "dl/F2", "Notes for that day")
	f.On("files.list", `{"ok":true,"files":[
		{"id":"F1","title":"22.07.2026 Team Sync","created":100,"mimetype":"text/markdown","url_private_download":"`+f.URL()+`dl/F1"},
		{"id":"F2","title":"23.07.2026 Team Sync","created":200,"mimetype":"text/markdown","url_private_download":"`+url+`"}
	]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{
		"channel": "alpha", "date": "2026-07-23", "match": "Team Sync",
	})
	got := res.Text()

	if res.IsError {
		t.Fatalf("date selection failed: %s", got)
	}
	if !strings.Contains(got, "23.07.2026 Team Sync") || strings.Contains(got, "22.07.2026") {
		t.Errorf("selector picked the wrong canvas:\n%s", got)
	}
	if !strings.Contains(got, "Notes for that day") {
		t.Errorf("selected canvas body missing:\n%s", got)
	}
}

func TestReadCanvas_SelectorMissListsWhatDoesExist(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.On("files.list", `{"ok":true,"files":[{"id":"F1","title":"22.07.2026 Team Sync","created":100}]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{
		"channel": "alpha", "date": "2026-07-23", "match": "Team Sync",
	})
	got := res.Text()

	// A miss is an answer, not an error: it has to say what IS there,
	// otherwise the caller cannot tell "no notes that day" from "lookup
	// broken".
	if res.IsError {
		t.Fatalf("a selector miss must not be an error: %q", got)
	}
	if !strings.Contains(got, "no canvas matching") || !strings.Contains(got, "Team Sync + 2026-07-23") {
		t.Errorf("miss must echo what was asked for: %q", got)
	}
	if !strings.Contains(got, "22.07.2026 Team Sync") {
		t.Errorf("miss must list the canvases that do exist: %q", got)
	}
}

func TestReadCanvas_SelectorOnEmptyChannelSaysNoneAtAll(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.On("files.list", `{"ok":true,"files":[]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha", "list_only": true})

	if got := res.Text(); got != "no canvases in #alpha at all" {
		t.Errorf("empty channel rendered as %q", got)
	}
}

func TestReadCanvas_SelectedModeAddsTheAttachedTab(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	// The tab's file is NOT among the shared canvas files, so the
	// selector has to fetch it separately or it is invisible to `match`.
	dcCanvasTab(f, "F9", false)
	f.On("files.list", `{"ok":true,"files":[{"id":"F1","title":"Shared notes","created":100}]}`)
	f.On("files.info", `{"ok":true,"file":{"id":"F9","title":"Channel tab","created":900}}`)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha", "list_only": true}).Text()

	if !strings.Contains(got, "Channel tab") {
		t.Errorf("the attached canvas tab is missing from the listing:\n%s", got)
	}
	if !strings.Contains(got, "Shared notes") {
		t.Errorf("shared canvases are missing from the listing:\n%s", got)
	}
}

func TestReadCanvas_SelectedModeDoesNotRefetchAnAlreadyListedTab(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	// files.list already carries the tab's file, so there is nothing to
	// add — a second files.info would be a wasted round trip.
	f.On("files.list", `{"ok":true,"files":[{"id":"F1","title":"Channel tab","created":900}]}`)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha", "list_only": true}).Text()

	if !strings.Contains(got, "Channel tab") {
		t.Errorf("listing lost the canvas:\n%s", got)
	}
	if strings.Count(got, "Channel tab") != 1 {
		t.Errorf("the canvas is listed twice:\n%s", got)
	}
	if f.Called("files.info") {
		t.Errorf("an already-listed tab must not be re-fetched; calls=%v", f.Calls())
	}
}

func TestReadCanvas_FilesListFailureInFallbackIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.OnError("files.list", "invalid_auth")
	h := newFakeHub(t, f)

	// No selector: this is the plain "read the canvas" path, whose
	// fallback to shared canvas files can fail on its own.
	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a files.list failure must surface, got %q", got)
	}
	if !strings.Contains(got, "canvas lookup (files.list)") || !strings.Contains(got, "invalid_auth") {
		t.Errorf("error lost its context or the Slack code: %q", got)
	}
}

func TestReadCanvas_OversizedCanvasIsMarkedTruncated(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	// Past canvasMaxBytes, so the render has to stop early — and say so.
	url := dcCanvasBody(f, "dl/F1", strings.Repeat("line of canvas text\n", canvasMaxBytes/10))
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Long notes","mimetype":"text/markdown","url_private_download":"`+url+`"}}`)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"}).Text()

	if !strings.HasSuffix(got, "_(canvas truncated)_") {
		t.Errorf("truncation must be announced at the end of the body:\n…%s", got[max(0, len(got)-120):])
	}
	if len(got) > canvasMaxBytes+500 {
		t.Errorf("body was not actually capped: %d bytes", len(got))
	}
}

func TestReadCanvas_MalformedDateIsRejected(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.On("files.list", `{"ok":true,"files":[{"id":"F1","title":"Team Sync","created":100}]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha", "date": "23.07.2026"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a non-ISO date must be refused, got %q", got)
	}
	if !strings.Contains(got, "date must be YYYY-MM-DD") {
		t.Errorf("error must state the expected format: %q", got)
	}
}

func TestReadCanvas_FilesListFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcNoCanvasTab(f)
	f.OnError("files.list", "missing_scope")
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha", "list_only": true})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a files.list failure must surface, got %q", got)
	}
	if !strings.Contains(got, "canvas lookup (files.list)") || !strings.Contains(got, "missing_scope") {
		t.Errorf("error lost its context or the Slack code: %q", got)
	}
}

func TestReadCanvas_UnknownChannelIsReported(t *testing.T) {
	f := newFakeSlack(t)
	f.On("conversations.list", `{"ok":true,"channels":[{"id":"C1","name":"alpha"}]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "beta"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("an unknown channel must be an error, got %q", got)
	}
	if !strings.Contains(got, "#beta") {
		t.Errorf("error must name the channel asked for: %q", got)
	}
}

func TestReadCanvas_MissingChannelArgumentIsRefused(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{})

	if got := res.Text(); got != "channel is required" {
		t.Errorf("missing channel rendered as %q", got)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("an argument error must not reach Slack; calls=%v", f.Calls())
	}
}

func TestReadCanvas_UnknownWorkspaceIsRefusedBeforeAnyCall(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha", "workspace": "nowhere"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("an unknown workspace must be an error, got %q", got)
	}
	if !strings.Contains(got, "nowhere") {
		t.Errorf("error must name the label asked for: %q", got)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("workspace scoping must fail before any API call; calls=%v", f.Calls())
	}
}

func TestReadCanvas_ResolvesMentionsInTheBody(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	url := dcCanvasBody(f, "dl/F1", "Owner: <@U1> signs off.")
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Weekly notes","mimetype":"text/markdown","url_private_download":"`+url+`"}}`)
	f.On("users.info", `{"ok":true,"user":{"id":"U1","name":"alex","profile":{"display_name":"Alex","real_name":"Alex"}}}`)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"}).Text()

	// A canvas that renders raw IDs is unreadable next to an unread
	// summary that says "mentions you" — the reader is told they were
	// named and cannot find where.
	if !strings.Contains(got, "Alex") {
		t.Errorf("mention was not resolved to a name:\n%s", got)
	}
	if strings.Contains(got, "<@U1>") {
		t.Errorf("raw mention markup survived:\n%s", got)
	}
}

func TestReadCanvas_HTMLCanvasIsFlattened(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	url := dcCanvasBody(f, "dl/F1",
		`<html><head><style>p{color:red}</style></head><body><h1>Plan</h1><ul><li>Ship it</li><li>Tell the team</li></ul><p>Owner &amp; reviewer</p></body></html>`)
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Plan","mimetype":"text/html","url_private_download":"`+url+`"}}`)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"}).Text()

	for _, want := range []string{"Plan", "• Ship it", "• Tell the team", "Owner & reviewer"} {
		if !strings.Contains(got, want) {
			t.Errorf("flattened canvas missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "color:red") || strings.Contains(got, "<li>") {
		t.Errorf("markup or style survived flattening:\n%s", got)
	}
}

func TestReadCanvas_DownloadFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	// A download URL nothing is listening on: the transport fails, which
	// is the shape of a revoked or scope-less token in production.
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Weekly notes","mimetype":"text/markdown","url_private_download":"http://127.0.0.1:1/dl/F1"}}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a failed download must surface as an error, got %q", got)
	}
	if !strings.Contains(got, "canvas download") {
		t.Errorf("error lost the stage it failed at: %q", got)
	}
}

func TestReadCanvas_ChannelCanvasErrorIsSurfaced(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	f.OnError("conversations.info", "channel_not_found")
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a conversations.info failure must surface, got %q", got)
	}
	if !strings.Contains(got, "conversations.info") || !strings.Contains(got, "channel_not_found") {
		t.Errorf("error lost its context or the Slack code: %q", got)
	}
}

func TestReadCanvas_BotOnlyHubStillReadsTheAttachedTab(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcCanvasTab(f, "F1", false)
	url := dcCanvasBody(f, "dl/F1", "Bot-visible body")
	f.On("files.info", `{"ok":true,"file":{"id":"F1","title":"Weekly notes","mimetype":"text/markdown","url_private_download":"`+url+`"}}`)
	h := newFakeHub(t, f, func(c *config.Config) { c.UserToken = "" })

	res := dcCallTool(t, h, "read_canvas", map[string]any{"channel": "alpha"})
	got := res.Text()

	if res.IsError {
		t.Fatalf("read_canvas failed on a bot-only hub: %s", got)
	}
	if !strings.Contains(got, "Bot-visible body") {
		t.Errorf("body missing:\n%s", got)
	}
	// One identity means one conversations.info, not two.
	n := 0
	for _, c := range f.Calls() {
		if c == "conversations.info" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("bot-only hub made %d conversations.info calls, want 1 (%v)", n, f.Calls())
	}
}

func TestReadCanvasSelector_ActiveReflectsEachField(t *testing.T) {
	cases := []struct {
		name string
		sel  canvasSelector
		want bool
	}{
		{"nothing set", canvasSelector{}, false},
		{"date", canvasSelector{date: "2026-07-23"}, true},
		{"match", canvasSelector{match: "Team Sync"}, true},
		{"list only", canvasSelector{listOnly: true}, true},
	}
	for _, tc := range cases {
		if got := tc.sel.active(); got != tc.want {
			t.Errorf("%s: active() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
