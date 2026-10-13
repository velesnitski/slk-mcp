package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// ADR 119: get_message reactors=true names who reacted.

func reactorsFake(t *testing.T, reactionsJSON string) *fakeSlack {
	t.Helper()
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex", "U2": "sam", "U3": "kim"})
	f.On("conversations.history", tmHistoryBody(
		`{"type":"message","user":"U1","text":"good news","ts":"`+tmRootTS+`","reactions":`+reactionsJSON+`}`,
	))
	return f
}

func TestGetMessage_ReactorsNamesEveryoneFromHistoryWithoutAnExtraCall(t *testing.T) {
	f := reactorsFake(t, `[{"name":"tada","count":2,"users":["U2","U3"]},{"name":"heart","count":1,"users":["U2"]}]`)
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"channel": "alpha", "ts": tmRootTS, "reactors": true}))

	tmWant(t, out,
		"reactions: :tada: ×2  :heart: ×1",
		"reacted by:",
		":tada: ×2 — sam, kim",
		":heart: ×1 — sam",
		"everyone who reacted (2): kim, sam",
	)
	if f.Called("reactions.get") {
		t.Error("complete user lists in history must not trigger reactions.get")
	}
}

func TestGetMessage_ReactorsFillsATruncatedListFromReactionsGet(t *testing.T) {
	f := reactorsFake(t, `[{"name":"tada","count":3,"users":["U2"]}]`)
	f.On("reactions.get", `{"ok":true,"type":"message","message":{"reactions":[{"name":"tada","count":3,"users":["U2","U3","U1"]}]}}`)
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"channel": "alpha", "ts": tmRootTS, "reactors": true}))

	tmWant(t, out, ":tada: ×3 — sam, kim, alex", "everyone who reacted (3): alex, kim, sam")
	tmNotWant(t, out, "not listed", "partial")
}

func TestGetMessage_ReactorsShowsPartialListsWhenTheFullLookupFails(t *testing.T) {
	f := reactorsFake(t, `[{"name":"tada","count":3,"users":["U2"]}]`)
	f.OnError("reactions.get", "missing_scope")
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"channel": "alpha", "ts": tmRootTS, "reactors": true}))

	tmWant(t, out, ":tada: ×3 — sam (+2 not listed)", "everyone who reacted (1): sam", "note: partial", "missing_scope")
}

func TestGetMessage_ReactorsOffByDefault(t *testing.T) {
	f := reactorsFake(t, `[{"name":"tada","count":3,"users":["U2"]}]`)
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"channel": "alpha", "ts": tmRootTS}))

	tmNotWant(t, out, "reacted by:")
	if f.Called("reactions.get") {
		t.Error("reactions.get must not be called unless reactors=true")
	}
}

func TestGetMessage_ReactorsOnAMessageWithoutReactionsAddsNothing(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	tmUsers(f, map[string]string{"U1": "alex"})
	f.On("conversations.history", tmHistoryBody(`{"type":"message","user":"U1","text":"quiet","ts":"`+tmRootTS+`"}`))
	s := tmServer(t, newFakeHub(t, f).registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"channel": "alpha", "ts": tmRootTS, "reactors": true}))

	tmNotWant(t, out, "reacted by:", "reactions:")
}

func TestRenderReactors_UnresolvedIDFallsBackToTheIDAndDedupes(t *testing.T) {
	got := renderReactors([]goslack.ItemReaction{
		{Name: "a", Count: 2, Users: []string{"U9", "U1"}},
		{Name: "b", Count: 1, Users: []string{"U1"}},
	}, map[string]string{"U1": "alex"}, "")

	want := "reacted by:\n  :a: ×2 — U9, alex\n  :b: ×1 — alex\neveryone who reacted (2): U9, alex"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "note:") {
		t.Fatal("no note without one")
	}
}

func TestReactionsTruncated(t *testing.T) {
	for _, tc := range []struct {
		in   []goslack.ItemReaction
		want bool
	}{
		{nil, false},
		{[]goslack.ItemReaction{{Count: 1, Users: []string{"U1"}}}, false},
		{[]goslack.ItemReaction{{Count: 1, Users: []string{"U1"}}, {Count: 2, Users: []string{"U1"}}}, true},
	} {
		if got := reactionsTruncated(tc.in); got != tc.want {
			t.Errorf("reactionsTruncated(%+v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
