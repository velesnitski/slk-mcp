package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// ADR 128: a DM is read and shown as a conversation, a cut says what is
// missing and how to read it, and the channel follow-up note stays out
// of DMs.

func ccMessages(n int, user, prefix string) []map[string]any {
	out := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		// Newest first, as conversations.history returns them.
		out = append(out, uaMsg(uaTS(int64(-60*(i+1)), 100), user, fmt.Sprintf("%s %d", prefix, i), nil))
	}
	return out
}

func TestRunUnreadSummary_ADMIsNotCutLikeAChannel(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t,
		uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2"}),
		uaChannel("C1", "alpha", nil),
	)
	f.uaInfo(t, map[string]map[string]any{
		"D1": uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2", "last_read": "1700000000.000000"}),
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	pages := map[string][]map[string]any{
		"D1": ccMessages(30, "U2", "dm line"),
		"C1": ccMessages(30, "U2", "a longer channel update about the release, item"),
	}
	var mu sync.Mutex
	limits := map[string][]string{}
	f.OnFunc("conversations.history", func(r *http.Request) string {
		mu.Lock()
		limits[r.Form.Get("channel")] = append(limits[r.Form.Get("channel")], r.Form.Get("limit"))
		mu.Unlock()
		return uaJSON(t, map[string]any{"ok": true, "messages": pages[r.Form.Get("channel")], "has_more": false})
	})
	h := uaHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	dm := out[strings.Index(out, "## @"):]
	if i := strings.Index(dm, "\n## "); i > 0 {
		dm = dm[:i]
	}
	if !strings.Contains(dm, "dm line 29") || strings.Contains(dm, "older messages not shown") {
		t.Fatalf("all 30 DM messages must be shown, the oldest included:\n%s", dm)
	}
	if !strings.Contains(out, "+10 older messages not shown — get_channel_digest channel=C1 reads the rest") {
		t.Fatalf("a cut channel must say what is missing and how to read it:\n%s", out)
	}
	// The unread read is the first; later reads (the answered-DM probe)
	// ask for less and are not under test here.
	if limits["D1"][0] != fmt.Sprint(slack.DMMinMessages+30) || limits["C1"][0] != "50" {
		t.Fatalf("history limits = %v, want D1 %d and C1 50", limits, slack.DMMinMessages+30)
	}
}

func TestRunUnreadSummary_FollowUpNoteStaysOutOfDMs(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t)
	asked, answered := uaTS(-900, 100), uaTS(-300, 100)
	f.OnFunc("search.messages", func(r *http.Request) string {
		var hits []map[string]any
		if strings.Contains(r.Form.Get("query"), "from:me") {
			hits = []map[string]any{
				uaHit("D1", "", answered, "U1", "sure", ""),
				uaHit("C1", "alpha", answered, "U1", "on it", ""),
			}
		} else {
			hits = []map[string]any{
				uaHit("D1", "", asked, "U2", "<@U1> can you look?", ""),
				uaHit("C1", "alpha", asked, "U2", "<@U1> can you look?", ""),
			}
		}
		return uaJSON(t, map[string]any{"ok": true, "messages": map[string]any{"matches": hits, "total": len(hits)}})
	})
	h := uaHub(t, f)
	p := uaUnreadDefaults()
	p.ownThreadHours = 0

	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if n := strings.Count(out, "you posted in this channel after it"); n != 1 {
		t.Fatalf("the note must appear once, under the channel mention only (got %d):\n%s", n, out)
	}
	ch := out[strings.Index(out, "#alpha"):]
	if !strings.Contains(ch, "you posted in this channel after it") {
		t.Fatalf("the channel mention lost its note:\n%s", out)
	}
}

func TestMergeThreadMentions_KeepsNewestFirst(t *testing.T) {
	ch := goslack.Channel{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C1"}}}
	msg := func(ts string) goslack.Message { m := goslack.Message{}; m.Timestamp = ts; return m }
	base := []*slack.ChannelUnread{{Channel: ch, Messages: []goslack.Message{msg("300.0"), msg("100.0")}}}
	got := mergeThreadMentions(base, []*slack.ChannelUnread{{Channel: ch, Messages: []goslack.Message{msg("200.0")}}})
	var order []string
	for _, m := range got[0].Messages {
		order = append(order, m.Timestamp)
	}
	if strings.Join(order, ",") != "300.0,200.0,100.0" {
		t.Fatalf("order = %v, want newest first", order)
	}
}

func TestDMCapAndReadMoreHint(t *testing.T) {
	if dmCap(20) != slack.DMMinMessages || dmCap(80) != 80 {
		t.Fatalf("dmCap: %d %d", dmCap(20), dmCap(80))
	}
	if got := readMoreHint("C1", "secondary", true); got != "get_channel_digest channel=C1 workspace=secondary reads the rest" {
		t.Fatalf("multi-workspace hint = %q", got)
	}
	if got := readMoreHint("C1", "primary", false); got != "get_channel_digest channel=C1 reads the rest" {
		t.Fatalf("single-workspace hint = %q", got)
	}
	if readMoreHint("", "x", true) != "" {
		t.Fatal("no channel, no hint")
	}
}
