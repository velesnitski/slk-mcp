package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// Fixture constants — generic placeholders only.
const (
	tmRootTS  = "1700000000.000100"
	tmReplyTS = "1700000100.000200"
	tmHost    = "https://alpha.example.com"
)

// tmThreadServer wires only the thread tools onto a fresh MCP server.
func tmThreadServer(t *testing.T, h *Hub) *server.MCPServer {
	t.Helper()
	return tmServer(t, h.registerThreadTools)
}

// ---------------------------------------------------------------- get_thread

func TestGetThread_RendersEveryReplyWithNames(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex", "U2": "sam"})
	f.On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`","reply_count":1}`,
		`{"type":"message","user":"U2","text":"an answer","ts":"`+tmReplyTS+`","thread_ts":"`+tmRootTS+`"}`,
	))
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "get_thread", map[string]any{
		"channel": "alpha", "thread_ts": tmRootTS,
	}))

	tmWant(t, out, "thread #alpha (2 msgs)", "root question", "an answer", "alex", "sam")
	if !f.Called("conversations.replies") {
		t.Fatalf("conversations.replies not called; calls=%v", f.Calls())
	}
}

func TestGetThread_FullTextDisablesTruncation(t *testing.T) {
	long := strings.Repeat("x", 400)
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"`+long+`","ts":"`+tmRootTS+`"}`,
	))
	s := tmThreadServer(t, newFakeHub(t, f))

	compact := resultText(tmCall(t, s, "get_thread", map[string]any{
		"channel": "alpha", "thread_ts": tmRootTS,
	}))
	tmWant(t, compact, "(+120 chars)")

	full := resultText(tmCall(t, s, "get_thread", map[string]any{
		"channel": "alpha", "thread_ts": tmRootTS, "full_text": true,
	}))
	tmNotWant(t, full, "(+120 chars)")
	if !strings.Contains(full, long) {
		t.Fatalf("full_text should render the whole body, got %d chars", len(full))
	}
}

func TestGetThread_PermalinkFillsChannelAndThreadTS(t *testing.T) {
	link := tmHost + "/archives/C0ALPHA001/p1700000000000100"
	f := newFakeSlack(t)
	tmAuthTest(f, "U1", tmHost+"/")
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`"}`,
	))
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "get_thread", map[string]any{"permalink": link}))

	tmWant(t, out, "thread C0ALPHA001 (1 msgs)", "root question")
	// A canonical id short-circuits name resolution entirely.
	if f.Called("conversations.list") {
		t.Fatalf("permalink carries a channel id; no name lookup expected; calls=%v", f.Calls())
	}
}

func TestGetThread_MissingArgsAreRejectedBeforeAnyCall(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_thread", map[string]any{})
	if !res.IsError {
		t.Fatalf("no channel and no permalink must error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "channel is required")

	res = tmCall(t, s, "get_thread", map[string]any{"channel": "alpha"})
	tmWant(t, resultText(res), "thread_ts is required")

	if len(f.Calls()) != 0 {
		t.Fatalf("validation must precede every Slack call, got %v", f.Calls())
	}
}

func TestGetThread_UnparseablePermalinkIsAnError(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_thread", map[string]any{"permalink": "https://example.com/nope"})
	if !res.IsError {
		t.Fatalf("garbage permalink should error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "permalink could not be parsed")
}

func TestGetThread_UnknownChannelSurfacesResolveError(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"beta": "C2"})
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_thread", map[string]any{"channel": "alpha", "thread_ts": tmRootTS})
	if !res.IsError {
		t.Fatalf("unknown channel should error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "#alpha not found")
}

func TestGetThread_RepliesFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.OnError("conversations.replies", "thread_not_found")
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_thread", map[string]any{"channel": "alpha", "thread_ts": tmRootTS})
	if !res.IsError {
		t.Fatalf("replies failure should error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "conversations.replies", "thread_not_found")
}

func TestGetThread_UnknownWorkspaceIsRejected(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_thread", map[string]any{
		"channel": "alpha", "thread_ts": tmRootTS, "workspace": "ghost",
	})
	if !res.IsError {
		t.Fatalf("unknown workspace should error, got %q", resultText(res))
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("routing must fail before any Slack call, got %v", f.Calls())
	}
}

// --------------------------------------------------------- get_user_messages

func tmSearchMatch(channelID, channelName, username, ts, text, permalink string) string {
	return `{"type":"message","channel":{"id":"` + channelID + `","name":"` + channelName +
		`"},"user":"U2","username":"` + username + `","ts":"` + ts +
		`","text":"` + text + `","permalink":"` + permalink + `"}`
}

func TestGetUserMessages_RendersHitsAndBuildsQuery(t *testing.T) {
	var gotQuery string
	f := newFakeSlack(t)
	f.OnFunc("search.messages", func(r *http.Request) string {
		gotQuery = r.Form.Get("query")
		return tmSearchBody(tmSearchMatch("C1", "alpha", "sam", tmReplyTS, "shipped it",
			tmHost+"/archives/C1/p1700000100000200"))
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "get_user_messages", map[string]any{
		"user": "sam", "channel": "#alpha", "since": "2026-01-01", "until": "2026-02-01",
	}))

	tmWant(t, out, "1 msgs from sam", "shipped it", "#alpha")
	if gotQuery != "from:@sam in:#alpha after:2026-01-01 before:2026-02-01" {
		t.Fatalf("query not assembled as expected: %q", gotQuery)
	}
}

func TestGetUserMessages_NoHitsSaysSo(t *testing.T) {
	f := newFakeSlack(t).On("search.messages", tmSearchBody())
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_user_messages", map[string]any{"user": "sam"})
	if res.IsError {
		t.Fatalf("an empty result set is not an error: %q", resultText(res))
	}
	tmWant(t, resultText(res), "no messages from sam")
}

func TestGetUserMessages_BadDateBoundsAreRejected(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	for arg, want := range map[string]string{"since": "since must be YYYY-MM-DD", "until": "until must be YYYY-MM-DD"} {
		res := tmCall(t, s, "get_user_messages", map[string]any{"user": "sam", arg: "01/02/2026"})
		if !res.IsError {
			t.Fatalf("%s=01/02/2026 should error, got %q", arg, resultText(res))
		}
		tmWant(t, resultText(res), want)
	}
	if f.Called("search.messages") {
		t.Fatalf("date validation must precede the search, calls=%v", f.Calls())
	}
}

func TestGetUserMessages_MissingUserIsRejected(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_user_messages", map[string]any{})
	if !res.IsError {
		t.Fatalf("user is required, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "user is required")
}

func TestGetUserMessages_SearchFailureIsReported(t *testing.T) {
	f := newFakeSlack(t).OnError("search.messages", "not_allowed_token_type")
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "get_user_messages", map[string]any{"user": "sam"})
	if !res.IsError {
		t.Fatalf("search failure should error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "search.messages", "not_allowed_token_type")
}

func TestGetUserMessages_ThreadContextInlinesParentOncePerThread(t *testing.T) {
	threaded := "?thread_ts=" + tmRootTS
	f := newFakeSlack(t)
	f.On("search.messages", tmSearchBody(
		tmSearchMatch("C1", "alpha", "sam", tmReplyTS, "ok", tmHost+"/archives/C1/p1700000100000200"+threaded),
		tmSearchMatch("C1", "alpha", "sam", "1700000200.000300", "got it", tmHost+"/archives/C1/p1700000200000300"+threaded),
		// Top-level hit: its permalink carries no thread_ts, so ExtractThreadTS
		// equals its own ts and no parent fetch is due.
		tmSearchMatch("C1", "alpha", "sam", "1700000300.000400", "standalone", tmHost+"/archives/C1/p1700000300000400"),
	))
	f.On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`"}`,
		`{"type":"message","user":"U2","text":"ok","ts":"`+tmReplyTS+`","thread_ts":"`+tmRootTS+`"}`,
	))
	tmUsers(f, map[string]string{"U1": "alex"})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "get_user_messages", map[string]any{
		"user": "sam", "with_thread_context": true,
	}))

	tmWant(t, out, "3 msgs from sam", "↑", "root question", "alex")
	if got := strings.Count(out, "root question"); got != 2 {
		t.Fatalf("both replies in the thread should carry the parent line, got %d", got)
	}
	if n := tmCountCalls(f, "conversations.replies"); n != 1 {
		t.Fatalf("one thread means one conversations.replies call, got %d", n)
	}
}

func TestGetUserMessages_WithoutThreadContextFetchesNoParents(t *testing.T) {
	f := newFakeSlack(t).On("search.messages", tmSearchBody(
		tmSearchMatch("C1", "alpha", "sam", tmReplyTS, "ok",
			tmHost+"/archives/C1/p1700000100000200?thread_ts="+tmRootTS),
	))
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "get_user_messages", map[string]any{"user": "sam"}))

	tmNotWant(t, out, "↑")
	if f.Called("conversations.replies") {
		t.Fatalf("default must not pay for parent fetches, calls=%v", f.Calls())
	}
}

// ------------------------------------------------------ fetchThreadParents

func tmHit(channelID, ts, permalink string) goslack.SearchMessage {
	var m goslack.SearchMessage
	m.Channel.ID = channelID
	m.Timestamp = ts
	m.Permalink = permalink
	return m
}

func TestFetchThreadParents_SkipsNonRepliesAndDedupes(t *testing.T) {
	f := newFakeSlack(t).On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U1","text":"root question","ts":"`+tmRootTS+`"}`,
	))
	h := newFakeHub(t, f)

	threaded := tmHost + "/archives/C1/pX?thread_ts=" + tmRootTS
	parents := h.fetchThreadParents(context.Background(), []goslack.SearchMessage{
		tmHit("C1", tmReplyTS, threaded),           // reply → fetch
		tmHit("C1", "1700000200.000300", threaded), // same thread → dedup
		tmHit("C1", tmRootTS, ""),                  // parent itself (thread_ts == ts)
		tmHit("", tmReplyTS, threaded),             // no channel id
		tmHit("C1", "1700000400.000500", ""),       // no permalink → thread_ts == ts
	})

	if len(parents) != 1 {
		t.Fatalf("exactly one thread should have been fetched, got %d: %v", len(parents), parents)
	}
	if n := tmCountCalls(f, "conversations.replies"); n != 1 {
		t.Fatalf("dedup failed: %d conversations.replies calls", n)
	}
	if got := parents["C1|"+tmRootTS].Text; got != "root question" {
		t.Fatalf("parent should be replies[0], got %q", got)
	}
}

func TestFetchThreadParents_FailuresAreBestEffort(t *testing.T) {
	f := newFakeSlack(t).OnError("conversations.replies", "thread_not_found")
	h := newFakeHub(t, f)

	parents := h.fetchThreadParents(context.Background(), []goslack.SearchMessage{
		tmHit("C1", tmReplyTS, tmHost+"/archives/C1/pX?thread_ts="+tmRootTS),
	})
	if len(parents) != 0 {
		t.Fatalf("a failed fetch must not invent a parent, got %v", parents)
	}
}

func TestFetchThreadParents_EmptyThreadYieldsNoParent(t *testing.T) {
	f := newFakeSlack(t).On("conversations.replies", tmHistoryBody())
	h := newFakeHub(t, f)

	parents := h.fetchThreadParents(context.Background(), []goslack.SearchMessage{
		tmHit("C1", tmReplyTS, tmHost+"/archives/C1/pX?thread_ts="+tmRootTS),
	})
	if len(parents) != 0 {
		t.Fatalf("an empty thread must not register a zero-value parent, got %v", parents)
	}
}

// -------------------------------------------------------------- post_message

func TestPostMessage_PostsAndReportsTimestamp(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	var gotText, gotThread string
	f.OnFunc("chat.postMessage", func(r *http.Request) string {
		gotText, gotThread = r.Form.Get("text"), r.Form.Get("thread_ts")
		return `{"ok":true,"channel":"C1","ts":"1700000500.000600"}`
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "post_message", map[string]any{
		"channel": "alpha", "text": "status update", "thread_ts": tmRootTS,
	}))

	tmWant(t, out, "posted to #alpha", "1700000500.000600")
	if gotText != "status update" || gotThread != tmRootTS {
		t.Fatalf("payload drifted: text=%q thread_ts=%q", gotText, gotThread)
	}
}

func TestPostMessage_RequiredArgsAreChecked(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	tmWant(t, resultText(tmCall(t, s, "post_message", map[string]any{"text": "hi"})), "channel is required")
	tmWant(t, resultText(tmCall(t, s, "post_message", map[string]any{"channel": "alpha"})), "text is required")
	if len(f.Calls()) != 0 {
		t.Fatalf("argument checks must precede every Slack call, got %v", f.Calls())
	}
}

func TestPostMessage_PostFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.OnError("chat.postMessage", "not_in_channel")
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "post_message", map[string]any{"channel": "alpha", "text": "hi"})
	if !res.IsError {
		t.Fatalf("a failed post must not read as success: %q", resultText(res))
	}
	tmWant(t, resultText(res), "chat.postMessage", "not_in_channel")
}

func TestPostMessage_SkipIfRecentSuppressesTheDuplicate(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmAuthTest(f, "U1", tmHost+"/")
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"status update","ts":"`+uaTS(-300, 600)+`"}`,
	))
	f.On("chat.postMessage", `{"ok":true,"channel":"C1","ts":"1700000600.000700"}`)
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "post_message", map[string]any{
		"channel": "alpha", "text": "  status update  ", "skip_if_recent": 30,
	}))

	tmWant(t, out, "skipped #alpha", "within 30m", "skip_if_recent")
	if f.Called("chat.postMessage") {
		t.Fatalf("the guard must suppress the post, calls=%v", f.Calls())
	}
}

func TestPostMessage_SkipIfRecentPostsWhenTextDiffers(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmAuthTest(f, "U1", tmHost+"/")
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"a different line","ts":"1700000500.000600"}`,
		`{"type":"message","user":"U2","text":"status update","ts":"1700000400.000500"}`,
	))
	f.On("chat.postMessage", `{"ok":true,"channel":"C1","ts":"1700000600.000700"}`)
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "post_message", map[string]any{
		"channel": "alpha", "text": "status update", "skip_if_recent": 30,
	}))

	// Same text but a DIFFERENT author must not count as a self-duplicate.
	tmWant(t, out, "posted to #alpha", "1700000600.000700")
}

func TestPostMessage_SkipIfRecentFailsOpenWithoutUserToken(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("chat.postMessage", `{"ok":true,"channel":"C1","ts":"1700000600.000700"}`)
	h := newFakeHub(t, f, func(c *config.Config) { c.UserToken = "" })
	s := tmThreadServer(t, h)

	out := resultText(tmCall(t, s, "post_message", map[string]any{
		"channel": "alpha", "text": "status update", "skip_if_recent": 30,
	}))

	tmWant(t, out, "posted to #alpha")
	if f.Called("auth.test") || f.Called("conversations.history") {
		t.Fatalf("no user token means no dedup lookup at all, calls=%v", f.Calls())
	}
}

func TestRecentSelfDuplicate_FailsOpenOnLookupErrors(t *testing.T) {
	t.Run("auth.test error", func(t *testing.T) {
		f := newFakeSlack(t).OnError("auth.test", "invalid_auth")
		if newFakeHub(t, f).recentSelfDuplicate(context.Background(), "C1", "x", 30) {
			t.Fatal("an unidentifiable self must not suppress the post")
		}
	})
	t.Run("history error", func(t *testing.T) {
		f := newFakeSlack(t)
		tmAuthTest(f, "U1", tmHost+"/")
		f.OnError("conversations.history", "channel_not_found")
		if newFakeHub(t, f).recentSelfDuplicate(context.Background(), "C1", "x", 30) {
			t.Fatal("an unreadable history must not suppress the post")
		}
	})
	t.Run("no match", func(t *testing.T) {
		f := newFakeSlack(t)
		tmAuthTest(f, "U1", tmHost+"/")
		f.On("conversations.history", tmHistoryBody(
			`{"type":"message","user":"U1","text":"something else","ts":"`+tmRootTS+`"}`,
		))
		if newFakeHub(t, f).recentSelfDuplicate(context.Background(), "C1", "x", 30) {
			t.Fatal("a different text is not a duplicate")
		}
	})
}

// ADR 111: the guard wants the operator's MOST RECENT posts, so it reads
// the page adjacent to now and trims to the window locally. A page
// anchored at `oldest` holds the window's oldest messages instead.
func TestRecentSelfDuplicate_FetchesTheNewestPage(t *testing.T) {
	f := newFakeSlack(t)
	tmAuthTest(f, "U1", tmHost+"/")
	var oldest, limit string
	f.OnFunc("conversations.history", func(r *http.Request) string {
		oldest, limit = r.Form.Get("oldest"), r.Form.Get("limit")
		return tmHistoryBody()
	})

	newFakeHub(t, f).recentSelfDuplicate(context.Background(), "C1", "x", 30)

	if oldest != "" {
		t.Fatalf("the guard must not anchor its page at a lower bound, got oldest=%q", oldest)
	}
	if limit != "100" {
		t.Fatalf("limit should be 100, got %q", limit)
	}
}

// ------------------------------------------------------------- add_reaction

func TestAddReaction_ConfirmsTheEmojiAndChannel(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	var gotName, gotChannel string
	f.OnFunc("reactions.add", func(r *http.Request) string {
		gotName, gotChannel = r.Form.Get("name"), r.Form.Get("channel")
		return `{"ok":true}`
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "add_reaction", map[string]any{
		"channel": "alpha", "timestamp": tmRootTS, "emoji": "thumbsup",
	}))

	tmWant(t, out, "added :thumbsup: on #alpha")
	if gotName != "thumbsup" || gotChannel != "C1" {
		t.Fatalf("reaction payload drifted: name=%q channel=%q", gotName, gotChannel)
	}
}

func TestAddReaction_FailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.OnError("reactions.add", "already_reacted")
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "add_reaction", map[string]any{
		"channel": "alpha", "timestamp": tmRootTS, "emoji": "thumbsup",
	})
	if !res.IsError {
		t.Fatalf("a failed reaction must not read as success: %q", resultText(res))
	}
	tmWant(t, resultText(res), "already_reacted")
}

func TestAddReaction_UnknownChannelIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"beta": "C2"})
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "add_reaction", map[string]any{
		"channel": "alpha", "timestamp": tmRootTS, "emoji": "thumbsup",
	})
	if !res.IsError {
		t.Fatalf("unknown channel should error, got %q", resultText(res))
	}
	if f.Called("reactions.add") {
		t.Fatalf("no reaction should be attempted, calls=%v", f.Calls())
	}
}

// ----------------------------------------------------------- delete_message

func TestDeleteMessage_DeletesByChannelAndTimestamp(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	var gotChannel, gotTS string
	f.OnFunc("chat.delete", func(r *http.Request) string {
		gotChannel, gotTS = r.Form.Get("channel"), r.Form.Get("ts")
		return `{"ok":true,"channel":"C1","ts":"` + tmRootTS + `"}`
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "delete_message", map[string]any{
		"channel": "#alpha", "timestamp": tmRootTS,
	}))

	tmWant(t, out, "deleted message "+tmRootTS+" in #alpha")
	if gotChannel != "C1" || gotTS != tmRootTS {
		t.Fatalf("delete payload drifted: channel=%q ts=%q", gotChannel, gotTS)
	}
}

func TestDeleteMessage_PermalinkSkipsNameResolution(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("chat.delete", `{"ok":true,"channel":"C0ALPHA001","ts":"`+tmRootTS+`"}`)
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "delete_message", map[string]any{
		"permalink": tmHost + "/archives/C0ALPHA001/p1700000000000100",
	}))

	tmWant(t, out, "deleted message "+tmRootTS+" in C0ALPHA001")
	if f.Called("conversations.list") {
		t.Fatalf("a permalink already carries the id; no listing expected, calls=%v", f.Calls())
	}
}

func TestDeleteMessage_SlackErrorsGetAnActionableHint(t *testing.T) {
	for code, want := range map[string]string{
		"cant_delete_message": "only delete messages that identity posted",
		"message_not_found":   "already deleted",
	} {
		f := newFakeSlack(t)
		tmChannelList(f, map[string]string{"alpha": "C1"})
		f.OnError("chat.delete", code)
		s := tmThreadServer(t, newFakeHub(t, f))

		res := tmCall(t, s, "delete_message", map[string]any{
			"channel": "alpha", "timestamp": tmRootTS,
		})
		if !res.IsError {
			t.Fatalf("%s should error, got %q", code, resultText(res))
		}
		tmWant(t, resultText(res), code, want)
	}
}

func TestDeleteMessage_MissingTargetIsRejectedBeforeAnyCall(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "delete_message", map[string]any{"channel": "alpha"})
	if !res.IsError {
		t.Fatalf("channel without a timestamp should error, got %q", resultText(res))
	}
	tmWant(t, resultText(res), "provide a permalink, or channel + timestamp")
	if len(f.Calls()) != 0 {
		t.Fatalf("validation must precede every Slack call, got %v", f.Calls())
	}
}

// --------------------------------------------------------------- registration

func TestRegisterThreadTools_ReadOnlyHidesEveryWrite(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f, func(c *config.Config) { c.ReadOnly = true })
	names := tmToolNames(t, tmThreadServer(t, h))

	for _, read := range []string{"get_thread", "get_user_messages"} {
		if !tmHas(names, read) {
			t.Fatalf("read-only must keep %s, got %v", read, names)
		}
	}
	for _, write := range []string{"post_message", "add_reaction", "delete_message"} {
		if tmHas(names, write) {
			t.Fatalf("read-only must drop %s, got %v", write, names)
		}
	}
}

func TestRegisterThreadTools_RegistersEveryToolByDefault(t *testing.T) {
	f := newFakeSlack(t)
	names := tmToolNames(t, tmThreadServer(t, newFakeHub(t, f)))

	for _, want := range []string{"get_thread", "get_user_messages", "post_message", "add_reaction", "delete_message"} {
		if !tmHas(names, want) {
			t.Fatalf("%s should be registered, got %v", want, names)
		}
	}
}

func TestRegisterThreadTools_HonoursTheDisabledList(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{
			"get_thread": {}, "get_user_messages": {}, "post_message": {},
			"add_reaction": {}, "delete_message": {},
		}
	})
	if names := tmToolNames(t, tmThreadServer(t, h)); len(names) != 0 {
		t.Fatalf("every tool was disabled, yet %v registered", names)
	}
}

// -------------------------------------------------- remaining error branches

func TestAddReaction_UnknownWorkspaceIsRejected(t *testing.T) {
	f := newFakeSlack(t)
	s := tmThreadServer(t, newFakeHub(t, f))

	res := tmCall(t, s, "add_reaction", map[string]any{
		"channel": "alpha", "timestamp": tmRootTS, "emoji": "thumbsup", "workspace": "ghost",
	})
	if !res.IsError {
		t.Fatalf("unknown workspace should error, got %q", resultText(res))
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("routing must fail before any Slack call, got %v", f.Calls())
	}
}

func TestRunDeleteMessage_ChannelResolutionFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"beta": "C2"})
	h := newFakeHub(t, f)

	res := h.runDeleteMessage(context.Background(), "", "alpha", tmRootTS, "")
	if res == nil || !res.IsError {
		t.Fatalf("an unresolvable channel must error, got %+v", res)
	}
	tmWant(t, resultText(res), "#alpha not found")
	if f.Called("chat.delete") {
		t.Fatalf("nothing should be deleted, calls=%v", f.Calls())
	}
}

func TestRunPostMessage_ChannelResolutionFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"beta": "C2"})
	h := newFakeHub(t, f)

	res := h.runPostMessage(context.Background(), "", "alpha", "hi", "", 0)
	if res == nil || !res.IsError {
		t.Fatalf("an unresolvable channel must error, got %+v", res)
	}
	tmWant(t, resultText(res), "#alpha not found")
	if f.Called("chat.postMessage") {
		t.Fatalf("nothing should be posted, calls=%v", f.Calls())
	}
}

func TestGetThread_AutoDetectedWorkspaceIsAnnounced(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	tmAuthTest(a, "U1", tmHost+"/")
	tmAuthTest(b, "U2", "https://beta.example.com/")
	tmUsers(b, map[string]string{"U2": "sam"})
	b.On("conversations.replies", tmHistoryBody(
		`{"type":"message","user":"U2","text":"root question","ts":"`+tmRootTS+`"}`,
	))
	s := tmThreadServer(t, tmTwoFakeHub(t, a, b))

	out := resultText(tmCall(t, s, "get_thread", map[string]any{
		"permalink": "https://beta.example.com/archives/C0BETA0001/p1700000000000100",
	}))

	tmWant(t, out, "thread C0BETA0001 (1 msgs)",
		"(workspace auto-detected from permalink)", "root question")
	if a.Called("conversations.replies") {
		t.Fatalf("the thread must be read from the matching workspace only, calls=%v", a.Calls())
	}
}

func TestPostMessage_MultiWorkspaceConfirmationNamesTheTarget(t *testing.T) {
	a, b := newFakeSlack(t), newFakeSlack(t)
	tmChannelList(b, map[string]string{"alpha": "C1"})
	b.On("chat.postMessage", `{"ok":true,"channel":"C1","ts":"1700000500.000600"}`)
	s := tmThreadServer(t, tmTwoFakeHub(t, a, b))

	out := resultText(tmCall(t, s, "post_message", map[string]any{
		"channel": "alpha", "text": "status update", "workspace": "beta",
	}))

	tmWant(t, out, "posted to #alpha [beta]", "1700000500.000600")
	if len(a.Calls()) != 0 {
		t.Fatalf("the primary must not be touched, calls=%v", a.Calls())
	}
}
