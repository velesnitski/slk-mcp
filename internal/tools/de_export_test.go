package tools

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/export"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// deReadCorpus parses a JSONL corpus file into records.
func deReadCorpus(t *testing.T, path string) []export.Record {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus %s: %v", path, err)
	}
	var out []export.Record
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var r export.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("corpus line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// deRichMsg is a message carrying everything the corpus promises to keep
// losslessly: thread root, reactions with actors, an edit flag and a file.
func deRichMsg(ts string, replyCount int) string {
	return fmt.Sprintf(
		`{"type":"message","user":"U0AAAAAAAAA","ts":%q,"thread_ts":%q,"text":"the decision thread",`+
			`"reply_count":%d,"latest_reply":%q,"edited":{"user":"U0AAAAAAAAA","ts":%q},`+
			`"reactions":[{"name":"white_check_mark","count":1,"users":["U0BBBBBBBBB"]}],`+
			`"files":[{"id":"F0AAAAAAAAA","name":"notes.txt","mimetype":"text/plain"}]}`,
		ts, ts, replyCount, ts, ts)
}

func TestExport_CapturesThreadReactionsAndFiles_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	parentTS := deTS(2 * time.Hour)

	f.on("conversations.list", deAlphaList())
	f.on("auth.test", deAuthTest("https://example.slack.com/"))
	deAnyUser(f)
	f.on("conversations.history", deHistory(deRichMsg(parentTS, 2)))
	f.on("conversations.replies", deHistory(
		deRichMsg(parentTS, 2),
		deReplyJSON("U0BBBBBBBBB", deTS(90*time.Minute), parentTS, "first answer"),
		deReplyJSON("U0BBBBBBBBB", deTS(80*time.Minute), parentTS, "second answer"),
	))
	hub := deHub(t, f)

	path := filepath.Join(dir, "primary.jsonl")
	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha", "hours": 24,
	}))
	if out != "# Export\nprimary: +3 new of 3 scanned → "+path {
		t.Fatalf("summary drifted: %q", out)
	}

	recs := deReadCorpus(t, path)
	if len(recs) != 3 {
		t.Fatalf("parent plus both replies expected, got %d", len(recs))
	}
	// Sorted oldest-first regardless of the order Slack returned them.
	for i := 1; i < len(recs); i++ {
		if !tsLessStr(recs[i-1].TS, recs[i].TS) {
			t.Fatalf("records must be ordered by timestamp: %v", []string{recs[i-1].TS, recs[i].TS})
		}
	}

	var parent *export.Record
	for i := range recs {
		if recs[i].TS == parentTS {
			parent = &recs[i]
		}
	}
	if parent == nil {
		t.Fatalf("parent record missing from %+v", recs)
	}
	if parent.Workspace != "primary" || parent.ChannelID != "C0AAAAAAAAA" || parent.Channel != "alpha" {
		t.Fatalf("identity fields wrong: %+v", parent)
	}
	if parent.Kind != "channel" {
		t.Fatalf("kind should separate public discussion from a DM, got %q", parent.Kind)
	}
	if parent.UserName != "Alex" {
		t.Fatalf("author should be resolved, got %q", parent.UserName)
	}
	if !parent.Edited {
		t.Fatal("the edit flag cannot be reconstructed later, so it must be captured")
	}
	// A ✅ from the right person IS the decision — keep who, not just how many.
	if len(parent.Reactions) != 1 || parent.Reactions[0].Name != "white_check_mark" ||
		len(parent.Reactions[0].Users) != 1 || parent.Reactions[0].Users[0] != "U0BBBBBBBBB" {
		t.Fatalf("reactions must keep their actors: %+v", parent.Reactions)
	}
	if len(parent.Files) != 1 || parent.Files[0].ID != "F0AAAAAAAAA" ||
		parent.Files[0].Name != "notes.txt" || parent.Files[0].Mimetype != "text/plain" {
		t.Fatalf("file references must survive: %+v", parent.Files)
	}
	// reply_count vs replies_fetched is how a later reader tells a short
	// thread from a truncated one.
	if parent.ReplyCount != 2 || parent.RepliesFetched != 2 {
		t.Fatalf("thread completeness counters wrong: %d/%d", parent.ReplyCount, parent.RepliesFetched)
	}
	wantLink := "https://example.slack.com/archives/C0AAAAAAAAA/p" + strings.Replace(parentTS, ".", "", 1)
	if parent.Permalink != wantLink {
		t.Fatalf("permalink = %q, want %q", parent.Permalink, wantLink)
	}
	if parent.V != export.SchemaVersion {
		t.Fatalf("every record must be stamped with the schema version, got %d", parent.V)
	}
}

func TestExport_RerunOverSameWindowAddsNothing_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("conversations.list", deAlphaList())
	f.on("auth.test", deAuthTest("https://example.slack.com/"))
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "only once")))
	hub := deHub(t, f)

	args := map[string]any{"dir": dir, "channels": "alpha"}
	if got := resultText(deCall(t, hub, "export_conversations", args)); !strings.Contains(got, "+1 new of 1 scanned") {
		t.Fatalf("first run: %q", got)
	}
	got := resultText(deCall(t, hub, "export_conversations", args))
	if !strings.Contains(got, "+0 new of 1 scanned") {
		t.Fatalf("an overlapping re-run must not duplicate: %q", got)
	}
	if recs := deReadCorpus(t, filepath.Join(dir, "primary.jsonl")); len(recs) != 1 {
		t.Fatalf("corpus should still hold one record, got %d", len(recs))
	}
}

func TestExport_PartialThreadIsDeclared_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	parentTS := deTS(2 * time.Hour)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	// Slack says five replies; only one comes back.
	f.on("conversations.history", deHistory(deRichMsg(parentTS, 5)))
	f.on("conversations.replies", deHistory(
		deRichMsg(parentTS, 5),
		deReplyJSON("U0BBBBBBBBB", deTS(90*time.Minute), parentTS, "the only answer we got"),
	))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	// "returned less while looking successful" is exactly what this note exists to stop.
	if !strings.Contains(out, ", 1 thread(s) captured partially") {
		t.Fatalf("a truncated thread must be declared in the summary: %q", out)
	}
	recs := deReadCorpus(t, filepath.Join(dir, "primary.jsonl"))
	for _, r := range recs {
		if r.TS == parentTS && (r.ReplyCount != 5 || r.RepliesFetched != 1) {
			t.Fatalf("counters must show the gap: %d/%d", r.ReplyCount, r.RepliesFetched)
		}
	}
}

func TestExport_ThreadCapBoundsOneThread_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	parentTS := deTS(2 * time.Hour)
	total := exportThreadCap + 5

	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(deRichMsg(parentTS, total)))
	replies := []string{deRichMsg(parentTS, total)}
	for i := 0; i < total; i++ {
		replies = append(replies, deReplyJSON("U0BBBBBBBBB",
			deTS(time.Duration(100-i)*time.Minute), parentTS, fmt.Sprintf("answer %d", i)))
	}
	f.on("conversations.replies", deHistory(replies...))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	if !strings.Contains(out, fmt.Sprintf("+%d new", exportThreadCap+1)) {
		t.Fatalf("one long thread must be capped, not unbounded: %q", out)
	}
	if !strings.Contains(out, "thread(s) captured partially") {
		t.Fatalf("hitting the cap is a partial capture and must say so: %q", out)
	}
}

func TestExport_RedactionIsOnByDefaultAndStable_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	// Credential-shaped, assembled at runtime so no secret-looking
	// literal is ever committed (ADR 082).
	secret := "bearer " + strings.Repeat("A", 24)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "token is "+secret+" and again "+secret)))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	if strings.Contains(out, "error") {
		t.Fatalf("export failed: %q", out)
	}
	recs := deReadCorpus(t, filepath.Join(dir, "primary.jsonl"))
	if len(recs) != 1 {
		t.Fatalf("want one record, got %d", len(recs))
	}
	if strings.Contains(recs[0].Text, secret) {
		t.Fatal("a credential-shaped span must not reach disk")
	}
	if recs[0].Redacted != 2 {
		t.Fatalf("both occurrences should be counted, got %d", recs[0].Redacted)
	}
	// Same secret → same placeholder, so a reader can still link them.
	if n := strings.Count(recs[0].Text, "[secret:sha256:"); n != 2 {
		t.Fatalf("want two placeholders, got %d in %q", n, recs[0].Text)
	}
	parts := strings.Split(recs[0].Text, "[secret:sha256:")
	if parts[1][:12] != parts[2][:12] {
		t.Fatalf("the same secret must hash to the same placeholder: %q", recs[0].Text)
	}

	// redact=false is the explicit opt-out for a disk you control.
	dir2 := t.TempDir()
	out = resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir2, "channels": "alpha", "redact": false,
	}))
	if strings.Contains(out, "error") {
		t.Fatalf("export failed: %q", out)
	}
	recs = deReadCorpus(t, filepath.Join(dir2, "primary.jsonl"))
	if !strings.Contains(recs[0].Text, secret) || recs[0].Redacted != 0 {
		t.Fatalf("redact=false must leave the text alone: %+v", recs[0])
	}
}

func TestExport_UnresolvedChannelIsSkippedNotFatal_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "still captured")))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha,ghost",
	}))
	if strings.Contains(out, "error") {
		t.Fatalf("one bad name must not abort the export: %q", out)
	}
	if !strings.Contains(out, "+1 new of 1 scanned") {
		t.Fatalf("the resolvable channel should still be captured: %q", out)
	}
}

func TestExport_HistoryFailureSkipsThatChannel_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("conversations.list", deAlphaList())
	f.onError("conversations.history", "not_in_channel")
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	if !strings.Contains(out, "+0 new of 0 scanned") {
		t.Fatalf("an unreadable channel contributes nothing: %q", out)
	}
	// Nothing captured means nothing written — no empty corpus file.
	if _, err := os.Stat(filepath.Join(dir, "primary.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("no records should mean no file, stat err = %v", err)
	}
}

func TestExport_NoUserTokenIsSkippedNotSilent_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	log := testLog()
	// Bot token only: the export needs a user-scope token and must say so
	// rather than quietly writing an empty corpus.
	cfg := &config.Config{BotToken: f.newToken(), MaxMessagesPerChannel: 50}
	hub := NewHub(slack.New(cfg, log), cfg, log)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	if !strings.Contains(out, "primary: skipped (no user token)") {
		t.Fatalf("a workspace that cannot be exported must say so, got %q", out)
	}
	if len(f.callList()) != 0 {
		t.Fatalf("no API call should be attempted, got %v", f.callList())
	}
}

func TestExport_CreateDirFailureIsReported_Behaviour(t *testing.T) {
	f := deNewFake(t)
	base := t.TempDir()
	blocker := filepath.Join(base, "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	hub := deHub(t, f)

	res := deCall(t, hub, "export_conversations", map[string]any{
		"dir": filepath.Join(blocker, "corpus"), "channels": "alpha",
	})
	if !res.IsError || !strings.Contains(resultText(res), "export: create ") {
		t.Fatalf("an unusable directory must be reported, got %q", resultText(res))
	}
}

func TestExport_AutoSelectsChannelsTheOperatorPostedIn_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("auth.test", deAuthTest("https://example.slack.com/"))
	deAnyUser(f)
	f.on("search.messages", `{"ok":true,"messages":{"total":2,"matches":[`+
		`{"ts":"`+deTS(time.Hour)+`","channel":{"id":"C0AAAAAAAAA","name":"alpha"}},`+
		`{"ts":"`+deTS(2*time.Hour)+`","channel":{"id":"D0AAAAAAAAA","name":"sam"}}`+
		`],"paging":{"count":2,"total":2,"page":1,"pages":1}}}`)
	var seen []string
	f.onFunc("conversations.history", func(r *http.Request) string {
		seen = append(seen, r.Form.Get("channel"))
		return deHistory(deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "captured"))
	})
	hub := deHub(t, f)

	// Default: DMs included.
	out := resultText(deCall(t, hub, "export_conversations", map[string]any{"dir": dir}))
	if !strings.Contains(out, "+2 new of 2 scanned") {
		t.Fatalf("both participation conversations should be captured: %q", out)
	}
	if len(seen) != 2 {
		t.Fatalf("one history call per conversation expected, got %v", seen)
	}
	// The DM is labelled as such rather than guessed at by the reader.
	kinds := map[string]string{}
	for _, r := range deReadCorpus(t, filepath.Join(dir, "primary.jsonl")) {
		kinds[r.ChannelID] = r.Kind
	}
	if kinds["D0AAAAAAAAA"] != "im" || kinds["C0AAAAAAAAA"] != "channel" {
		t.Fatalf("conversation kinds wrong: %v", kinds)
	}

	// include_dms=false drops the DM before it is ever read.
	seen = nil
	dir2 := t.TempDir()
	out = resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir2, "include_dms": false,
	}))
	if len(seen) != 1 || seen[0] != "C0AAAAAAAAA" {
		t.Fatalf("include_dms=false must not even fetch the DM, got %v", seen)
	}
	if !strings.Contains(out, "+1 new of 1 scanned") {
		t.Fatalf("only the channel should be captured: %q", out)
	}
}

func TestExport_NoParticipationIsAQuietSuccess_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("auth.test", deAuthTest("https://example.slack.com/"))
	f.on("search.messages", `{"ok":true,"messages":{"total":0,"matches":[],"paging":{"count":0,"total":0,"page":1,"pages":1}}}`)
	hub := deHub(t, f)

	res := deCall(t, hub, "export_conversations", map[string]any{"dir": dir})
	if res.IsError {
		t.Fatalf("nothing to export is not a failure: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "+0 new of 0 scanned") {
		t.Fatalf("got %q", resultText(res))
	}
}

func TestExport_ParticipationFailureIsReportedPerWorkspace_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("auth.test", deAuthTest("https://example.slack.com/"))
	f.onError("search.messages", "missing_scope")
	hub := deHub(t, f)

	res := deCall(t, hub, "export_conversations", map[string]any{"dir": dir})
	if res.IsError {
		t.Fatalf("a per-workspace failure is reported in the body, not as a tool error: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "primary: error: ") ||
		!strings.Contains(resultText(res), "missing_scope") {
		t.Fatalf("the failure must name its workspace and cause: %q", resultText(res))
	}
}

func TestExport_OneFilePerWorkspace_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("conversations.list", deAlphaList())
	f.on("auth.test", deAuthTest("https://example.slack.com/"))
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "shared fixture")))
	hub := deMultiHub(t, f, []string{"primary", "team two"})

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	for _, name := range []string{"primary", "team two"} {
		if !strings.Contains(out, name+": +1 new of 1 scanned") {
			t.Fatalf("workspace %q missing from the summary:\n%s", name, out)
		}
	}
	// A label with a separator in it must not escape into a path.
	for _, base := range []string{"primary.jsonl", "team_two.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, base)); err != nil {
			t.Fatalf("expected corpus %s: %v", base, err)
		}
	}
}

func TestExport_SingleWorkspaceSelectionAndUnknownLabel_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("conversations.list", deAlphaList())
	f.on("auth.test", deAuthTest("https://example.slack.com/"))
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "scoped")))
	hub := deMultiHub(t, f, []string{"primary", "secondary"})

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha", "workspace": "secondary",
	}))
	if strings.Contains(out, "primary:") || !strings.Contains(out, "secondary: +1 new") {
		t.Fatalf("an explicit label must scope the export:\n%s", out)
	}

	res := deCall(t, hub, "export_conversations", map[string]any{"dir": dir, "workspace": "ghost"})
	if !res.IsError || !strings.Contains(resultText(res), "ghost") {
		t.Fatalf("unknown workspace: %q", resultText(res))
	}
}

func TestExport_NonPositiveArgumentsFallBackToDefaults_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	var oldest, limit string
	f.onFunc("conversations.history", func(r *http.Request) string {
		oldest, limit = r.Form.Get("oldest"), r.Form.Get("limit")
		return deHistory()
	})
	hub := deHub(t, f)

	deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha", "hours": 0, "max_per_channel": 0,
	})
	if limit != fmt.Sprint(exportMaxPerChan) {
		t.Fatalf("max_per_channel=0 should fall back to %d, got %q", exportMaxPerChan, limit)
	}
	// The window (hours=0 -> the one-week default) is applied locally now,
	// not as a lower bound on the page: that bound made Slack return the
	// window's oldest messages and drop the newest (ADR 111).
	if oldest != "" {
		t.Fatalf("export must not anchor its page at a lower bound, got oldest=%q", oldest)
	}
}

func TestExportTool_DisabledByConfig_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deHub(t, f, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{"export_conversations": {}}
	})
	if _, ok := deCallRaw(t, hub, "export_conversations", nil).(mcp.JSONRPCError); !ok {
		t.Fatal("export_conversations should not be registered when disabled")
	}
}

func TestExport_ThreadFetchFailureIsCountedAsPartial_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	parentTS := deTS(2 * time.Hour)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(deRichMsg(parentTS, 4)))
	f.onError("conversations.replies", "thread_not_found")
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	// The parent is still captured, but the record must not claim the
	// thread came with it.
	if !strings.Contains(out, "+1 new of 1 scanned, 1 thread(s) captured partially") {
		t.Fatalf("an unreadable thread must be declared partial: %q", out)
	}
	recs := deReadCorpus(t, filepath.Join(dir, "primary.jsonl"))
	if len(recs) != 1 || recs[0].ReplyCount != 4 || recs[0].RepliesFetched != 0 {
		t.Fatalf("counters must show nothing was fetched: %+v", recs)
	}
}

func TestExport_UnopenableCorpusIsReported_Behaviour(t *testing.T) {
	f := deNewFake(t)
	dir := t.TempDir()
	// A directory sitting where the corpus file belongs.
	if err := os.MkdirAll(filepath.Join(dir, "primary.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "never lands")))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "export_conversations", map[string]any{
		"dir": dir, "channels": "alpha",
	}))
	if !strings.Contains(out, "primary: error: export: open ") {
		t.Fatalf("a corpus that cannot be opened must not read as a successful capture: %q", out)
	}
}

func TestExport_NoDirAndNoHomeIsAnError_Behaviour(t *testing.T) {
	t.Setenv("HOME", "")
	f := deNewFake(t)
	hub := deHub(t, f)

	res := deCall(t, hub, "export_conversations", map[string]any{"channels": "alpha"})
	if !res.IsError || !strings.Contains(resultText(res), "home dir unavailable") {
		t.Fatalf("with nowhere to write, say so, got %q", resultText(res))
	}
	if len(f.callList()) != 0 {
		t.Fatalf("nothing should be fetched before the destination is known: %v", f.callList())
	}
}

// ----------------------------- pure helpers -----------------------------

func TestToReactions_KeepsActors_Behaviour(t *testing.T) {
	if toReactions(nil) != nil {
		t.Fatal("no reactions must yield nil, not an empty slice in every record")
	}
	got := toReactions([]goslack.ItemReaction{
		{Name: "white_check_mark", Count: 2, Users: []string{"U0AAAAAAAAA", "U0BBBBBBBBB"}},
		{Name: "eyes", Count: 1},
	})
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "white_check_mark" || len(got[0].Users) != 2 {
		t.Fatalf("who reacted is the point: %+v", got[0])
	}
	if got[1].Name != "eyes" || len(got[1].Users) != 0 {
		t.Fatalf("an actorless reaction should still be kept: %+v", got[1])
	}
}

func TestToFileRefs_ReferencesWithoutBytes_Behaviour(t *testing.T) {
	if toFileRefs(nil) != nil {
		t.Fatal("no files must yield nil")
	}
	got := toFileRefs([]goslack.File{
		{ID: "F0AAAAAAAAA", Name: "notes.txt", Mimetype: "text/plain"},
		{ID: "F0BBBBBBBBB"},
	})
	if len(got) != 2 || got[0].ID != "F0AAAAAAAAA" || got[0].Name != "notes.txt" ||
		got[0].Mimetype != "text/plain" {
		t.Fatalf("got %+v", got)
	}
	if got[1].ID != "F0BBBBBBBBB" || got[1].Name != "" {
		t.Fatalf("a bare file id should still be referenced: %+v", got[1])
	}
}

func TestLoadCorpusKeys_Behaviour(t *testing.T) {
	dir := t.TempDir()

	// A missing corpus is the normal first run, not an error.
	if got := loadCorpusKeys(filepath.Join(dir, "absent.jsonl")); got == nil || len(got) != 0 {
		t.Fatalf("missing file should yield an empty set, got %v", got)
	}

	path := filepath.Join(dir, "corpus.jsonl")
	body := `{"v":1,"ws":"primary","ch":"C0AAAAAAAAA","ts":"1.000100"}` + "\n" +
		`{"v":1,"ws":"primary","ch":"C0AAAAAAAAA","ts":"2.000200"}` + "\n" +
		`{"v":1,"ws":"primary","ch":"C0AAAAAAAAA"}` + "\n" + // no ts → not a key
		`{not json` + "\n" // a torn last line must not block future appends
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := loadCorpusKeys(path)
	if len(keys) != 2 {
		t.Fatalf("want 2 usable keys, got %d: %v", len(keys), keys)
	}
	want := export.Record{Workspace: "primary", ChannelID: "C0AAAAAAAAA", TS: "1.000100"}.Key()
	if _, ok := keys[want]; !ok {
		t.Fatalf("key %q missing from %v", want, keys)
	}
}

func TestExportDir_HomeUnavailableIsAnError_Behaviour(t *testing.T) {
	t.Setenv("HOME", "")
	if _, err := exportDir(""); err == nil {
		t.Fatal("no dir and no home is not something to paper over")
	} else if !strings.Contains(err.Error(), "home dir unavailable") {
		t.Fatalf("error should say what is missing, got %v", err)
	}
	// An explicit dir never consults HOME.
	if got, err := exportDir("/tmp/de-corpus"); err != nil || got != "/tmp/de-corpus" {
		t.Fatalf("explicit dir must still win: %q %v", got, err)
	}
}
