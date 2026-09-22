package tools

import (
	"context"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// tmTwoFakeHub builds a two-workspace Hub whose clients talk to a and b.
// Needed for the permalink-host routing rules, which are only observable
// once more than one workspace is configured.
func tmTwoFakeHub(t *testing.T, a, b *fakeSlack) *Hub {
	t.Helper()
	log := testLog()
	cfgA, cfgB := newFakeConfig(t, a), newFakeConfig(t, b)
	return NewHubWithRegistry([]slack.Workspace{
		{Name: "alpha", Client: slack.New(cfgA, log)},
		{Name: "beta", Client: slack.New(cfgB, log)},
	}, cfgA, log)
}

// ---------------------------------------------------------------- get_message

func TestGetMessage_RendersOneMessageVerbatim(t *testing.T) {
	long := strings.Repeat("y", 500)
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"`+long+`","ts":"`+tmRootTS+`"}`,
	))
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{
		"channel": "alpha", "ts": tmRootTS,
	}))

	tmWant(t, out, "message in #alpha [primary]", "from: alex", "chars: 500", long)
	// The drill-in exists to escape truncation: no "(+N chars)" marker.
	tmNotWant(t, out, "chars)")
}

func TestGetMessage_ReportsEditedAndReactionsAndFiles(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"release notes","ts":"`+tmRootTS+`",
		  "edited":{"user":"U1","ts":"`+tmRootTS+`"},
		  "reply_count":4,
		  "files":[{"id":"F1","name":"notes.txt","mimetype":"text/plain","size":42}],
		  "reactions":[{"name":"thumbsup","count":3,"users":["U1"]}]}`,
	))
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{
		"channel": "alpha", "ts": tmRootTS,
	}))

	tmWant(t, out,
		"(edited)",
		"thread parent: 4 replies",
		"files:",
		"notes.txt (text/plain, 42 bytes)",
		"reactions: :thumbsup: ×3",
	)
}

func TestGetMessage_FileWithoutANameFallsBackToItsID(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"see attached","ts":"`+tmRootTS+`",
		  "files":[{"id":"F1","mimetype":"audio/mp4","size":7}]}`,
	))
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	tmWant(t, resultText(tmCall(t, s, "get_message", map[string]any{
		"channel": "alpha", "ts": tmRootTS,
	})), "- F1 (audio/mp4, 7 bytes)")
}

func TestGetMessage_EmptyTextStillReportsItsPayload(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"","ts":"`+tmRootTS+`",
		  "attachments":[{"text":"build finished","fallback":"build finished"}]}`,
	))
	tmUsers(f, map[string]string{"U1": "alex"})
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{
		"channel": "alpha", "ts": tmRootTS,
	}))

	// "chars: 0" alone would read as "this message is blank".
	tmWant(t, out, "chars: 0", "payload:", "build finished")
}

func TestGetMessage_ForwardedMessagePointsAtTheOriginal(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"","ts":"`+tmRootTS+`",
		  "attachments":[{"ts":"1699000000.000001","author_name":"Sam"}]}`,
	))
	tmUsers(f, map[string]string{"U1": "alex"})
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	tmWant(t, resultText(tmCall(t, s, "get_message", map[string]any{
		"channel": "alpha", "ts": tmRootTS,
	})), "forwarded from a message at ts=1699000000.000001")
}

func TestGetMessage_ThreadReplyCarriesItsParentLine(t *testing.T) {
	link := tmHost + "/archives/C0ALPHA001/p1700000100000200?thread_ts=" + tmRootTS
	f := newFakeSlack(t)
	tmAuthTest(f, "U1", tmHost+"/")
	tmUsers(f, map[string]string{"U1": "alex", "U2": "sam"})
	f.On("conversations.info", `{"ok":true,"channel":{"id":"C0ALPHA001","name":"alpha"}}`)
	f.On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`"}`,
		`{"type":"message","user":"U2","text":"an answer","ts":"`+tmReplyTS+`","thread_ts":"`+tmRootTS+`"}`,
	))
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"permalink": link}))

	tmWant(t, out, "message in #alpha [primary]", "from: sam",
		"reply in thread of: [alex] root question", "an answer")
	tmNotWant(t, out, "thread parent:")
}

func TestGetMessage_ParentWithoutAUserIDStillRenders(t *testing.T) {
	link := tmHost + "/archives/C0ALPHA001/p1700000100000200?thread_ts=" + tmRootTS
	f := newFakeSlack(t)
	tmAuthTest(f, "U1", tmHost+"/")
	tmUsers(f, map[string]string{"U2": "sam"})
	f.On("conversations.info", `{"ok":true,"channel":{"id":"C0ALPHA001","name":"alpha"}}`)
	// A bot-posted parent carries a username, never a user id.
	f.On("conversations.replies", tmHistoryBody(
		`{"type":"message","username":"build-bot","text":"nightly build","ts":"`+tmRootTS+`"}`,
		`{"type":"message","user":"U2","text":"an answer","ts":"`+tmReplyTS+`","thread_ts":"`+tmRootTS+`"}`,
	))
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"permalink": link}))

	tmWant(t, out, "reply in thread of:", "nightly build", "an answer")
}

func TestGetMessage_BotMessageIsAttributedToItsUsername(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","username":"build-bot","text":"build ok","ts":"`+tmRootTS+`"}`,
	))
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	tmWant(t, resultText(tmCall(t, s, "get_message", map[string]any{
		"channel": "alpha", "ts": tmRootTS,
	})), "from: build-bot")
}

func TestGetMessage_MissingTargetIsRejectedBeforeAnyCall(t *testing.T) {
	f := newFakeSlack(t)
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	for _, args := range []map[string]any{
		{},
		{"channel": "alpha"},
		{"ts": tmRootTS},
	} {
		res := tmCall(t, s, "get_message", args)
		if !res.IsError {
			t.Fatalf("args %v should error, got %q", args, resultText(res))
		}
		tmWant(t, resultText(res), "pass a permalink, or channel + ts")
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("validation must precede every Slack call, got %v", f.Calls())
	}
}

func TestGetMessage_UnparseablePermalinkIsAnError(t *testing.T) {
	f := newFakeSlack(t)
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	res := tmCall(t, s, "get_message", map[string]any{"permalink": "https://example.com/not-a-link"})
	if !res.IsError {
		t.Fatalf("garbage permalink should error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "permalink could not be parsed")
}

func TestGetMessage_UnknownChannelIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"beta": "C2"})
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	res := tmCall(t, s, "get_message", map[string]any{"channel": "alpha", "ts": tmRootTS})
	if !res.IsError {
		t.Fatalf("unknown channel should error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "#alpha not found")
}

func TestGetMessage_LookupFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.OnError("conversations.history", "channel_not_found")
	f.OnError("conversations.replies", "thread_not_found")
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	res := tmCall(t, s, "get_message", map[string]any{"channel": "alpha", "ts": tmRootTS})
	if !res.IsError {
		t.Fatalf("a failed lookup must not read as success: %q", resultText(res))
	}
	tmWant(t, resultText(res), "conversations.history")
}

func TestGetMessage_UnknownWorkspaceIsRejected(t *testing.T) {
	f := newFakeSlack(t)
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	res := tmCall(t, s, "get_message", map[string]any{
		"channel": "alpha", "ts": tmRootTS, "workspace": "ghost",
	})
	if !res.IsError {
		t.Fatalf("unknown workspace should error, got %q", resultText(res))
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("routing must fail before any Slack call, got %v", f.Calls())
	}
}

func TestRegisterMessageTools_HonoursTheDisabledList(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{"get_message": {}}
	})
	if names := tmToolNames(t, tmServer(t, h.registerMessageTools)); len(names) != 0 {
		t.Fatalf("get_message was disabled, yet %v registered", names)
	}
}

func TestRegisterMessageTools_RegistersGetMessage(t *testing.T) {
	f := newFakeSlack(t)
	if names := tmToolNames(t, tmServer(t, newFakeHub(t, f).registerMessageTools)); !tmHas(names, "get_message") {
		t.Fatalf("get_message should be registered, got %v", names)
	}
}

// -------------------------------------------------------------- routeWorkspace

func TestRouteWorkspace_ExplicitLabelBeatsThePermalinkHost(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	tmAuthTest(a, "U1", tmHost+"/")
	tmAuthTest(b, "U2", "https://beta.example.com/")
	h := tmTwoFakeHub(t, a, b)

	scoped, name, note, errRes := h.routeWorkspace(context.Background(), "beta",
		tmHost+"/archives/C0ALPHA001/p1700000000000100")

	if errRes != nil || name != "beta" || note != "" {
		t.Fatalf("explicit label must win: name=%q note=%q errRes=%v", name, note, errRes)
	}
	if scoped.client != h.Workspaces()[1].Client {
		t.Fatal("explicit label must scope to that workspace's client")
	}
	// Auto-detection is skipped entirely, so no identity call is made.
	if a.Called("auth.test") || b.Called("auth.test") {
		t.Fatalf("no host probing expected; a=%v b=%v", a.Calls(), b.Calls())
	}
}

func TestRouteWorkspace_PermalinkHostPicksItsWorkspace(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	tmAuthTest(a, "U1", tmHost+"/")
	tmAuthTest(b, "U2", "https://beta.example.com/")
	h := tmTwoFakeHub(t, a, b)

	scoped, name, note, errRes := h.routeWorkspace(context.Background(), "",
		"https://beta.example.com/archives/C0BETA0001/p1700000000000100")

	if errRes != nil || name != "beta" {
		t.Fatalf("host match should pick beta, got name=%q errRes=%v", name, errRes)
	}
	if note != "workspace auto-detected from permalink" {
		t.Fatalf("a multi-workspace auto-detection should be announced, got note=%q", note)
	}
	if scoped.client != h.Workspaces()[1].Client {
		t.Fatal("routing did not retarget the client")
	}
}

func TestRouteWorkspace_UnmatchedHostFallsBackWithANote(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	tmAuthTest(a, "U1", tmHost+"/")
	tmAuthTest(b, "U2", "https://beta.example.com/")
	h := tmTwoFakeHub(t, a, b)

	_, name, note, errRes := h.routeWorkspace(context.Background(), "",
		"https://gamma.example.com/archives/C0GAMMA001/p1700000000000100")

	if errRes != nil || name != "alpha" {
		t.Fatalf("no match should fall back to the primary, got name=%q errRes=%v", name, errRes)
	}
	tmWant(t, note, "no configured workspace matches host", "gamma.example.com", "tried the primary")
}

func TestRouteWorkspace_SkipsWorkspacesThatCannotSelfIdentify(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	a.OnError("auth.test", "not_allowed_token_type") // bot-only: no team url
	tmAuthTest(b, "U2", "https://beta.example.com/")
	h := tmTwoFakeHub(t, a, b)

	_, name, _, errRes := h.routeWorkspace(context.Background(), "",
		"https://beta.example.com/archives/C0BETA0001/p1700000000000100")

	if errRes != nil || name != "beta" {
		t.Fatalf("an unidentifiable workspace must be skipped, not fatal: name=%q errRes=%v", name, errRes)
	}
}

func TestRouteWorkspace_NoPermalinkGoesStraightToThePrimary(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	h := tmTwoFakeHub(t, a, b)

	_, name, note, errRes := h.routeWorkspace(context.Background(), "", "")

	if errRes != nil || name != "alpha" || note != "" {
		t.Fatalf("no permalink → primary, got name=%q note=%q errRes=%v", name, note, errRes)
	}
	if len(a.Calls())+len(b.Calls()) != 0 {
		t.Fatalf("nothing to route means no API calls, got a=%v b=%v", a.Calls(), b.Calls())
	}
}

func TestRouteWorkspace_HostlessPermalinkGoesToThePrimary(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	h := tmTwoFakeHub(t, a, b)

	// A relative link parses as a permalink but carries no host to match.
	_, name, note, errRes := h.routeWorkspace(context.Background(), "",
		"/archives/C0ALPHA001/p1700000000000100")

	if errRes != nil || name != "alpha" || note != "" {
		t.Fatalf("hostless permalink → primary, got name=%q note=%q errRes=%v", name, note, errRes)
	}
}

func TestRouteWorkspace_UnparseableTeamURLIsNotAMatch(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	// auth.test answers, but with a url that carries no host — the
	// workspace identifies itself and still cannot be matched.
	tmAuthTest(a, "U1", "not-a-url")
	tmAuthTest(b, "U2", "also-not-a-url")
	h := tmTwoFakeHub(t, a, b)

	_, name, note, errRes := h.routeWorkspace(context.Background(), "",
		tmHost+"/archives/C0ALPHA001/p1700000000000100")

	if errRes != nil || name != "alpha" {
		t.Fatalf("no host to compare → primary, got name=%q errRes=%v", name, errRes)
	}
	tmWant(t, note, "no configured workspace matches host")
}

func TestRouteWorkspace_UnknownLabelIsAnError(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	h := tmTwoFakeHub(t, a, b)

	_, _, _, errRes := h.routeWorkspace(context.Background(), "ghost", "")
	if errRes == nil || !errRes.IsError {
		t.Fatal("an unknown label must surface as an error result")
	}
}

// -------------------------------------------------- fetchMessageWithParent

func TestFetchMessageWithParent_FindsTheReplyAndItsParent(t *testing.T) {
	f := newFakeSlack(t).On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`"}`,
		`{"type":"message","user":"U2","text":"an answer","ts":"`+tmReplyTS+`","thread_ts":"`+tmRootTS+`"}`,
	))
	h := newFakeHub(t, f)

	msg, parent, err := h.fetchMessageWithParent(context.Background(), "C1", tmReplyTS, tmRootTS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Text != "an answer" {
		t.Fatalf("wrong target message: %q", msg.Text)
	}
	if parent == nil || parent.Text != "root question" {
		t.Fatalf("parent not returned: %+v", parent)
	}
}

func TestFetchMessageWithParent_MissingReplyIsNamedPrecisely(t *testing.T) {
	f := newFakeSlack(t).On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`"}`,
	))
	h := newFakeHub(t, f)

	_, _, err := h.fetchMessageWithParent(context.Background(), "C1", tmReplyTS, tmRootTS)
	if err == nil {
		t.Fatal("a reply that is not in the thread must be an error")
	}
	tmWant(t, err.Error(), "no reply at ts "+tmReplyTS, "in thread "+tmRootTS)
}

func TestFetchMessageWithParent_ThreadLookupErrorIsWrapped(t *testing.T) {
	f := newFakeSlack(t).OnError("conversations.replies", "thread_not_found")
	h := newFakeHub(t, f)

	_, _, err := h.fetchMessageWithParent(context.Background(), "C1", tmReplyTS, tmRootTS)
	if err == nil {
		t.Fatal("expected an error")
	}
	tmWant(t, err.Error(), "thread lookup:", "thread_not_found")
}

func TestFetchMessageWithParent_TopLevelMessageHasNoParent(t *testing.T) {
	f := newFakeSlack(t).On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`"}`,
	))
	h := newFakeHub(t, f)

	msg, parent, err := h.fetchMessageWithParent(context.Background(), "C1", tmRootTS, tmRootTS)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if parent != nil {
		t.Fatalf("a top-level message has no parent, got %+v", parent)
	}
	if msg.Text != "root question" {
		t.Fatalf("wrong message: %q", msg.Text)
	}
	if f.Called("conversations.replies") {
		t.Fatalf("a point lookup that hit should not fall back, calls=%v", f.Calls())
	}
}

// ------------------------------------------------------------- gatherForRefs

func TestGatherForRefs_IncludesTheParentOnlyWhenThereIsOne(t *testing.T) {
	msg := &goslack.Message{}
	msg.Text = "reply"
	parent := &goslack.Message{}
	parent.Text = "root"

	if got := gatherForRefs(msg, nil); len(got) != 1 || got[0].Text != "reply" {
		t.Fatalf("without a parent the message stands alone, got %+v", got)
	}
	got := gatherForRefs(msg, parent)
	if len(got) != 2 || got[1].Text != "root" {
		t.Fatalf("with a parent both bodies are scanned, got %+v", got)
	}
}
