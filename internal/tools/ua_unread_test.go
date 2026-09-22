package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// uaUnreadDefaults mirrors the parameter defaults the MCP handler applies,
// so a direct runUnreadSummary call exercises the same code path the tool
// does. Tests override individual fields.
func uaUnreadDefaults() unreadParams {
	return unreadParams{
		maxPer:             20,
		replyCap:           3,
		logMode:            "auto",
		logSamples:         1,
		maxChars:           maxCharsAuto,
		threadMentionHours: 24,
		ownThreadHours:     24,
		canvasHours:        24,
		dmFullText:         true,
	}
}

// uaSweepFixture wires the minimal set of endpoints a plain unread sweep
// touches: one public channel with two human messages, one of them a
// thread parent with a reply.
func uaSweepFixture(t *testing.T, f *uaFakeSlack) (parentTS, replyTS, newestTS string) {
	t.Helper()
	parentTS = uaTS(-600, 100)
	replyTS = uaTS(-300, 200)
	newestTS = uaTS(-120, 300)

	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U1": "Alex", "U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg(newestTS, "U2", "second thing", nil),
			uaMsg(parentTS, "U2", "first thing", map[string]any{
				"thread_ts": parentTS, "reply_count": 1, "latest_reply": replyTS,
			}),
		},
	})
	f.uaReplies(t, map[string][]map[string]any{
		parentTS: {uaMsg(replyTS, "U2", "a reply in the thread", nil)},
	})
	return parentTS, replyTS, newestTS
}

func TestRunUnreadSummary_RendersChannelMessagesAndCursor(t *testing.T) {
	f := uaNewFakeSlack(t)
	_, _, newest := uaSweepFixture(t, f)
	h := uaHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	for _, want := range []string{
		"# Unread summary",
		"1 channels, 2 top-level + 1 thread replies",
		"cursor: " + newest,
		"## #alpha (2 msgs)",
		"second thing",
		"a reply in the thread",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "## [primary]") {
		t.Fatalf("single workspace must not emit a workspace heading:\n%s", out)
	}
	if !f.Called("conversations.replies") {
		t.Fatalf("thread parent should trigger a replies fetch; calls=%v", f.Calls())
	}
}

func TestRunUnreadSummary_EmptySweepSaysCaughtUpNotBlank(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaConversations(t)
	h := uaHub(t, f)

	res := h.runUnreadSummary(context.Background(), uaUnreadDefaults(), "")
	if res.IsError {
		t.Fatalf("an empty workspace is not an error: %q", resultText(res))
	}
	if got := resultText(res); got != "all caught up — 0 unread" {
		t.Fatalf("empty sweep must be visibly empty, got %q", got)
	}
}

func TestRunUnreadSummary_MentionsOnlyEmptyHasItsOwnWording(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaConversations(t)
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.mentionsOnly = true
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))
	if out != "no unread channels mention you" {
		t.Fatalf("mentions_only empty wording: %q", out)
	}
}

func TestRunUnreadSummary_UpstreamFailureIsAnError(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.OnError("users.conversations", "invalid_auth")
	h := uaHub(t, f)

	res := h.runUnreadSummary(context.Background(), uaUnreadDefaults(), "")
	if res == nil || !res.IsError {
		t.Fatalf("a failed sweep must not read as caught up, got %+v", res)
	}
	if !strings.Contains(resultText(res), "invalid_auth") {
		t.Fatalf("error text should name the upstream failure, got %q", resultText(res))
	}
}

func TestRunUnreadSummary_MentionsOnlyWithoutSelfIDErrors(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.OnError("auth.test", "invalid_auth")
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-60, 100), "U2", "ping <@U1>", nil)},
	})
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.mentionsOnly = true
	res := h.runUnreadSummary(context.Background(), p, "")
	if res == nil || !res.IsError {
		t.Fatalf("mentions_only without a self id must fail loudly, got %+v", res)
	}
	if !strings.Contains(resultText(res), "mentions_only requires auth.test") {
		t.Fatalf("unexpected error text: %q", resultText(res))
	}
}

func TestRunUnreadSummary_MentionsOnlyKeepsOnlyMentioningChannels(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t,
		uaChannel("C1", "alpha", nil),
		uaChannel("C2", "beta", nil),
	)
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
		"C2": uaChannel("C2", "beta", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-60, 100), "U2", "no tag here", nil)},
		"C2": {uaMsg(uaTS(-50, 100), "U2", "please look <@U1>", nil)},
	})
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.mentionsOnly = true
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if !strings.Contains(out, "#beta") {
		t.Fatalf("mentioning channel must survive:\n%s", out)
	}
	if strings.Contains(out, "#alpha") {
		t.Fatalf("non-mentioning channel must be filtered out:\n%s", out)
	}
	if !strings.Contains(out, "(mentions only)") {
		t.Fatalf("title should state the filter:\n%s", out)
	}
}

func TestRunUnreadSummary_AfterCursorDropsEverythingOlder(t *testing.T) {
	f := uaNewFakeSlack(t)
	old := uaTS(-3600, 100)
	fresh := uaTS(-60, 100)

	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg(fresh, "U2", "arrived after the cursor", nil),
			uaMsg(old, "U2", "already seen last pull", nil),
		},
	})
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.afterTS = uaTS(-600, 0)
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if !strings.Contains(out, "arrived after the cursor") {
		t.Fatalf("new message must survive the delta:\n%s", out)
	}
	if strings.Contains(out, "already seen last pull") {
		t.Fatalf("pre-cursor message must be dropped:\n%s", out)
	}
	if !strings.Contains(out, "1 channels, 1 top-level") {
		t.Fatalf("counters must reflect the delta, not the raw sweep:\n%s", out)
	}
}

func TestRunUnreadSummary_AfterCursorConsumingEverythingIsCaughtUp(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-3600, 100), "U2", "old news", nil)},
	})
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.afterTS = uaTS(-60, 0)
	if got := resultText(h.runUnreadSummary(context.Background(), p, "")); got != "all caught up — 0 unread" {
		t.Fatalf("a fully-consumed delta must say so plainly, got %q", got)
	}
}

func TestRunUnreadSummary_MaxCharsCapListsOmittedChannelsWithCounts(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})

	channels := []map[string]any{}
	infos := map[string]map[string]any{}
	history := map[string][]map[string]any{}
	names := []string{"alpha", "beta", "gamma", "delta"}
	for i, name := range names {
		id := []string{"C1", "C2", "C3", "C4"}[i]
		channels = append(channels, uaChannel(id, name, nil))
		infos[id] = uaChannel(id, name, map[string]any{"last_read": "1700000000.000000"})
		history[id] = []map[string]any{
			uaMsg(uaTS(int64(-60-i), 100), "U2", strings.Repeat("padding text ", 12), nil),
		}
	}
	f.uaConversations(t, channels...)
	f.uaInfo(t, infos)
	f.uaHistory(t, history)
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.maxChars = 260
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if !strings.Contains(out, "channels omitted by max_chars cap, with unread counts:") {
		t.Fatalf("capped channels must be named in a footer, not silently dropped:\n%s", out)
	}
	if !strings.Contains(out, "(1)") {
		t.Fatalf("footer must carry the per-channel volume:\n%s", out)
	}
	if !strings.Contains(out, "4 channels, 4 top-level") {
		t.Fatalf("header must still count everything found:\n%s", out)
	}
}

func TestRunUnreadSummary_IncludeRefsAppendsReferenceFooter(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-60, 100), "U2", "see PROJ-123 and branch feature/thing", nil)},
	})
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	withRefs := p
	withRefs.includeRefs = true
	with := resultText(h.runUnreadSummary(context.Background(), withRefs, ""))
	without := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if len(with) <= len(without) {
		t.Fatalf("include_refs should add a references block\nwith:\n%s\nwithout:\n%s", with, without)
	}
	if !strings.Contains(with, "PROJ-123") {
		t.Fatalf("references footer should carry the issue id:\n%s", with)
	}
}

func TestRunUnreadSummary_SkipLogAndGitModesOmitFeedChannels(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t,
		uaChannel("C1", "alpha", nil),
		uaChannel("C2", "alerts", nil),
		uaChannel("C3", "ci-deploy", nil),
	)
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
		"C2": uaChannel("C2", "alerts", map[string]any{"last_read": "1700000000.000000"}),
		"C3": uaChannel("C3", "ci-deploy", map[string]any{"last_read": "1700000000.000000"}),
	})
	bot := map[string]any{"bot_id": "B1", "subtype": "bot_message"}
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-60, 100), "U2", "a human sentence with enough words to matter", nil)},
		"C2": {uaMsg(uaTS(-59, 100), "", "ERROR disk pressure on node", bot)},
		"C3": {uaMsg(uaTS(-58, 100), "", "deploy succeeded for service", bot)},
	})
	h := uaHub(t, f)

	full := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(full, "#alerts") || !strings.Contains(full, "#ci-deploy") {
		t.Fatalf("feed channels should render by default:\n%s", full)
	}

	p := uaUnreadDefaults()
	p.skipLog = true
	p.skipGit = true
	lean := resultText(h.runUnreadSummary(context.Background(), p, ""))
	if strings.Contains(lean, "#alerts") || strings.Contains(lean, "#ci-deploy") {
		t.Fatalf("skip_log_mode/skip_git_mode should omit feed channels:\n%s", lean)
	}
	if !strings.Contains(lean, "#alpha") {
		t.Fatalf("human channel must survive the skips:\n%s", lean)
	}
	if !strings.Contains(lean, "3 channels,") {
		t.Fatalf("the header still counts what was swept:\n%s", lean)
	}
}

func TestRunUnreadSummary_LogModeOffRendersFeedAsPlainDigest(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{})
	f.uaConversations(t, uaChannel("C2", "alerts", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C2": uaChannel("C2", "alerts", map[string]any{"last_read": "1700000000.000000"}),
	})
	bot := map[string]any{"bot_id": "B1", "subtype": "bot_message"}
	f.uaHistory(t, map[string][]map[string]any{
		"C2": {uaMsg(uaTS(-60, 100), "", "ERROR something specific broke in the pipeline", bot)},
	})
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.logMode = "off"
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))
	if !strings.Contains(out, "ERROR something specific broke in the pipeline") {
		t.Fatalf("log_mode=off must render the message bodies:\n%s", out)
	}
}

func TestRunUnreadSummary_AnsweredDMIsHiddenWithANote(t *testing.T) {
	f := uaNewFakeSlack(t)
	peer := uaTS(-600, 100)
	mine := uaTS(-300, 100)

	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U1": "Alex", "U2": "Sam"})
	f.uaConversations(t, uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2"}))
	f.uaInfo(t, map[string]map[string]any{
		"D1": uaChannel("D1", "", map[string]any{
			"is_im": true, "user": "U2", "last_read": "1700000000.000000",
		}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D1": {
			uaMsg(mine, "U1", "already handled, here is the answer", nil),
			uaMsg(peer, "U2", "could you check the invoice?", nil),
		},
	})
	h := uaHub(t, f)

	hidden := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(hidden, "answered DM(s) hidden") {
		t.Fatalf("an answered DM should collapse to a note:\n%s", hidden)
	}
	if !strings.Contains(hidden, "@Sam") {
		t.Fatalf("the note should name the counterpart:\n%s", hidden)
	}
	if strings.Contains(hidden, "could you check the invoice?") {
		t.Fatalf("answered DM body should not be inlined:\n%s", hidden)
	}

	p := uaUnreadDefaults()
	p.showAnswered = true
	shown := resultText(h.runUnreadSummary(context.Background(), p, ""))
	if !strings.Contains(shown, "could you check the invoice?") {
		t.Fatalf("show_answered=true must inline the DM again:\n%s", shown)
	}
}

// uaSearchByQuery routes search.messages on a substring of the query, so
// the three backstops (`to:me`, `@handle`, `from:me`) can be answered
// independently. Unmatched queries return an empty match set.
func (f *uaFakeSlack) uaSearchByQuery(t *testing.T, byPrefix map[string][]map[string]any) {
	t.Helper()
	bodies := map[string]string{}
	for prefix, hits := range byPrefix {
		bodies[prefix] = uaJSON(t, map[string]any{
			"ok":       true,
			"messages": map[string]any{"matches": hits, "total": len(hits)},
		})
	}
	empty := `{"ok":true,"messages":{"matches":[],"total":0}}`
	f.OnFunc("search.messages", func(r *http.Request) string {
		q := r.Form.Get("query")
		for prefix, body := range bodies {
			if strings.Contains(q, prefix) {
				return body
			}
		}
		return empty
	})
}

func TestRunUnreadSummary_ThreadMentionBackstopAddsUnsweptChannel(t *testing.T) {
	f := uaNewFakeSlack(t)
	hitTS := uaTS(-120, 100)

	f.On("auth.test", uaAuthOK)
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t) // the regular sweep finds nothing unread
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"@alex": {uaHit("C9", "gamma", hitTS, "U2", "<@U1> can you confirm?", "")},
	})
	h := uaHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "#gamma") {
		t.Fatalf("a thread mention in an already-read channel must still surface:\n%s", out)
	}
	if !strings.Contains(out, "can you confirm?") {
		t.Fatalf("the mentioning message body must be rendered:\n%s", out)
	}
	if !strings.Contains(out, "cursor: "+hitTS) {
		t.Fatalf("the backstop hit must advance the cursor:\n%s", out)
	}
}

func TestRunUnreadSummary_BackstopFailureDegradesToUnreadOnly(t *testing.T) {
	f := uaNewFakeSlack(t)
	_, _, newest := uaSweepFixture(t, f)
	f.OnError("search.messages", "ratelimited_search") // both backstops fail
	h := uaHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "## #alpha") {
		t.Fatalf("a failing backstop must not sink the sweep:\n%s", out)
	}
	if !strings.Contains(out, "cursor: "+newest) {
		t.Fatalf("cursor should still come from the unread sweep:\n%s", out)
	}
}

func TestRunUnreadSummary_DMWindowOverrideSurfacesAlreadyReadDMs(t *testing.T) {
	f := uaNewFakeSlack(t)
	recent := uaTS(-120, 100)

	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2"}))
	// last_read is at the newest message, so the unread sweep sees nothing.
	f.uaInfo(t, map[string]map[string]any{
		"D1": uaChannel("D1", "", map[string]any{
			"is_im": true, "user": "U2", "last_read": recent,
		}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D1": {uaMsg(recent, "U2", "decision made in the DM", nil)},
	})
	h := uaHub(t, f)

	plain := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if plain != "all caught up — 0 unread" {
		t.Fatalf("a fully-read DM should not surface without the window, got %q", plain)
	}

	p := uaUnreadDefaults()
	p.dmWindowHours = 4
	windowed := resultText(h.runUnreadSummary(context.Background(), p, ""))
	if !strings.Contains(windowed, "decision made in the DM") {
		t.Fatalf("dm_window_hours must surface the already-read DM:\n%s", windowed)
	}
	if !strings.Contains(windowed, "@Sam") {
		t.Fatalf("DM heading should resolve the counterpart handle:\n%s", windowed)
	}
}

func TestRunUnreadSummary_CanvasEditSurvivesAnEmptySweep(t *testing.T) {
	f := uaNewFakeSlack(t)
	now := uaNowUnix()

	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaConversations(t)
	f.On("files.list", uaJSON(t, map[string]any{
		"ok": true,
		"files": []map[string]any{{
			"id":        "F1",
			"title":     "Launch checklist",
			"created":   now - 86400,
			"updated":   now - 600,
			"mimetype":  "text/plain",
			"permalink": "https://example.slack.com/docs/T1/F1",
			"channels":  []string{"C1"},
		}},
	}))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", nil),
	})
	h := uaHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "CANVASES — 1 updated") {
		t.Fatalf("a canvas edit is the only event; it must be reported:\n%s", out)
	}
	if !strings.Contains(out, "Launch checklist") {
		t.Fatalf("canvas title missing:\n%s", out)
	}
	if strings.Contains(out, "all caught up") {
		t.Fatalf("must not claim caught-up while a canvas changed:\n%s", out)
	}
}

func TestRunUnreadSummary_MultiWorkspaceSectionsAndCombinedCursor(t *testing.T) {
	f := uaNewFakeSlack(t)
	_, _, newest := uaSweepFixture(t, f)
	h := uaMultiHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	for _, want := range []string{
		"# Unread summary — 2 workspaces",
		"## [primary]",
		"## [secondary]",
		"cursor: primary=" + newest + ";secondary=" + newest,
		"(pass as after= next pull for an exact per-workspace delta)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "## [primary]") > strings.Index(out, "## [secondary]") {
		t.Fatalf("registry order must be preserved (primary first):\n%s", out)
	}
}

func TestRunUnreadSummary_MultiWorkspaceSkipsTokenlessWorkspace(t *testing.T) {
	f := uaNewFakeSlack(t)
	uaSweepFixture(t, f)
	h := uaMultiHub(t, f, func(c *config.Config) {
		c.UserToken = ""
		c.BotToken = "xoxb-test"
	})

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "## [secondary]\n_skipped: no user token for this workspace_") {
		t.Fatalf("a token-less workspace must say so rather than read as empty:\n%s", out)
	}
	if !strings.Contains(out, "## #alpha") {
		t.Fatalf("the workspace that does have a token must still render:\n%s", out)
	}
}

func TestRunUnreadSummary_MultiWorkspaceErrorIsScopedToItsSection(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.OnError("users.conversations", "invalid_auth")
	h := uaMultiHub(t, f)

	res := h.runUnreadSummary(context.Background(), uaUnreadDefaults(), "")
	out := resultText(res)
	if res.IsError {
		t.Fatalf("multi-workspace mode reports per-section errors, not a whole-call error: %q", out)
	}
	if strings.Count(out, "_error: ") != 2 {
		t.Fatalf("each failing workspace needs its own error line:\n%s", out)
	}
	if !strings.Contains(out, "invalid_auth") {
		t.Fatalf("the upstream error text must be visible:\n%s", out)
	}
}

func TestRunUnreadSummary_MultiWorkspacePerWorkspaceCursorApplies(t *testing.T) {
	f := uaNewFakeSlack(t)
	old := uaTS(-3600, 100)
	fresh := uaTS(-60, 100)

	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg(fresh, "U2", "brand new line", nil),
			uaMsg(old, "U2", "older line", nil),
		},
	})
	h := uaMultiHub(t, f)

	primaryCursor := uaTS(-1, 0)
	secondaryCursor := uaTS(-600, 0)

	p := uaUnreadDefaults()
	// primary is caught up past everything; secondary is behind.
	p.afterTS = "primary=" + primaryCursor + ";secondary=" + secondaryCursor
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	primary := out[strings.Index(out, "## [primary]"):strings.Index(out, "## [secondary]")]
	secondary := out[strings.Index(out, "## [secondary]"):]

	if !strings.Contains(primary, "all caught up") {
		t.Fatalf("primary's own cursor should consume its backlog:\n%s", primary)
	}
	if !strings.Contains(secondary, "brand new line") {
		t.Fatalf("secondary's cursor should still admit the newer line:\n%s", secondary)
	}
	if strings.Contains(secondary, "older line") {
		t.Fatalf("secondary's cursor should drop the older line:\n%s", secondary)
	}
	if !strings.Contains(out, "cursor: primary="+primaryCursor) {
		t.Fatalf("a caught-up workspace must carry its incoming cursor forward:\n%s", out)
	}
}

func TestRunUnreadSummary_UnknownWorkspaceNamesTheConfiguredOnes(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaMultiHub(t, f)

	res := h.runUnreadSummary(context.Background(), uaUnreadDefaults(), "ghost")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace must be an error, got %+v", res)
	}
	got := resultText(res)
	if !strings.Contains(got, `unknown workspace "ghost"`) ||
		!strings.Contains(got, "configured: primary, secondary") {
		t.Fatalf("error should name the configured labels, got %q", got)
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("routing must fail before any Slack call, got %v", f.Calls())
	}
}

func TestRunUnreadSummary_WorkspaceArgScopesToOneWorkspace(t *testing.T) {
	f := uaNewFakeSlack(t)
	uaSweepFixture(t, f)
	h := uaMultiHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), "secondary"))
	if strings.Contains(out, "## [secondary]") || strings.Contains(out, "workspaces") {
		t.Fatalf("a single scoped workspace renders unlabelled:\n%s", out)
	}
	if !strings.Contains(out, "## #alpha") {
		t.Fatalf("scoped sweep should still render its channels:\n%s", out)
	}
}

func TestWorkspaceOrder_ProjectsRegistryOrderToNames(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaMultiHub(t, f)

	got := workspaceOrder(h.Workspaces())
	if len(got) != 2 || got[0] != "primary" || got[1] != "secondary" {
		t.Fatalf("workspaceOrder = %v; want [primary secondary]", got)
	}
	if got := workspaceOrder(nil); len(got) != 0 {
		t.Fatalf("empty registry should yield no names, got %v", got)
	}
}

func TestRunUnreadSummary_DMWindowFailureFallsBackToUnreadOnly(t *testing.T) {
	f := uaNewFakeSlack(t)
	_, _, newest := uaSweepFixture(t, f)

	// The unread sweep lists conversations successfully; the DM-window
	// pass, which lists them again, fails.
	list := uaJSON(t, map[string]any{
		"ok":                true,
		"channels":          []map[string]any{uaChannel("C1", "alpha", nil)},
		"response_metadata": map[string]any{"next_cursor": ""},
	})
	calls := 0
	f.OnFunc("users.conversations", func(*http.Request) string {
		calls++
		if calls == 1 {
			return list
		}
		return `{"ok":false,"error":"ratelimited_list"}`
	})

	p := uaUnreadDefaults()
	p.dmWindowHours = 6
	out := resultText(uaHub(t, f).runUnreadSummary(context.Background(), p, ""))

	if !strings.Contains(out, "## #alpha") {
		t.Fatalf("a failed DM window must not sink the unread sweep:\n%s", out)
	}
	if !strings.Contains(out, "cursor: "+newest) {
		t.Fatalf("the sweep's own cursor must still be emitted:\n%s", out)
	}
}

func TestRunUnreadSummary_AnsweredDMNoteRidesAlongWithLiveChannels(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U1": "Alex", "U2": "Sam"})
	f.uaConversations(t,
		uaChannel("C1", "alpha", nil),
		uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2"}),
	)
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
		"D1": uaChannel("D1", "", map[string]any{
			"is_im": true, "user": "U2", "last_read": "1700000000.000000",
		}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-100, 100), "U2", "a live channel message", nil)},
		"D1": {
			uaMsg(uaTS(-200, 100), "U1", "already replied here", nil),
			uaMsg(uaTS(-300, 100), "U2", "their earlier ask", nil),
		},
	})

	out := resultText(uaHub(t, f).runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "answered DM(s) hidden") {
		t.Fatalf("the hidden-DM note must appear alongside live channels:\n%s", out)
	}
	if !strings.Contains(out, "## #alpha") {
		t.Fatalf("the live channel must still render:\n%s", out)
	}
	if strings.Contains(out, "their earlier ask") {
		t.Fatalf("the answered DM body must stay hidden:\n%s", out)
	}
}

func TestRunUnreadSummary_CanvasSectionAccompaniesUnreadChannels(t *testing.T) {
	f := uaNewFakeSlack(t)
	now := uaNowUnix()
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-100, 100), "U2", "a message in the channel", nil)},
	})
	f.On("files.list", uaJSON(t, map[string]any{
		"ok": true,
		"files": []map[string]any{{
			"id": "F1", "title": "Runbook", "created": now - 86400,
			"updated": now - 300, "mimetype": "text/plain", "channels": []string{"C1"},
		}},
	}))

	out := resultText(uaHub(t, f).runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "CANVASES — 1 updated") {
		t.Fatalf("the canvas block must accompany the message sweep:\n%s", out)
	}
	if !strings.Contains(out, "#alpha") || !strings.Contains(out, "a message in the channel") {
		t.Fatalf("the message sweep must still render:\n%s", out)
	}
	if !strings.Contains(out, "body not checked") {
		t.Fatalf("an unprobed canvas must say so rather than imply no mention:\n%s", out)
	}
}

func TestRunUnreadSummary_LowSignalChannelCollapsesToOneLine(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam", "U3": "Pat"})
	f.uaConversations(t, uaChannel("C1", "checkin", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "checkin", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg(uaTS(-100, 100), "U2", "+", nil),
			uaMsg(uaTS(-110, 100), "U3", "+", nil),
		},
	})

	out := resultText(uaHub(t, f).runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "short status updates from 2 people") {
		t.Fatalf("a status-only channel should collapse:\n%s", out)
	}
}

func TestRunUnreadSummary_ChannelWithNoRenderableContentIsOmittedButCounted(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t,
		uaChannel("C1", "alpha", nil),
		uaChannel("C2", "beta", nil),
	)
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
		"C2": uaChannel("C2", "beta", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg(uaTS(-100, 100), "U2", "a real message", nil)},
		"C2": {uaMsg(uaTS(-110, 100), "U2", "", nil)}, // no body, no files, no reactions
	})

	out := resultText(uaHub(t, f).runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if !strings.Contains(out, "2 channels, 2 top-level") {
		t.Fatalf("the header counts what the sweep found:\n%s", out)
	}
	if strings.Contains(out, "## #beta") {
		t.Fatalf("a channel with nothing renderable must not emit an empty heading:\n%s", out)
	}
	if !strings.Contains(out, "## #alpha") {
		t.Fatalf("the channel with content must render:\n%s", out)
	}
}
