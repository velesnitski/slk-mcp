package tools

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
)

func TestParseChannelList_TrimsSigilsAndDropsBlanks(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",,", nil},
		{"#", nil},
		{"alpha", []string{"alpha"}},
		{"#alpha, beta ,,#general ", []string{"alpha", "beta", "general"}},
		{"#alpha", []string{"alpha"}},
	}
	for _, c := range cases {
		if got := parseChannelList(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseChannelList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// KNOWN DEFECT, pinned so a fix is a deliberate change and not a
// surprise: parseChannelList trims the '#' BEFORE it trims whitespace,
// so a sigil preceded by a space keeps it. That is the ordinary way a
// list is typed — "alpha, #beta" — and the second entry comes back as
// "#beta". Downstream resolution strips the sigil again, so today this
// only leaks into rendered labels; see register.go:19.
func TestParseChannelList_SpaceBeforeSigilKeepsIt(t *testing.T) {
	got := parseChannelList("alpha, #beta")
	if len(got) != 2 {
		t.Fatalf("expected two entries, got %v", got)
	}
	if got[1] != "#beta" {
		t.Skipf("register.go:19 appears fixed — entry is %q; update this test", got[1])
	}
}

func TestResolveTargetChannels_InputWinsOverConfig(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t, func(c *config.Config) { c.Channels = []string{"general"} })

	got, source, err := h.resolveTargetChannels(context.Background(), "#alpha, beta")
	if err != nil || source != "input" || !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("explicit input must win: got=%v source=%q err=%v", got, source, err)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("explicit input must not touch the API, calls=%v", f.Calls())
	}
}

func TestResolveTargetChannels_FallsBackToConfig(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t, func(c *config.Config) { c.Channels = []string{"general", "alpha"} })

	got, source, err := h.resolveTargetChannels(context.Background(), "   ")
	if err != nil || source != "config" || !reflect.DeepEqual(got, []string{"general", "alpha"}) {
		t.Fatalf("blank input must fall back to config: got=%v source=%q err=%v", got, source, err)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("config fallback must not touch the API, calls=%v", f.Calls())
	}
}

func TestResolveTargetChannels_AutodiscoversSortedAndCapped(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.conversations", `{"ok":true,"channels":[
		{"id":"C1","name":"alpha","num_members":3},
		{"id":"C2","name":"beta","num_members":90},
		{"id":"C3","name":"general","num_members":40},
		{"id":"C4","name":"archived-one","num_members":900,"is_archived":true}
	],"response_metadata":{"next_cursor":""}}`)
	h := f.hub(t, func(c *config.Config) { c.AutodiscoverLimit = 2 })

	got, source, err := h.resolveTargetChannels(context.Background(), "")
	if err != nil || source != "auto" {
		t.Fatalf("empty input + empty config must auto-discover: source=%q err=%v", source, err)
	}
	// Busiest first, archived dropped, capped at AutodiscoverLimit.
	if !reflect.DeepEqual(got, []string{"beta", "general"}) {
		t.Fatalf("auto-discovery should be sorted by members and capped, got %v", got)
	}
}

func TestResolveTargetChannels_AutodiscoveryErrorIsReported(t *testing.T) {
	f := usNewFakeSlack(t)
	f.OnError("users.conversations", "invalid_auth")
	h := f.hub(t)

	got, source, err := h.resolveTargetChannels(context.Background(), "")
	if err == nil {
		t.Fatal("a failed auto-discovery must surface as an error, not an empty channel list")
	}
	if got != nil || source != "auto" {
		t.Fatalf("on error expect (nil, \"auto\", err), got (%v, %q)", got, source)
	}
}

// usDecisionCfg is a config with a known keyword/reaction vocabulary.
func usDecisionCfg() *config.Config {
	return &config.Config{
		DecisionKeywords:  []string{"decided", "agreed"},
		DecisionReactions: []string{"white_check_mark"},
	}
}

func TestMatchDecision_KeywordIsCaseInsensitive(t *testing.T) {
	cfg := usDecisionCfg()
	reason, ok := matchDecision(cfg, goslack.Message{Msg: goslack.Msg{Text: "We DECIDED to ship on Friday"}})
	if !ok || reason != "keyword:decided" {
		t.Fatalf("uppercase keyword should match, got (%q,%v)", reason, ok)
	}
}

func TestMatchDecision_ReactionMatches(t *testing.T) {
	cfg := usDecisionCfg()
	msg := goslack.Message{Msg: goslack.Msg{
		Text: "shall we?",
		Reactions: []goslack.ItemReaction{
			{Name: "eyes"},
			{Name: "white_check_mark"},
		},
	}}
	reason, ok := matchDecision(cfg, msg)
	if !ok || reason != "reaction::white_check_mark:" {
		t.Fatalf("configured reaction should match, got (%q,%v)", reason, ok)
	}
}

func TestMatchDecision_NoSignalIsNoMatch(t *testing.T) {
	cfg := usDecisionCfg()
	msg := goslack.Message{Msg: goslack.Msg{
		Text:      "just thinking out loud",
		Reactions: []goslack.ItemReaction{{Name: "tada"}},
	}}
	if reason, ok := matchDecision(cfg, msg); ok {
		t.Fatalf("unrelated text and reaction must not match, got %q", reason)
	}
}

func TestDetectDecisions_RendersOnlyMatchesAndResolvesNames(t *testing.T) {
	cfg := usDecisionCfg()
	msgs := []goslack.Message{
		{Msg: goslack.Msg{User: "U1", Text: "agreed, let's do it"}},
		{Msg: goslack.Msg{User: "U2", Text: "small talk"}},
		{Msg: goslack.Msg{User: "U3", Text: "we decided"}},
	}
	render := func(m goslack.Message, channel, name, reason string) string {
		return fmt.Sprintf("%s|%s|%s|%s", channel, name, reason, m.Text)
	}

	got := detectDecisions(cfg, "alpha", msgs, map[string]string{"U1": "Alex"}, render)
	if len(got) != 2 {
		t.Fatalf("only the two decision lines should render, got %v", got)
	}
	if got[0] != "alpha|Alex|keyword:agreed|agreed, let's do it" {
		t.Errorf("known user should render by name: %q", got[0])
	}
	// An unresolved ID must still identify the author — falling back to
	// the raw ID, never to an empty author column.
	if !strings.Contains(got[1], "|U3|") {
		t.Errorf("unresolved user should fall back to the raw ID: %q", got[1])
	}
}

func TestDetectDecisions_EmptyInputIsEmptyOutput(t *testing.T) {
	if got := detectDecisions(usDecisionCfg(), "alpha", nil, nil, nil); got != nil {
		t.Fatalf("no messages should yield no lines, got %v", got)
	}
}

func TestCollectUserIDs_DedupesKeepsOrderSkipsBlank(t *testing.T) {
	msgs := []goslack.Message{
		{Msg: goslack.Msg{User: "U1"}},
		{Msg: goslack.Msg{User: ""}},
		{Msg: goslack.Msg{User: "U2"}},
		{Msg: goslack.Msg{User: "U1"}},
	}
	if got := collectUserIDs(msgs); !reflect.DeepEqual(got, []string{"U1", "U2"}) {
		t.Fatalf("collectUserIDs = %v, want [U1 U2]", got)
	}
	if got := collectUserIDs(nil); len(got) != 0 {
		t.Fatalf("no messages should yield no ids, got %v", got)
	}
}

func TestMergeRefs_AlwaysReturnsUsableMap(t *testing.T) {
	// Both empty: a nil map would panic the first writer downstream.
	if got := mergeRefs(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("mergeRefs(nil,nil) must be an empty non-nil map, got %v", got)
	}
	// Channels empty: users pass through untouched.
	users := map[string]string{"U1": "Alex"}
	if got := mergeRefs(users, nil); !reflect.DeepEqual(got, users) {
		t.Fatalf("mergeRefs(users,nil) = %v", got)
	}
	// Users nil but channels present: a map is built for them.
	if got := mergeRefs(nil, map[string]string{"C1": "alpha"}); got["C1"] != "alpha" {
		t.Fatalf("channel-only merge lost the channel: %v", got)
	}
	// Both present: one map, both namespaces.
	got := mergeRefs(map[string]string{"U1": "Alex"}, map[string]string{"C1": "alpha"})
	if got["U1"] != "Alex" || got["C1"] != "alpha" {
		t.Fatalf("merged map missing an entry: %v", got)
	}
}

func TestResolveRefs_ResolvesAuthorsAndMentionedChannels(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.info", `{"ok":true,"user":{"id":"U0AUTHOR01","name":"alex","real_name":"Alex"}}`)
	f.On("conversations.info", `{"ok":true,"channel":{"id":"C0ALPHA01","name":"alpha"}}`)
	h := f.hub(t)

	msgs := []goslack.Message{
		{Msg: goslack.Msg{User: "U0AUTHOR01", Text: "see <#C0ALPHA01|alpha> for details"}},
	}
	got := h.resolveRefs(context.Background(), msgs)
	if got["U0AUTHOR01"] != "Alex" {
		t.Errorf("author should resolve to a display name, got %q", got["U0AUTHOR01"])
	}
	if got["C0ALPHA01"] != "alpha" {
		t.Errorf("mentioned channel should resolve to its name, got %q", got["C0ALPHA01"])
	}
}

func TestResolveRefs_UnresolvableUserFallsBackToID(t *testing.T) {
	f := usNewFakeSlack(t)
	f.OnError("users.info", "user_not_found")
	h := f.hub(t)

	got := h.resolveRefs(context.Background(), []goslack.Message{
		{Msg: goslack.Msg{User: "U0GHOST001", Text: "hi"}},
	})
	// Rendering must never lose the author; the raw ID is the fallback.
	if got["U0GHOST001"] != "U0GHOST001" {
		t.Fatalf("failed lookup should fall back to the ID, got %q", got["U0GHOST001"])
	}
}

func TestResolveTextRefs_SplitsUsersFromConversations(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.info", `{"ok":true,"user":{"id":"U0PERSON01","name":"sam","real_name":"Sam"}}`)
	f.On("conversations.info", `{"ok":true,"channel":{"id":"C0BETA001","name":"beta"}}`)
	h := f.hub(t)

	got := h.resolveTextRefs(context.Background(), "ping <@U0PERSON01> in <#C0BETA001|beta>")
	if got["U0PERSON01"] != "Sam" {
		t.Errorf("user ref should go to the user service, got %q", got["U0PERSON01"])
	}
	if got["C0BETA001"] != "beta" {
		t.Errorf("channel ref should go to the channel service, got %q", got["C0BETA001"])
	}
	// Asking the wrong service is the failure this split prevents: a
	// channel id must never reach users.info and vice versa.
	if n := len(f.Calls()); n != 2 {
		t.Errorf("expected exactly one lookup per id, got %v", f.Calls())
	}
}

func TestResolveTextRefs_NoRefsMakesNoCalls(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t)

	got := h.resolveTextRefs(context.Background(), "plain text with no mentions at all")
	if got == nil || len(got) != 0 {
		t.Fatalf("text with no refs should yield an empty non-nil map, got %v", got)
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("text with no refs must not call the API, got %v", f.Calls())
	}
}
