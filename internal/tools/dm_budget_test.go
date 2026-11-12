package tools

import (
	"context"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

func dmBudgetUnread(lastRead string, msgs ...goslack.Message) *slack.ChannelUnread {
	var ch goslack.Channel
	ch.ID = "D1"
	ch.IsIM = true
	return &slack.ChannelUnread{Channel: ch, LastRead: lastRead, Messages: msgs}
}

func TestDMPending(t *testing.T) {
	self := "U1"
	cases := []struct {
		name string
		r    *slack.ChannelUnread
		want bool
	}{
		{"new from the other side", dmBudgetUnread("1700000000.000000", uaMsgOf("1700000100.000000", "U2", "hi")), true},
		{"other side read already", dmBudgetUnread("1700000200.000000", uaMsgOf("1700000100.000000", "U2", "hi")), false},
		{"only own messages newer", dmBudgetUnread("1700000000.000000", uaMsgOf("1700000100.000000", self, "ping")), false},
		{"no last_read, message from other", dmBudgetUnread("", uaMsgOf("1700000100.000000", "U2", "hi")), true},
	}
	for _, c := range cases {
		if got := dmPending(c.r, self); got != c.want {
			t.Errorf("%s: dmPending = %v, want %v", c.name, got, c.want)
		}
	}

	// A new thread reply from the other side counts too.
	r := dmBudgetUnread("1700000200.000000", uaMsgOf("1700000100.000000", self, "root"))
	r.Replies = map[string][]goslack.Message{"1700000100.000000": {uaMsgOf("1700000300.000000", "U2", "reply")}}
	if !dmPending(r, self) {
		t.Error("a new reply from the other side must make the DM pending")
	}

	// A channel is never a pending DM.
	ch := dmBudgetUnread("", uaMsgOf("1700000100.000000", "U2", "hi"))
	ch.Channel.IsIM = false
	ch.Channel.ID = "C1"
	if dmPending(ch, self) {
		t.Error("a channel must not count as a pending DM")
	}
}

func TestDMOverflowLine_ShowsNewestFromOtherSideCutAndHint(t *testing.T) {
	long := strings.Repeat("report for you only ", 30)
	r := dmBudgetUnread("1700000000.000000",
		uaMsgOf("1700000300.000000", "U1", "my own newest line"),
		uaMsgOf("1700000200.000000", "U2", long),
	)
	out := dmOverflowLine("@Sam", r, map[string]string{"U2": "Sam"}, "U1", "get_channel_digest channel=D1")

	if !strings.Contains(out, "report for you only") {
		t.Fatalf("the other side's newest message must be shown:\n%s", out)
	}
	if strings.Contains(out, "my own newest line") {
		t.Fatalf("the operator's own line must not stand in for the other side's:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Fatalf("a long message must be cut:\n%s", out)
	}
	if !strings.Contains(out, "cut by max_chars") || !strings.Contains(out, "read: get_channel_digest channel=D1") {
		t.Fatalf("the line must say it was cut and how to read the rest:\n%s", out)
	}
}

// The case that went wrong live: one long DM spends the budget, and a
// short DM from someone else is left as a name and a count. Every DM
// must keep its text (ADR 147).
func TestRunUnreadSummary_MaxCharsKeepsEveryDMText(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam", "U3": "Pat"})

	f.uaConversations(t,
		uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2"}),
		uaChannel("D2", "", map[string]any{"is_im": true, "user": "U3"}),
	)
	f.uaInfo(t, map[string]map[string]any{
		"D1": uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2", "last_read": "1700000000.000000"}),
		"D2": uaChannel("D2", "", map[string]any{"is_im": true, "user": "U3", "last_read": "1700000000.000000"}),
	})
	var long []map[string]any
	for i := 0; i < 12; i++ {
		long = append(long, uaMsg(uaTS(int64(-100-i), 100), "U2", strings.Repeat("long thread text ", 10), nil))
	}
	f.uaHistory(t, map[string][]map[string]any{
		"D1": long,
		"D2": {uaMsg(uaTS(-50, 100), "U3", "review report attached, for you only", nil)},
	})
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.maxChars = 600
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if !strings.Contains(out, "review report attached") {
		t.Fatalf("a DM cut by the budget must keep its newest message:\n%s", out)
	}
	if !strings.Contains(out, "cut by max_chars") {
		t.Fatalf("the budget must have cut one DM to its one line:\n%s", out)
	}
	if strings.Contains(out, "channels omitted by max_chars cap") {
		t.Fatalf("a DM must not be reduced to a footer count:\n%s", out)
	}
}
