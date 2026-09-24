package tools

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// deAlphaList is the one-channel workspace listing the digest tests
// resolve `alpha` against.
func deAlphaList() string {
	return deConversations(deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", ""))
}

// deParentJSON builds a thread-parent history entry: reply_count plus
// the latest_reply Slack puts on the parent, which is what lets the
// digest decide whether a thread moved without paying for a
// conversations.replies call.
func deParentJSON(user, ts, text string, replyCount int, latestReply string) string {
	return fmt.Sprintf(
		`{"type":"message","user":%q,"ts":%q,"thread_ts":%q,"text":%q,"reply_count":%d,"latest_reply":%q}`,
		user, ts, ts, text, replyCount, latestReply)
}

// deReplyJSON builds a conversations.replies entry hanging off parentTS.
func deReplyJSON(user, ts, parentTS, text string) string {
	return fmt.Sprintf(`{"type":"message","user":%q,"ts":%q,"thread_ts":%q,"text":%q}`,
		user, ts, parentTS, text)
}

// deAnyUser answers users.info for every id with a stable handle.
func deAnyUser(f *deFake) {
	f.onFunc("users.info", func(r *http.Request) string {
		id := r.Form.Get("user")
		return deUserInfo(id, "alex", "Alex")
	})
}

func TestChannelDigest_RendersOnlyTheWindow_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)

	var oldest string
	f.onFunc("conversations.history", func(r *http.Request) string {
		oldest = r.Form.Get("oldest")
		return deHistory(
			deMsgJSON("U0AAAAAAAAA", deTS(2*time.Hour), "inside the window"),
			deMsgJSON("U0AAAAAAAAA", deTS(48*time.Hour), "older than the window"),
		)
	})
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "hours": 24,
	}))

	if !strings.HasPrefix(out, "## #alpha (1 msgs)") {
		t.Fatalf("only the in-window message should be counted:\n%s", out)
	}
	if !strings.Contains(out, "inside the window") {
		t.Fatalf("the in-window message must be rendered:\n%s", out)
	}
	if strings.Contains(out, "older than the window") {
		t.Fatalf("a message fetched only for thread discovery must not be rendered:\n%s", out)
	}

	// ADR 111: the window page is anchored at its upper edge. A lower
	// bound makes Slack return the page adjacent to it — the oldest end —
	// and a busy conversation loses its newest messages entirely.
	if oldest != "" {
		t.Fatalf("the window fetch must not send a lower bound, got oldest=%q", oldest)
	}
}

// ADR 106: a reply is activity. A thread whose parent predates the
// window but whose newest reply lands inside it must not render as a
// dead channel — the counter is free, it comes off the page already
// fetched.
func TestChannelDigest_ThreadThatMovedInWindowIsReported_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deParentJSON("U0AAAAAAAAA", deTS(72*time.Hour), "the original report", 3, deTS(time.Hour)),
	))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "hours": 24,
	}))

	want := "## #alpha\n(no top-level messages in this window; 1 thread(s) received replies here" +
		" — pass with_replies=true to read them)"
	if out != want {
		t.Fatalf("an active channel must not render as quiet.\n got: %q\nwant: %q", out, want)
	}
	// And the counter must stay free: no per-thread replies call.
	if f.called("conversations.replies") {
		t.Fatalf("counting moved threads must not cost a replies call: %v", f.callList())
	}
}

func TestChannelDigest_StaleThreadIsNotActivity_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	// Parent and its newest reply both predate the window.
	f.on("conversations.history", deHistory(
		deParentJSON("U0AAAAAAAAA", deTS(96*time.Hour), "old report", 3, deTS(80*time.Hour)),
	))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "hours": 24,
	}))
	if out != "## #alpha\n(no activity)" {
		t.Fatalf("a genuinely quiet window must say so plainly, got %q", out)
	}
}

func TestChannelDigest_EmptyPageIsNoActivity_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	f.on("conversations.history", deHistory())
	hub := deHub(t, f)

	res := deCall(t, hub, "get_channel_digest", map[string]any{"channel": "alpha"})
	if res.IsError {
		t.Fatalf("an empty channel is not a failure: %q", resultText(res))
	}
	// A blank body would be indistinguishable from a broken call.
	if got := resultText(res); got != "## #alpha\n(no activity)" {
		t.Fatalf("empty digest must name itself, got %q", got)
	}
}

func TestChannelDigest_WithRepliesInlinesOnlyInWindowReplies_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	parentTS := deTS(72 * time.Hour)
	f.on("conversations.history", deHistory(
		deParentJSON("U0AAAAAAAAA", parentTS, "the original report", 2, deTS(time.Hour)),
	))
	f.on("conversations.replies", deHistory(
		// conversations.replies echoes the parent back first.
		deParentJSON("U0AAAAAAAAA", parentTS, "the original report", 2, deTS(time.Hour)),
		deReplyJSON("U0BBBBBBBBB", deTS(70*time.Hour), parentTS, "reply from before the window"),
		deReplyJSON("U0BBBBBBBBB", deTS(time.Hour), parentTS, "reply inside the window"),
	))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "hours": 24, "with_replies": true,
	}))

	if !strings.Contains(out, "reply inside the window") {
		t.Fatalf("the in-window reply is the substance and must be shown:\n%s", out)
	}
	if strings.Contains(out, "reply from before the window") {
		t.Fatalf("a reply from outside the window must be filtered out:\n%s", out)
	}
	// The parent echo conversations.replies prepends must not be re-rendered.
	if strings.Count(out, "the original report") > 1 {
		t.Fatalf("parent echo should be stripped from the reply list:\n%s", out)
	}
	// Nothing anchors these replies in the window, so they are rendered
	// on their own rather than dropped with their stale parent.
	if !strings.Contains(out, "## #alpha (1 reply in earlier threads)") {
		t.Fatalf("orphaned replies need their own heading:\n%s", out)
	}
}

func TestChannelDigest_ThreadPreviewCapLimitsReplies_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	parentTS := deTS(2 * time.Hour)
	replies := []string{deParentJSON("U0AAAAAAAAA", parentTS, "root", 3, deTS(time.Hour))}
	for i := 0; i < 3; i++ {
		replies = append(replies, deReplyJSON("U0BBBBBBBBB",
			deTS(time.Duration(60-i)*time.Minute), parentTS, fmt.Sprintf("reply %d", i)))
	}
	f.on("conversations.history", deHistory(replies[0]))
	f.on("conversations.replies", deHistory(replies...))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "with_replies": true, "thread_preview_replies": 1,
	}))
	if !strings.Contains(out, "reply 0") {
		t.Fatalf("the first reply should survive the cap:\n%s", out)
	}
	if !strings.Contains(out, "+2 more replies") {
		t.Fatalf("a capped thread must say how many replies it hid:\n%s", out)
	}
}

func TestChannelDigest_DMDefaultsToRepliesChannelDoesNot_Behaviour(t *testing.T) {
	// Same fixture twice: the ONLY difference is the reference shape, and
	// that alone decides whether threads are expanded (ADR 064).
	build := func(t *testing.T) (*deFake, *Hub) {
		t.Helper()
		f := deNewFake(t)
		f.on("conversations.list", deAlphaList())
		f.on("users.list", `{"ok":true,"members":[{"id":"U0BBBBBBBBB","name":"sam","real_name":"Sam"}],"response_metadata":{"next_cursor":""}}`)
		f.on("conversations.open", `{"ok":true,"channel":{"id":"D0AAAAAAAAA"}}`)
		deAnyUser(f)
		ts := deTS(2 * time.Hour)
		f.on("conversations.history", deHistory(
			deParentJSON("U0BBBBBBBBB", ts, "root message", 1, deTS(time.Hour))))
		f.on("conversations.replies", deHistory(
			deParentJSON("U0BBBBBBBBB", ts, "root message", 1, deTS(time.Hour)),
			deReplyJSON("U0AAAAAAAAA", deTS(time.Hour), ts, "the answer"),
		))
		return f, deHub(t, f)
	}

	fDM, hubDM := build(t)
	out := resultText(deCall(t, hubDM, "get_channel_digest", map[string]any{"channel": "@sam"}))
	if !fDM.called("conversations.replies") {
		t.Fatalf("a DM must expand threads by default, calls: %v", fDM.callList())
	}
	if !strings.Contains(out, "the answer") {
		t.Fatalf("the reply IS the DM conversation:\n%s", out)
	}
	if !strings.HasPrefix(out, "## @sam") {
		t.Fatalf("a DM heading keeps its @, got %q", strings.SplitN(out, "\n", 2)[0])
	}

	fCh, hubCh := build(t)
	out = resultText(deCall(t, hubCh, "get_channel_digest", map[string]any{"channel": "alpha"}))
	if fCh.called("conversations.replies") {
		t.Fatalf("a channel must not fan out one replies call per thread by default: %v", fCh.callList())
	}
	if !strings.HasPrefix(out, "## #alpha") {
		t.Fatalf("a channel heading gets exactly one #, got %q", strings.SplitN(out, "\n", 2)[0])
	}
}

func TestChannelDigest_ExplicitWithRepliesFalseBeatsDMDefault_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("users.list", `{"ok":true,"members":[{"id":"U0BBBBBBBBB","name":"sam","real_name":"Sam"}],"response_metadata":{"next_cursor":""}}`)
	f.on("conversations.open", `{"ok":true,"channel":{"id":"D0AAAAAAAAA"}}`)
	deAnyUser(f)
	ts := deTS(2 * time.Hour)
	f.on("conversations.history", deHistory(
		deParentJSON("U0BBBBBBBBB", ts, "root message", 1, deTS(time.Hour))))
	hub := deHub(t, f)

	deCall(t, hub, "get_channel_digest", map[string]any{"channel": "@sam", "with_replies": false})
	if f.called("conversations.replies") {
		t.Fatalf("an explicit false must win over the per-kind default: %v", f.callList())
	}
}

func TestChannelDigest_BareUserIDOpensThatDM_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.onFunc("conversations.open", func(r *http.Request) string {
		if got := r.Form.Get("users"); got != "U0BBBBBBBBB" {
			t.Errorf("conversations.open should target the pasted id, got %q", got)
		}
		return `{"ok":true,"channel":{"id":"D0AAAAAAAAA"}}`
	})
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0BBBBBBBBB", deTS(time.Hour), "hello there")))
	hub := deHub(t, f)

	// The unread summary prints DM headers as a bare user id, so pasting
	// one back in has to Just Work — without a roster lookup.
	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{"channel": "U0BBBBBBBBB"}))
	if !strings.Contains(out, "hello there") {
		t.Fatalf("a bare user id should open that DM:\n%s", out)
	}
	if f.called("users.list") {
		t.Fatalf("a canonical user id needs no roster scan: %v", f.callList())
	}
}

func TestChannelDigest_WithTSMakesLinesCitable_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	ts := deTS(time.Hour)
	f.on("conversations.history", deHistory(deMsgJSON("U0AAAAAAAAA", ts, "citable")))
	hub := deHub(t, f)

	plain := resultText(deCall(t, hub, "get_channel_digest", map[string]any{"channel": "alpha"}))
	if strings.Contains(plain, "ts=") {
		t.Fatalf("timestamps are opt-in:\n%s", plain)
	}
	withTS := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "with_ts": true,
	}))
	if !strings.Contains(withTS, "ts="+ts) {
		t.Fatalf("with_ts must print the key get_message takes:\n%s", withTS)
	}
}

func TestChannelDigest_FullTextStopsTruncation_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	long := strings.Repeat("a", 400)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), long)))
	hub := deHub(t, f)

	short := resultText(deCall(t, hub, "get_channel_digest", map[string]any{"channel": "alpha"}))
	if !strings.Contains(short, "(+120 chars)") {
		t.Fatalf("a truncated body must declare how much it dropped:\n%s", short)
	}
	full := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "full_text": true,
	}))
	if !strings.Contains(full, long) || strings.Contains(full, "chars)") {
		t.Fatalf("full_text must render the whole body:\n%s", full)
	}
}

func TestChannelDigest_MaxMessagesCapsAndCounts_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	var msgs []string
	for i := 0; i < 4; i++ {
		msgs = append(msgs, deMsgJSON("U0AAAAAAAAA",
			deTS(time.Duration(i+1)*time.Minute), fmt.Sprintf("msg %d", i)))
	}
	f.on("conversations.history", deHistory(msgs...))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "max_messages": 2,
	}))
	// The header counts everything in the window; the body shows the cap
	// — the difference is what tells a reader the view is partial.
	if !strings.HasPrefix(out, "## #alpha (4 msgs)") {
		t.Fatalf("header should count the whole window:\n%s", out)
	}
	if strings.Contains(out, "msg 2") || strings.Contains(out, "msg 3") {
		t.Fatalf("max_messages was not applied:\n%s", out)
	}
}

func TestChannelDigest_AbsoluteRangeIsDayInclusive_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	// Record the FIRST history call: that is the window page. A second call
	// (thread discovery, when the window is empty) is anchored at the
	// window's lower edge and would overwrite the capture.
	var oldest, latest string
	var seen bool
	f.onFunc("conversations.history", func(r *http.Request) string {
		if !seen {
			oldest, latest, seen = r.Form.Get("oldest"), r.Form.Get("latest"), true
		}
		return deHistory()
	})
	hub := deHub(t, f)

	deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "after": "2026-04-30", "before": "2026-04-30",
	})

	day, _ := time.Parse("2006-01-02", "2026-04-30")
	wantLatest := float64(day.Add(24 * time.Hour).Unix())
	// The window is trimmed to `after` locally; the page itself is
	// anchored at the upper edge only (ADR 111).
	if oldest != "" {
		t.Fatalf("the window page must not send a lower bound, got oldest=%q", oldest)
	}
	// Naming one day as both bounds must mean that whole day.
	if got, _ := strconv.ParseFloat(latest, 64); got != wantLatest {
		t.Fatalf("latest = %v, want %v (the named day included)", got, wantLatest)
	}
}

func TestChannelDigest_BadDateIsRejectedBeforeAnyCall_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deHub(t, f)

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"channel": "alpha", "after": "30-04-2026"}, "after must be YYYY-MM-DD"},
		{map[string]any{"channel": "alpha", "before": "nope"}, "before must be YYYY-MM-DD"},
		{map[string]any{"channel": "alpha", "after": "2026-05-05", "before": "2026-05-01"}, "before must be after after"},
	} {
		res := deCall(t, hub, "get_channel_digest", c.args)
		if !res.IsError || !strings.Contains(resultText(res), c.want) {
			t.Fatalf("args %v: got %q, want %q", c.args, resultText(res), c.want)
		}
	}
	if len(f.callList()) != 0 {
		t.Fatalf("a malformed range must cost nothing, got %v", f.callList())
	}
}

func TestChannelDigest_ErrorsSurface_Behaviour(t *testing.T) {
	// Channel cannot be resolved.
	f := deNewFake(t)
	f.on("conversations.list", deConversations())
	res := deCall(t, deHub(t, f), "get_channel_digest", map[string]any{"channel": "ghost"})
	if !res.IsError || !strings.Contains(resultText(res), "#ghost not found") {
		t.Fatalf("unresolvable channel: got %q", resultText(res))
	}

	// History itself fails.
	f2 := deNewFake(t)
	f2.on("conversations.list", deAlphaList())
	f2.onError("conversations.history", "not_in_channel")
	res = deCall(t, deHub(t, f2), "get_channel_digest", map[string]any{"channel": "alpha"})
	if !res.IsError || !strings.Contains(resultText(res), "not_in_channel") {
		t.Fatalf("history failure must not render as a quiet channel: %q", resultText(res))
	}

	// Missing argument.
	res = deCall(t, deHub(t, deNewFake(t)), "get_channel_digest", map[string]any{})
	if !res.IsError || resultText(res) != "channel is required" {
		t.Fatalf("missing channel: got %q", resultText(res))
	}

	// Unknown workspace, reported before any call.
	f3 := deNewFake(t)
	hub := deMultiHub(t, f3, []string{"primary", "secondary"})
	res = deCall(t, hub, "get_channel_digest", map[string]any{"channel": "alpha", "workspace": "ghost"})
	if !res.IsError || !strings.Contains(resultText(res), "ghost") {
		t.Fatalf("unknown workspace: got %q", resultText(res))
	}
	if len(f3.callList()) != 0 {
		t.Fatalf("routing must fail before any API call, got %v", f3.callList())
	}
}

// ----------------------------- multi-channel digest -----------------------------

func TestMultiChannelDigest_OneSectionPerChannel_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", ""),
		deChannelJSON("C0BBBBBBBBB", "beta", 3, true, false, "", ""),
	))
	deAnyUser(f)
	f.onFunc("conversations.history", func(r *http.Request) string {
		return deHistory(deMsgJSON("U0AAAAAAAAA", deTS(time.Hour),
			"note in "+r.Form.Get("channel")))
	})
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_multi_channel_digest", map[string]any{
		"channels": "alpha, beta",
	}))
	if !strings.Contains(out, "## #alpha (1 msgs)") || !strings.Contains(out, "## #beta (1 msgs)") {
		t.Fatalf("both channels should render:\n%s", out)
	}
	if !strings.Contains(out, "note in C0AAAAAAAAA") || !strings.Contains(out, "note in C0BBBBBBBBB") {
		t.Fatalf("each section must carry its own channel's messages:\n%s", out)
	}
}

func TestMultiChannelDigest_OneBadChannelStaysInline_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "still here")))
	hub := deHub(t, f)

	res := deCall(t, hub, "get_multi_channel_digest", map[string]any{"channels": "alpha,ghost"})
	if res.IsError {
		t.Fatalf("one unresolvable channel must not sink the whole digest: %q", resultText(res))
	}
	out := resultText(res)
	if !strings.Contains(out, "still here") {
		t.Fatalf("the healthy channel's content must survive:\n%s", out)
	}
	if !strings.Contains(out, "## #ghost\nerror: ") {
		t.Fatalf("the failure belongs in its own section:\n%s", out)
	}
}

func TestMultiChannelDigest_FallsBackToConfiguredChannels_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "configured")))
	hub := deHub(t, f, func(c *config.Config) { c.Channels = []string{"alpha"} })

	out := resultText(deCall(t, hub, "get_multi_channel_digest", map[string]any{}))
	if !strings.Contains(out, "## #alpha") || !strings.Contains(out, "configured") {
		t.Fatalf("with no argument the configured channel set is used:\n%s", out)
	}
	// No auto-discovery when config already answers the question.
	if f.called("users.conversations") {
		t.Fatalf("configured channels must short-circuit discovery: %v", f.callList())
	}
}

func TestMultiChannelDigest_AutoDiscoversJoinedChannels_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("users.conversations", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", "")))
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "discovered")))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_multi_channel_digest", map[string]any{}))
	if !strings.Contains(out, "discovered") {
		t.Fatalf("joined channels should be discovered when nothing is configured:\n%s", out)
	}
}

func TestMultiChannelDigest_NoChannelsGivesGuidance_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("users.conversations", deConversations())
	hub := deHub(t, f)

	res := deCall(t, hub, "get_multi_channel_digest", map[string]any{})
	if !res.IsError || !strings.Contains(resultText(res), "no channels available") {
		t.Fatalf("an empty target set should say what to do, got %q", resultText(res))
	}
}

func TestMultiChannelDigest_DiscoveryFailureIsNamed_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.onError("users.conversations", "missing_scope")
	hub := deHub(t, f)

	res := deCall(t, hub, "get_multi_channel_digest", map[string]any{})
	if !res.IsError || !strings.Contains(resultText(res), "auto-discover channels:") ||
		!strings.Contains(resultText(res), "missing_scope") {
		t.Fatalf("discovery failure should name itself, got %q", resultText(res))
	}
}

func TestMultiChannelDigest_MultiWorkspaceSectionsAndErrors_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	// Only the first workspace's history answers; the second fails, and
	// must not take the first one's section with it.
	n := 0
	f.onFunc("conversations.history", func(*http.Request) string {
		n++
		if n == 1 {
			return deHistory(deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "primary note"))
		}
		return `{"ok":false,"error":"not_in_channel"}`
	})
	hub := deMultiHub(t, f, []string{"primary", "secondary"},
		func(c *config.Config) { c.Channels = []string{"alpha"} })

	res := deCall(t, hub, "get_multi_channel_digest", map[string]any{})
	if res.IsError {
		t.Fatalf("a per-workspace failure must not fail the sweep: %q", resultText(res))
	}
	out := resultText(res)
	if !strings.Contains(out, "## [primary]") || !strings.Contains(out, "primary note") {
		t.Fatalf("the healthy workspace must render:\n%s", out)
	}
	// A per-channel failure inside a workspace still renders that
	// workspace's section rather than erroring it out.
	if !strings.Contains(out, "## [secondary]") || !strings.Contains(out, "not_in_channel") {
		t.Fatalf("the failing workspace must report why:\n%s", out)
	}
}

// ----------------------------- morning recap -----------------------------

func TestMorningRecap_DecisionsThenActivity_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "we decided to go with option two"),
		deMsgJSON("U0AAAAAAAAA", deTS(2*time.Hour), "just chatter"),
	))
	hub := deHub(t, f, func(c *config.Config) {
		c.Channels = []string{"alpha"}
		c.DecisionKeywords = []string{"decided"}
	})

	out := resultText(deCall(t, hub, "get_morning_recap", map[string]any{"hours": 24}))

	if !strings.HasPrefix(out, "# Morning Recap (last 24h)\n\n") {
		t.Fatalf("recap title drifted:\n%s", out)
	}
	di, ai := strings.Index(out, "## Decisions"), strings.Index(out, "## Activity")
	if di < 0 || ai < 0 || di > ai {
		t.Fatalf("decisions must come before activity:\n%s", out)
	}
	if !strings.Contains(out, "[keyword:decided]") ||
		!strings.Contains(out, "we decided to go with option two") {
		t.Fatalf("the matched decision must be quoted with its reason:\n%s", out)
	}
	if !strings.Contains(out, "## #alpha (2 msgs)") || !strings.Contains(out, "just chatter") {
		t.Fatalf("activity must still hold every message:\n%s", out)
	}
}

func TestMorningRecap_NoDecisionsOmitsTheSection_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "just chatter")))
	hub := deHub(t, f, func(c *config.Config) {
		c.Channels = []string{"alpha"}
		c.DecisionKeywords = []string{"decided"}
	})

	out := resultText(deCall(t, hub, "get_morning_recap", map[string]any{}))
	if strings.Contains(out, "## Decisions") {
		t.Fatalf("an empty decisions section is noise:\n%s", out)
	}
	if !strings.Contains(out, "## Activity") {
		t.Fatalf("activity is always rendered:\n%s", out)
	}
}

func TestMorningRecap_PerChannelFailuresStayInline_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.onError("conversations.history", "not_in_channel")
	hub := deHub(t, f, func(c *config.Config) { c.Channels = []string{"alpha", "ghost"} })

	res := deCall(t, hub, "get_morning_recap", map[string]any{})
	if res.IsError {
		t.Fatalf("per-channel failures must not fail the recap: %q", resultText(res))
	}
	out := resultText(res)
	// One channel fails to resolve, the other fails to read — both are
	// reported, neither is silently dropped.
	if !strings.Contains(out, "## #alpha\nerror: ") || !strings.Contains(out, "not_in_channel") {
		t.Fatalf("a history failure must be visible:\n%s", out)
	}
	if !strings.Contains(out, "## #ghost\nerror: ") {
		t.Fatalf("an unresolvable channel must be visible:\n%s", out)
	}
}

func TestMorningRecap_NoChannelsGivesGuidance_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("users.conversations", deConversations())
	hub := deHub(t, f)

	res := deCall(t, hub, "get_morning_recap", map[string]any{})
	if !res.IsError || !strings.Contains(resultText(res), "no channels available") {
		t.Fatalf("got %q", resultText(res))
	}

	f2 := deNewFake(t)
	f2.onError("users.conversations", "missing_scope")
	res = deCall(t, deHub(t, f2), "get_morning_recap", map[string]any{})
	if !res.IsError || !strings.Contains(resultText(res), "auto-discover channels:") {
		t.Fatalf("got %q", resultText(res))
	}
}

func TestMorningRecap_MultiWorkspaceTitleOnce_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	n := 0
	f.onFunc("conversations.history", func(*http.Request) string {
		n++
		if n == 1 {
			return deHistory(deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "primary note"))
		}
		return `{"ok":false,"error":"not_in_channel"}`
	})
	hub := deMultiHub(t, f, []string{"primary", "secondary"},
		func(c *config.Config) { c.Channels = []string{"alpha"} })

	out := resultText(deCall(t, hub, "get_morning_recap", map[string]any{"hours": 12}))
	if strings.Count(out, "# Morning Recap") != 1 {
		t.Fatalf("the title belongs once, above the sections:\n%s", out)
	}
	if !strings.HasPrefix(out, "# Morning Recap (last 12h)\n\n## [primary]") {
		t.Fatalf("sections must follow the single title:\n%s", out)
	}
	if !strings.Contains(out, "## [secondary]") {
		t.Fatalf("every workspace gets a section:\n%s", out)
	}
}

func TestMorningRecap_UnknownWorkspace_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deMultiHub(t, f, []string{"primary", "secondary"})
	res := deCall(t, hub, "get_morning_recap", map[string]any{"workspace": "ghost"})
	if !res.IsError || !strings.Contains(resultText(res), "ghost") {
		t.Fatalf("got %q", resultText(res))
	}
}

// A thread whose replies all fall outside the window contributes no
// reply block — the parent renders with its counter and nothing is
// invented around it.
func TestChannelDigest_ThreadWithNoInWindowRepliesAddsNothing_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	parentTS := deTS(2 * time.Hour)
	f.on("conversations.history", deHistory(
		deParentJSON("U0AAAAAAAAA", parentTS, "root", 1, deTS(time.Hour))))
	f.on("conversations.replies", deHistory(
		deParentJSON("U0AAAAAAAAA", parentTS, "root", 1, deTS(time.Hour)),
		deReplyJSON("U0BBBBBBBBB", deTS(200*time.Hour), parentTS, "ancient answer"),
	))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_digest", map[string]any{
		"channel": "alpha", "hours": 24, "with_replies": true,
	}))
	if strings.Contains(out, "ancient answer") {
		t.Fatalf("an out-of-window reply must not be inlined:\n%s", out)
	}
	if !strings.Contains(out, "## #alpha (1 msgs)") || !strings.Contains(out, "(1 replies)") {
		t.Fatalf("the parent should still render with its counter:\n%s", out)
	}
}

// deSweepHub builds a two-workspace Hub where the FIRST workspace
// discovers a channel and the second's discovery fails, so the
// per-workspace body error has to be rendered next to a good section.
func deSweepHub(t *testing.T, f *deFake) *Hub {
	t.Helper()
	n := 0
	f.onFunc("users.conversations", func(*http.Request) string {
		n++
		if n == 1 {
			return deConversations(deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", ""))
		}
		return `{"ok":false,"error":"missing_scope"}`
	})
	f.on("conversations.list", deAlphaList())
	deAnyUser(f)
	f.on("conversations.history", deHistory(
		deMsgJSON("U0AAAAAAAAA", deTS(time.Hour), "primary note")))
	return deMultiHub(t, f, []string{"primary", "secondary"})
}

func TestDigestSweeps_WorkspaceBodyFailureStaysInItsSection_Behaviour(t *testing.T) {
	for _, tool := range []string{"get_multi_channel_digest", "get_morning_recap"} {
		f := deNewFake(t)
		hub := deSweepHub(t, f)

		res := deCall(t, hub, tool, map[string]any{})
		if res.IsError {
			t.Fatalf("%s: one broken workspace must not fail the sweep: %q", tool, resultText(res))
		}
		out := resultText(res)
		if !strings.Contains(out, "## [primary]") || !strings.Contains(out, "primary note") {
			t.Fatalf("%s: the healthy workspace must render:\n%s", tool, out)
		}
		if !strings.Contains(out, "## [secondary]\n_error: ") ||
			!strings.Contains(out, "missing_scope") {
			t.Fatalf("%s: the failure belongs inline under its label:\n%s", tool, out)
		}
	}
}

func TestDigestTools_DisabledByConfig_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deHub(t, f, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{
			"get_channel_digest": {}, "get_multi_channel_digest": {}, "get_morning_recap": {},
		}
	})
	for _, tool := range []string{"get_channel_digest", "get_multi_channel_digest", "get_morning_recap"} {
		if _, ok := deCallRaw(t, hub, tool, map[string]any{"channel": "alpha"}).(mcp.JSONRPCError); !ok {
			t.Fatalf("%s should not be registered when disabled", tool)
		}
	}
}
