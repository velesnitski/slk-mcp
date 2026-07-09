package tools

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// twoWorkspaceHub builds a Hub serving two workspaces without any network
// — slack.New only constructs clients, it issues no API calls.
func twoWorkspaceHub(t *testing.T) *Hub {
	t.Helper()
	log := testLog()
	reg := []slack.Workspace{
		{Name: "primary", Client: slack.New(&config.Config{UserToken: "xoxp-a"}, log)},
		{Name: "secondary", Client: slack.New(&config.Config{UserToken: "xoxp-b"}, log)},
	}
	return NewHubWithRegistry(reg, &config.Config{}, log)
}

func TestNewHub_SingleWorkspaceRegistry(t *testing.T) {
	log := testLog()
	cfg := &config.Config{UserToken: "xoxp-a"}
	hub := NewHub(slack.New(cfg, log), cfg, log)
	if got := len(hub.Workspaces()); got != 1 {
		t.Fatalf("NewHub should yield a 1-element registry, got %d", got)
	}
	if hub.multiWorkspace() {
		t.Fatal("single workspace should not report multiWorkspace")
	}
	if name := hub.Workspaces()[0].Name; name != "primary" {
		t.Fatalf("default label should be primary, got %q", name)
	}
}

func TestNewHub_NamesPrimaryFromConfig(t *testing.T) {
	log := testLog()
	cfg := &config.Config{
		UserToken:  "xoxp-a",
		Workspaces: []config.WorkspaceConfig{{Name: "main", UserToken: "xoxp-a"}},
	}
	hub := NewHub(slack.New(cfg, log), cfg, log)
	if name := hub.Workspaces()[0].Name; name != "main" {
		t.Fatalf("primary label should come from config, got %q", name)
	}
}

func TestNewHubWithRegistry_PrimaryIsClientZero(t *testing.T) {
	hub := twoWorkspaceHub(t)
	if !hub.multiWorkspace() {
		t.Fatal("two workspaces should report multiWorkspace")
	}
	if hub.client != hub.Workspaces()[0].Client {
		t.Fatal("h.client must be registry[0] (primary)")
	}
}

func TestNewHubWithRegistry_PanicsOnEmpty(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on empty registry")
		}
	}()
	NewHubWithRegistry(nil, &config.Config{}, testLog())
}

func TestWorkspaceTargets(t *testing.T) {
	hub := twoWorkspaceHub(t)

	if got := hub.workspaceTargets(""); len(got) != 2 {
		t.Fatalf("empty arg should target all workspaces, got %d", len(got))
	}
	if got := hub.workspaceTargets("secondary"); len(got) != 1 || got[0].Name != "secondary" {
		t.Fatalf("named arg should scope to one workspace, got %+v", got)
	}
	if got := hub.workspaceTargets("SeCoNdArY"); len(got) != 1 {
		t.Fatal("workspace match should be case-insensitive")
	}
	if got := hub.workspaceTargets("nope"); got != nil {
		t.Fatalf("unknown workspace should return nil, got %+v", got)
	}
}

func TestWithClient_RetargetsClientAndConfig(t *testing.T) {
	hub := twoWorkspaceHub(t)
	secondary := hub.Workspaces()[1].Client
	scoped := hub.withClient(secondary)

	if scoped.client != secondary {
		t.Fatal("withClient should swap the client")
	}
	if scoped.cfg != secondary.Config() {
		t.Fatal("withClient should swap cfg to the client's own config")
	}
	// Parent is untouched (no shared mutation).
	if hub.client == secondary {
		t.Fatal("withClient must not mutate the parent hub")
	}
}

func TestScopedWorkspace(t *testing.T) {
	hub := twoWorkspaceHub(t)

	scoped, name, errRes := hub.scopedWorkspace("")
	if errRes != nil || name != "primary" || scoped.client != hub.Workspaces()[0].Client {
		t.Fatalf("empty arg should scope to primary, got name=%q errRes=%v", name, errRes)
	}

	scoped, name, errRes = hub.scopedWorkspace("SeCoNdArY")
	if errRes != nil || name != "secondary" || scoped.client != hub.Workspaces()[1].Client {
		t.Fatalf("named arg should scope case-insensitively, got name=%q errRes=%v", name, errRes)
	}

	if _, _, errRes = hub.scopedWorkspace("ghost"); errRes == nil || !errRes.IsError {
		t.Fatal("unknown workspace should yield a ready error result")
	}
}

func TestResolveMessageRef(t *testing.T) {
	const link = "https://example.slack.com/archives/C0TESTCHAN/p1714000000000123?thread_ts=1713990000.000111"

	// Permalink fills both; useThreadTS picks the thread root.
	ch, ts, errRes := resolveMessageRef(link, "", "", true)
	if errRes != nil || ch != "C0TESTCHAN" || ts != "1713990000.000111" {
		t.Fatalf("thread mode: got ch=%q ts=%q errRes=%v", ch, ts, errRes)
	}

	// useThreadTS=false takes the message's own ts.
	_, ts, errRes = resolveMessageRef(link, "", "", false)
	if errRes != nil || ts != "1714000000.000123" {
		t.Fatalf("message mode: got ts=%q errRes=%v", ts, errRes)
	}

	// Explicit args win over the permalink.
	ch, ts, errRes = resolveMessageRef(link, "general", "9.9", false)
	if errRes != nil || ch != "general" || ts != "9.9" {
		t.Fatalf("explicit args must win, got ch=%q ts=%q errRes=%v", ch, ts, errRes)
	}

	// Unparseable permalink is an error.
	if _, _, errRes = resolveMessageRef("not a permalink", "", "", false); errRes == nil || !errRes.IsError {
		t.Fatal("garbage permalink should error")
	}

	// Nothing provided → field-specific errors.
	if _, _, errRes = resolveMessageRef("", "", "", false); errRes == nil || !errRes.IsError {
		t.Fatal("missing channel should error")
	}
	if _, _, errRes = resolveMessageRef("", "general", "", true); errRes == nil || !errRes.IsError {
		t.Fatal("missing thread_ts should error")
	}
}

func TestRunUnreadSummary_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	// workspaceTargets returns nil before any API call, so this exercises
	// the error path with no network.
	res := hub.runUnreadSummary(context.Background(), unreadParams{}, "ghost")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should produce an error result, got %+v", res)
	}
}

func TestRunMentions_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runMentions(context.Background(), mentionParams{}, "ghost")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should produce an error result, got %+v", res)
	}
}

func TestResolveMaxChars(t *testing.T) {
	cases := []struct {
		maxChars, n, want int
	}{
		{maxCharsAuto, 2, DefaultTotalMaxChars / 2}, // auto, 2 workspaces → split
		{maxCharsAuto, 1, DefaultTotalMaxChars},     // auto, single workspace → full
		{maxCharsAuto, 0, DefaultTotalMaxChars},     // guard: n<1 treated as 1
		{0, 2, 0},                                   // explicit 0 → unlimited, untouched
		{500, 3, 500},                               // explicit cap → hard per-workspace
	}
	for _, c := range cases {
		if got := resolveMaxChars(c.maxChars, c.n); got != c.want {
			t.Fatalf("resolveMaxChars(%d,%d)=%d want %d", c.maxChars, c.n, got, c.want)
		}
	}
}

func TestWorkspaceTarget(t *testing.T) {
	hub := twoWorkspaceHub(t)

	if got := hub.workspaceTarget(""); got == nil || got.Name != "primary" {
		t.Fatalf("empty arg should default to primary, got %+v", got)
	}
	if got := hub.workspaceTarget("secondary"); got == nil || got.Name != "secondary" {
		t.Fatalf("named arg should select that workspace, got %+v", got)
	}
	if got := hub.workspaceTarget("SeCoNdArY"); got == nil || got.Name != "secondary" {
		t.Fatal("workspace match should be case-insensitive")
	}
	if got := hub.workspaceTarget("nope"); got != nil {
		t.Fatalf("unknown workspace should return nil, got %+v", got)
	}
}

func TestWorkspaceTarget_SingleDefaultsToPrimary(t *testing.T) {
	log := testLog()
	cfg := &config.Config{UserToken: "xoxp-a"}
	hub := NewHub(slack.New(cfg, log), cfg, log)
	// Empty arg on a one-workspace hub must resolve, not error.
	if got := hub.workspaceTarget(""); got == nil || got.Client != hub.client {
		t.Fatalf("single-workspace empty arg should resolve to the primary client, got %+v", got)
	}
}

func TestWsLabel(t *testing.T) {
	// Multi-workspace: label disambiguates the write target.
	if got := twoWorkspaceHub(t).wsLabel("secondary"); got != " [secondary]" {
		t.Fatalf("multi-workspace wsLabel: %q", got)
	}
	// Single-workspace: silent, so confirmations stay byte-identical to before.
	log := testLog()
	cfg := &config.Config{UserToken: "xoxp-a"}
	if got := NewHub(slack.New(cfg, log), cfg, log).wsLabel("primary"); got != "" {
		t.Fatalf("single-workspace wsLabel should be empty, got %q", got)
	}
}

func TestRunPostMessage_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	// workspaceTarget returns nil before any Slack call, so this exercises
	// the routing error path with no network.
	res := hub.runPostMessage(context.Background(), "ghost", "general", "hi", "", 0)
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should produce an error result, got %+v", res)
	}
}

func TestRunListChannels_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runListChannels(context.Background(), "ghost", 10, false)
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should produce an error result, got %+v", res)
	}
}

// resultText extracts the rendered text from a tool result for assertions.
func resultText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestRunDeleteMessage_RequiresTarget(t *testing.T) {
	hub := twoWorkspaceHub(t)
	// Neither a permalink nor channel+ts → guidance error, no Slack call.
	for _, c := range []struct{ channel, ts, link string }{
		{"", "", ""},             // nothing
		{"general", "", ""},      // channel but no ts, no permalink
		{"", "1700000000.1", ""}, // ts but no channel, no permalink
	} {
		res := hub.runDeleteMessage(context.Background(), "", c.channel, c.ts, c.link)
		if res == nil || !res.IsError || !strings.Contains(resultText(res), "provide a permalink") {
			t.Fatalf("missing target should ask for permalink/channel+ts, got %q", resultText(res))
		}
	}
}

func TestRunDeleteMessage_InvalidPermalink(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runDeleteMessage(context.Background(), "", "", "", "https://example.com/not-a-slack-link")
	if res == nil || !res.IsError || !strings.Contains(resultText(res), "invalid permalink") {
		t.Fatalf("bad permalink should error before any Slack call, got %q", resultText(res))
	}
}

func TestRunDeleteMessage_PermalinkFillsTargetThenWorkspaceChecked(t *testing.T) {
	hub := twoWorkspaceHub(t)
	// A VALID permalink fills channel+ts, so we sail past the "provide target"
	// guard and fail only at workspace resolution — proving the permalink path
	// supplied both fields (otherwise we'd see the target error instead).
	link := "https://x.slack.com/archives/C0ABC1234DE/p1700000000000123"
	res := hub.runDeleteMessage(context.Background(), "ghost", "", "", link)
	if res == nil || !res.IsError || !strings.Contains(resultText(res), "unknown workspace") {
		t.Fatalf("valid permalink + bad workspace should reach the workspace check, got %q", resultText(res))
	}
}

func TestRecentSelfDuplicate_NoUserTokenFailsOpen(t *testing.T) {
	// Without a user token we can't attribute authorship, so the dedup
	// guard must fail OPEN (return false → caller still posts). No network.
	log := testLog()
	cfg := &config.Config{} // no user token → HasUserToken() == false
	hub := NewHub(slack.New(cfg, log), cfg, log)
	if hub.recentSelfDuplicate(context.Background(), "C0123456789", "hi", 30) {
		t.Fatal("no user token must fail open (return false so the post proceeds)")
	}
}

func TestRunDeleteMessage_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runDeleteMessage(context.Background(), "ghost", "general", "1700000000.000100", "")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should produce an error result, got %+v", res)
	}
}

func TestDeleteErrorHint(t *testing.T) {
	if got := deleteErrorHint(errString("chat.delete: cant_delete_message")); !strings.Contains(got, "only delete messages that identity posted") {
		t.Fatalf("cant_delete_message hint missing: %q", got)
	}
	if got := deleteErrorHint(errString("chat.delete: message_not_found")); !strings.Contains(got, "already deleted") {
		t.Fatalf("message_not_found hint missing: %q", got)
	}
	if got := deleteErrorHint(errString("boom")); got != "boom" {
		t.Fatalf("unknown error should pass through, got %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestWorkspaceHelpers_PureFormatting(t *testing.T) {
	if got := workspaceSection("primary", "body"); got != "## [primary]\nbody" {
		t.Fatalf("workspaceSection: %q", got)
	}
	if got := unreadEmptyMsg(true); got != "no unread channels mention you" {
		t.Fatalf("unreadEmptyMsg(mentionsOnly): %q", got)
	}
	if got := unreadEmptyMsg(false); got != "all caught up — 0 unread" {
		t.Fatalf("unreadEmptyMsg: %q", got)
	}
	if got := mentionsEmptyMsg(false, 72); got != "no mentions in last 72h" {
		t.Fatalf("mentionsEmptyMsg: %q", got)
	}
}
func TestRunListUsers_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runListUsers(context.Background(), "ghost", false, false, "")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}
