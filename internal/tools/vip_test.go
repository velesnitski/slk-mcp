package tools

import (
	"context"
	"reflect"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

var vipRoster = []goslack.User{
	mnUser("U01", "sam.lee", "Sam", "Sam Lee"),
	mnUser("U02", "pat", "", "Pat Novak"),
	mnUser("U03", "kim.o", "Kim", "Kim Ortega"),
	mnUser("U04", "kim.r", "Kim", "Kim Ross"),
}

func TestResolveVIPs(t *testing.T) {
	v := resolveVIPs([]string{"Pat Novak", "@sam.lee", "U03", "Kim", "Nobody", " "}, vipRoster)

	if !v.has("U02") || !v.has("U01") || !v.has("U03") || v.has("U04") {
		t.Fatalf("ids=%v", v.ids)
	}
	if !reflect.DeepEqual(v.missing, []string{"Kim", "Nobody"}) {
		t.Fatalf("a shared name and an unknown one must be reported, got %v", v.missing)
	}
	// Platforms write names in either order.
	if v := resolveVIPs([]string{"Novak Pat"}, vipRoster); !v.has("U02") {
		t.Fatalf("swapped full name must match, got %v", v.ids)
	}
	tmWant(t, vipNote(resolveVIPs([]string{"Pat Novak", "Kim"}, vipRoster)), "VIP first: Pat Novak", "not matched here (no one, or more than one person): Kim")
	if vipNote(vipSet{}) != "" {
		t.Fatal("no VIPs, no note")
	}
}

func TestUnreadHasVIP(t *testing.T) {
	v := resolveVIPs([]string{"pat"}, vipRoster)
	dm := &slack.ChannelUnread{Channel: goslack.Channel{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "D9", IsIM: true, User: "U02"}}}}
	byMsg := &slack.ChannelUnread{Messages: []goslack.Message{{Msg: goslack.Msg{User: "U01"}}, {Msg: goslack.Msg{User: "U02"}}}}
	byReply := &slack.ChannelUnread{Replies: map[string][]goslack.Message{"1.0": {{Msg: goslack.Msg{User: "U02"}}}}}
	other := &slack.ChannelUnread{Messages: []goslack.Message{{Msg: goslack.Msg{User: "U01"}}}}

	for name, c := range map[string]struct {
		cu   *slack.ChannelUnread
		want bool
	}{"dm": {dm, true}, "message": {byMsg, true}, "reply": {byReply, true}, "other": {other, false}} {
		if got := unreadHasVIP(c.cu, v); got != c.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
	if unreadHasVIP(byMsg, vipSet{}) {
		t.Error("no VIPs configured, nothing is VIP")
	}
}

// The VIP's channel leads the sweep and is labelled, though by score it
// would come second.
func TestRunUnreadSummary_VIPLeads(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U1": "alex", "U2": "sam", "U3": "pat"})
	f.On("users.list", `{"ok":true,"members":[{"id":"U2","name":"sam","real_name":"Sam Lee"},{"id":"U3","name":"pat","real_name":"Pat Novak"}],"response_metadata":{"next_cursor":""}}`)
	f.uaConversations(t, uaChannel("C1", "busy", nil), uaChannel("C2", "quiet", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "busy", map[string]any{"last_read": "1700000000.000000"}),
		"C2": uaChannel("C2", "quiet", map[string]any{"last_read": "1700000000.000000"}),
	})
	busy := []map[string]any{}
	for i := 0; i < 6; i++ {
		busy = append(busy, uaMsg(uaTS(int64(-60*(i+1)), 100+i), "U2", "<@U1> urgent please look at this now", nil))
	}
	f.uaHistory(t, map[string][]map[string]any{
		"C1": busy,
		"C2": {uaMsg(uaTS(-3000, 900), "U3", "fyi, notes are up", nil)},
	})

	h := uaHub(t, f, func(c *config.Config) { c.VIPUsers = []string{"Pat Novak", "Ghost Person"} })
	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	q, b := strings.Index(out, "#quiet"), strings.Index(out, "#busy")
	if q < 0 || b < 0 || q > b {
		t.Fatalf("the VIP's channel must come first:\n%s", out)
	}
	tmWant(t, out, "⭐ VIP · ", "VIP not matched here (no one, or more than one person): Ghost Person")

	// Without VIPs the busy channel leads – the ordering above is the VIP's doing.
	vipCacheMu.Lock()
	vipCache = map[*slack.Client]vipCacheEntry{}
	vipCacheMu.Unlock()
	out = resultText(uaHub(t, f).runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))
	if strings.Index(out, "#busy") > strings.Index(out, "#quiet") || strings.Contains(out, "VIP") {
		t.Fatalf("without VIPs the score decides:\n%s", out)
	}
}

// A VIP's unanswered ask is on the ping list at once and leads it; the
// same ask from anyone else waits for stale_hours.
func TestRunMyActions_VIPAskIsPingedAtOnce(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "pat", "U3": "sam"})
	f.On("users.list", `{"ok":true,"members":[{"id":"U2","name":"pat","real_name":"Pat Novak"},{"id":"U3","name":"sam","real_name":"Sam Lee"}],"response_metadata":{"next_cursor":""}}`)
	vipTS, otherTS := uaTS(-2*3600, 100), uaTS(-3*3600, 200)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {
			uaHit("D2", "U2", vipTS, "U2", "можешь проверить домен?", "https://x.slack.com/archives/D2/p1"),
			uaHit("D3", "U3", otherTS, "U3", "можешь проверить сертификат?", "https://x.slack.com/archives/D3/p2"),
		},
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D2": {uaMsg(vipTS, "U2", "можешь проверить домен?", nil)},
		"D3": {uaMsg(otherTS, "U3", "можешь проверить сертификат?", nil)},
	})
	h := uaHub(t, f, func(c *config.Config) { c.VIPUsers = []string{"pat"} })

	out := resultText(h.runMyActions(context.Background(), actionDefaults(), ""))

	tmWant(t, out, "### On you (1)", "⭐ VIP · 💤 no reply 2h · asked of you by pat", "VIP first: Pat Novak")
	if strings.Contains(out, "сертификат") && !strings.Contains(out, "1 more open") {
		t.Fatalf("the non-VIP ask must wait for stale_hours:\n%s", out)
	}
	if strings.Contains(out, "⭐ VIP · 💤 no reply 3h") {
		t.Fatalf("only the VIP's item is marked:\n%s", out)
	}
}
