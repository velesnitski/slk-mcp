package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// tmSearchHandler runs handleSearchMessages the way the MCP server would.
func tmSearchHandler(t *testing.T, h *Hub, args map[string]any) string {
	t.Helper()
	res, err := h.handleSearchMessages(context.Background(), callToolRequest("search_messages", args))
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	return resultText(res)
}

// tmDecisionsHandler runs handleFindDecisions and returns (text, isError).
func tmDecisionsHandler(t *testing.T, h *Hub, args map[string]any) (string, bool) {
	t.Helper()
	res, err := h.handleFindDecisions(context.Background(), callToolRequest("find_decisions", args))
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	return resultText(res), res.IsError
}

// ----------------------------------------------------------- search_messages

func TestSearchMessages_RendersHitsWithThreadChaining(t *testing.T) {
	var gotQuery, gotCount string
	f := newFakeSlack(t)
	f.OnFunc("search.messages", func(r *http.Request) string {
		gotQuery, gotCount = r.Form.Get("query"), r.Form.Get("count")
		return tmSearchBody(tmSearchMatch("C1", "alpha", "sam", tmReplyTS, "shipped it",
			tmHost+"/archives/C1/p1700000100000200?thread_ts="+tmRootTS))
	})
	h := newFakeHub(t, f)

	out := tmSearchHandler(t, h, map[string]any{"query": "in:#alpha shipped", "limit": 5})

	tmWant(t, out, "1 hits for: in:#alpha shipped", "#alpha", "shipped it",
		"thread_ts="+tmRootTS, tmHost+"/archives/C1/p1700000100000200")
	if gotQuery != "in:#alpha shipped" {
		t.Fatalf("query not forwarded verbatim: %q", gotQuery)
	}
	if gotCount != "5" {
		t.Fatalf("limit should reach Slack as count, got %q", gotCount)
	}
}

func TestSearchMessages_FullTextDisablesBodyTruncation(t *testing.T) {
	long := strings.Repeat("z", 300)
	f := newFakeSlack(t).On("search.messages", tmSearchBody(
		tmSearchMatch("C1", "alpha", "sam", tmReplyTS, long, ""),
	))
	h := newFakeHub(t, f)

	compact := tmSearchHandler(t, h, map[string]any{"query": "q"})
	tmWant(t, compact, "...")
	if strings.Contains(compact, long) {
		t.Fatal("the default must truncate long bodies")
	}

	full := tmSearchHandler(t, h, map[string]any{"query": "q", "full_text": true})
	tmWant(t, full, long)
}

func TestSearchMessages_NoHitsSaysSo(t *testing.T) {
	f := newFakeSlack(t).On("search.messages", tmSearchBody())
	h := newFakeHub(t, f)

	out := tmSearchHandler(t, h, map[string]any{"query": "nothing here"})
	tmWant(t, out, "no hits for: nothing here")
}

func TestSearchMessages_QueryIsRequired(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	res, _ := h.handleSearchMessages(context.Background(), callToolRequest("search_messages", map[string]any{}))
	if res == nil || !res.IsError {
		t.Fatalf("a missing query must error, got %+v", res)
	}
	tmWant(t, resultText(res), "query is required")
	if len(f.Calls()) != 0 {
		t.Fatalf("validation must precede the search, got %v", f.Calls())
	}
}

func TestSearchMessages_SearchFailureIsReported(t *testing.T) {
	f := newFakeSlack(t).OnError("search.messages", "not_allowed_token_type")
	h := newFakeHub(t, f)

	res, _ := h.handleSearchMessages(context.Background(), callToolRequest("search_messages", map[string]any{"query": "q"}))
	if res == nil || !res.IsError {
		t.Fatalf("a failed search must not read as empty, got %+v", res)
	}
	tmWant(t, resultText(res), "search.messages", "not_allowed_token_type")
}

func TestSearchMessages_UnknownWorkspaceIsRejected(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	res, _ := h.handleSearchMessages(context.Background(),
		callToolRequest("search_messages", map[string]any{"query": "q", "workspace": "ghost"}))
	if res == nil || !res.IsError {
		t.Fatalf("an unknown workspace must error, got %+v", res)
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("routing must fail before the search, got %v", f.Calls())
	}
}

func TestSearchMessages_DMHitIsLabelledByHandleNotByRawID(t *testing.T) {
	f := newFakeSlack(t)
	tmUsers(f, map[string]string{"U2": "sam"})
	// Slack parks the counterpart's user id in channel.name for a DM hit.
	f.On("search.messages", tmSearchBody(
		`{"type":"message","channel":{"id":"D1","name":"U2"},"user":"U2","username":"sam",`+
			`"ts":"`+tmReplyTS+`","text":"ping","permalink":""}`,
	))
	h := newFakeHub(t, f)

	out := tmSearchHandler(t, h, map[string]any{"query": "ping"})

	tmWant(t, out, "@sam", "ping")
	tmNotWant(t, out, "#U2")
}

func TestSearchMessages_WithContextInlinesTheSurroundingTurns(t *testing.T) {
	f := newFakeSlack(t)
	tmUsers(f, map[string]string{"U1": "alex", "U2": "sam"})
	f.On("search.messages", tmSearchBody(
		tmSearchMatch("C1", "alpha", "sam", tmReplyTS, "yes", ""),
	))
	f.OnFunc("conversations.history", func(r *http.Request) string {
		if r.Form.Get("oldest") != "" { // the "after" half
			return tmHistoryBody(
				`{"type":"message","user":"U1","text":"thanks for confirming","ts":"1700000200.000300"}`,
			)
		}
		return tmHistoryBody(
			`{"type":"message","user":"U1","text":"are we agreed","ts":"` + tmRootTS + `"}`,
		)
	})
	h := newFakeHub(t, f)

	out := tmSearchHandler(t, h, map[string]any{
		"query": "yes", "with_context": true, "context_messages": 2,
	})

	tmWant(t, out, "↳", "are we agreed", "↪", "thanks for confirming", "alex")
	if n := tmCountCalls(f, "conversations.history"); n != 2 {
		t.Fatalf("context needs one history call per side, got %d", n)
	}
}

func TestSearchMessages_WithContextSkipsHitsWithoutAChannelID(t *testing.T) {
	f := newFakeSlack(t).On("search.messages", tmSearchBody(
		tmSearchMatch("", "alpha", "sam", tmReplyTS, "orphan hit", ""),
	))
	h := newFakeHub(t, f)

	out := tmSearchHandler(t, h, map[string]any{"query": "q", "with_context": true})

	tmWant(t, out, "orphan hit")
	if f.Called("conversations.history") {
		t.Fatalf("no channel id means no context fetch, calls=%v", f.Calls())
	}
}

func TestSearchMessages_ContextFetchFailureStillRendersTheHit(t *testing.T) {
	f := newFakeSlack(t).On("search.messages", tmSearchBody(
		tmSearchMatch("C1", "alpha", "sam", tmReplyTS, "shipped it", ""),
	))
	f.OnError("conversations.history", "channel_not_found")
	h := newFakeHub(t, f)

	out := tmSearchHandler(t, h, map[string]any{"query": "q", "with_context": true})

	// Context is best-effort: losing it must not lose the hit.
	tmWant(t, out, "1 hits for: q", "shipped it")
}

// ------------------------------------------------------------ find_decisions

func tmDecisionHub(t *testing.T, f *fakeSlack) *Hub {
	t.Helper()
	return newFakeHub(t, f, func(c *config.Config) {
		c.DecisionKeywords = []string{"decided"}
		c.DecisionReactions = []string{"white_check_mark"}
	})
}

func TestFindDecisions_ReportsKeywordAndReactionMatches(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex", "U2": "sam"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"we decided to ship","ts":"`+tmRootTS+`"}`,
		`{"type":"message","user":"U2","text":"proposal","ts":"`+tmReplyTS+`",
		  "reactions":[{"name":"white_check_mark","count":1,"users":["U1"]}]}`,
		`{"type":"message","user":"U2","text":"unrelated chatter","ts":"1700000200.000300"}`,
	))
	h := tmDecisionHub(t, f)

	out, isErr := tmDecisionsHandler(t, h, map[string]any{"channels": "alpha"})
	if isErr {
		t.Fatalf("unexpected error result: %q", out)
	}

	tmWant(t, out, "2 decisions (last 72h)",
		"- #alpha", "[keyword:decided] we decided to ship",
		"[reaction::white_check_mark:] proposal")
	tmNotWant(t, out, "unrelated chatter")
}

func TestFindDecisions_NoMatchesSaysSoWithTheWindow(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"just chatting","ts":"`+tmRootTS+`"}`,
	))
	h := tmDecisionHub(t, f)

	out, isErr := tmDecisionsHandler(t, h, map[string]any{"channels": "alpha", "hours": 12})
	if isErr {
		t.Fatalf("an empty result is not an error: %q", out)
	}
	tmWant(t, out, "no decisions found in last 12h")
}

func TestFindDecisions_ChannelResolutionErrorIsInlinedNotFatal(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"we decided to ship","ts":"`+tmRootTS+`"}`,
	))
	h := tmDecisionHub(t, f)

	out, isErr := tmDecisionsHandler(t, h, map[string]any{"channels": "alpha, gamma"})
	if isErr {
		t.Fatalf("one bad channel must not fail the whole scan: %q", out)
	}
	tmWant(t, out, "- #gamma error:", "we decided to ship")
}

func TestFindDecisions_HistoryErrorIsInlinedNotFatal(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.OnError("conversations.history", "not_in_channel")
	h := tmDecisionHub(t, f)

	out, isErr := tmDecisionsHandler(t, h, map[string]any{"channels": "alpha"})
	if isErr {
		t.Fatalf("a per-channel failure must not fail the scan: %q", out)
	}
	tmWant(t, out, "- #alpha error:", "not_in_channel")
}

func TestFindDecisions_NoChannelsAvailableIsAnActionableError(t *testing.T) {
	f := newFakeSlack(t).On("users.conversations",
		`{"ok":true,"channels":[],"response_metadata":{"next_cursor":""}}`)
	h := tmDecisionHub(t, f)

	out, isErr := tmDecisionsHandler(t, h, map[string]any{})
	if !isErr {
		t.Fatalf("no channels at all should error, got %q", out)
	}
	tmWant(t, out, "no channels available", "pass channels")
}

func TestFindDecisions_AutodiscoverFailureIsReported(t *testing.T) {
	f := newFakeSlack(t).OnError("users.conversations", "invalid_auth")
	h := tmDecisionHub(t, f)

	out, isErr := tmDecisionsHandler(t, h, map[string]any{})
	if !isErr {
		t.Fatalf("a failed auto-discovery should error, got %q", out)
	}
	tmWant(t, out, "auto-discover channels:", "invalid_auth")
}

func TestFindDecisions_ConfiguredChannelsAreUsedWhenNoneArePassed(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"we decided to ship","ts":"`+tmRootTS+`"}`,
	))
	h := newFakeHub(t, f, func(c *config.Config) {
		c.Channels = []string{"alpha"}
		c.DecisionKeywords = []string{"decided"}
	})

	out, isErr := tmDecisionsHandler(t, h, map[string]any{})
	if isErr {
		t.Fatalf("unexpected error result: %q", out)
	}
	tmWant(t, out, "1 decisions (last 72h)", "- #alpha")
	if f.Called("users.conversations") {
		t.Fatalf("configured channels must short-circuit auto-discovery, calls=%v", f.Calls())
	}
}

// --------------------------------------------------------------- registration

func TestRegisterSearchTools_RegistersBothTools(t *testing.T) {
	f := newFakeSlack(t)
	names := tmToolNames(t, tmServer(t, newFakeHub(t, f).registerSearchTools))

	for _, want := range []string{"search_messages", "find_decisions"} {
		if !tmHas(names, want) {
			t.Fatalf("%s should be registered, got %v", want, names)
		}
	}
}

func TestRegisterSearchTools_HonoursTheDisabledList(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{"find_decisions": {}}
	})
	names := tmToolNames(t, tmServer(t, h.registerSearchTools))

	if tmHas(names, "find_decisions") {
		t.Fatalf("find_decisions was disabled, got %v", names)
	}
	if !tmHas(names, "search_messages") {
		t.Fatalf("search_messages should survive, got %v", names)
	}
}

func TestRegisterSearchTools_ReachableThroughTheServer(t *testing.T) {
	f := newFakeSlack(t).On("search.messages", tmSearchBody(
		tmSearchMatch("C1", "alpha", "sam", tmReplyTS, "shipped it", ""),
	))
	s := tmServer(t, newFakeHub(t, f).registerSearchTools)

	out := resultText(tmCall(t, s, "search_messages", map[string]any{"query": "shipped"}))
	tmWant(t, out, "1 hits for: shipped", "shipped it")
}
