package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

func chBudgetUnread(msgs ...goslack.Message) *slack.ChannelUnread {
	var ch goslack.Channel
	ch.ID = "C1"
	ch.Name = "alpha"
	return &slack.ChannelUnread{Channel: ch, Messages: msgs}
}

func TestChannelPreviewPick(t *testing.T) {
	self := "U1"
	bot := uaMsgOf("1700000500.000000", "U9", "deploy finished")
	bot.BotID = "B1"
	join := uaMsgOf("1700000400.000000", "U3", "has joined the channel")
	join.SubType = "channel_join"

	// Newest by someone else wins over the operator's own newer posts,
	// bot posts and joins.
	r := chBudgetUnread(
		uaMsgOf("1700000600.000000", self, "own status post"),
		bot, join,
		uaMsgOf("1700000300.000000", "U2", "an idea for a test stand"),
		uaMsgOf("1700000100.000000", "U3", "older note"),
	)
	m, mention := channelPreviewPick(r, self)
	if m == nil || m.Text != "an idea for a test stand" || mention {
		t.Fatalf("pick = %+v mention=%v, want the newest human line by someone else", m, mention)
	}

	// A thread reply newer than every top-level message is picked.
	r.Replies = map[string][]goslack.Message{"1700000100.000000": {uaMsgOf("1700000350.000000", "U3", "reply in thread")}}
	if m, _ := channelPreviewPick(r, self); m == nil || m.Text != "reply in thread" {
		t.Fatalf("pick = %+v, want the newer thread reply", m)
	}

	// A mention of the operator outranks a newer line that is not.
	r.Messages = append(r.Messages, uaMsgOf("1700000050.000000", "U4", "<@U1> can you check this"))
	m, mention = channelPreviewPick(r, self)
	if m == nil || !strings.Contains(m.Text, "can you check") || !mention {
		t.Fatalf("pick = %+v mention=%v, want the mention", m, mention)
	}

	// Only the operator and bots wrote: nothing to preview.
	if m, _ := channelPreviewPick(chBudgetUnread(uaMsgOf("1700000600.000000", self, "own"), bot), self); m != nil {
		t.Fatalf("pick = %+v, want none", m)
	}
}

func TestChannelPreviewLine_CutsAndCarriesVolume(t *testing.T) {
	r := chBudgetUnread(uaMsgOf("1700000300.000000", "U2", strings.Repeat("long idea text ", 30)))
	line, ok := channelPreviewLine("#alpha", r, map[string]string{"U2": "Sam"}, "U1")
	if !ok || !strings.Contains(line, "#alpha (1): ") || !strings.Contains(line, "Sam") || !strings.HasSuffix(line, "…") {
		t.Fatalf("line = %q", line)
	}
	if n := len([]rune(line)); n > channelPreviewLimit+40 {
		t.Fatalf("line is %d runes, want it cut near %d", n, channelPreviewLimit)
	}
}

// The case that went wrong live: a channel of the operator's own status
// posts held one idea from a colleague, and the budget left it a count.
// Capped channels with a human line keep it, up to channelPreviewLines;
// the rest – and channels only the operator wrote in – stay counts.
func TestRunUnreadSummary_CappedChannelsKeepAPreview(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U2": "Sam"})

	var channels []map[string]any
	infos := map[string]map[string]any{}
	history := map[string][]map[string]any{}
	n := channelPreviewLines + 3
	for i := 0; i < n; i++ {
		id, name := fmt.Sprintf("C%d", i+1), fmt.Sprintf("chan%02d", i+1)
		channels = append(channels, uaChannel(id, name, nil))
		infos[id] = uaChannel(id, name, map[string]any{"last_read": "1700000000.000000"})
		history[id] = []map[string]any{
			uaMsg(uaTS(int64(-60-i), 100), "U2", fmt.Sprintf("idea number %d ", i+1)+strings.Repeat("padding ", 20), nil),
		}
	}
	// One more channel holds only the operator's own posts.
	channels = append(channels, uaChannel("C99", "ownposts", nil))
	infos["C99"] = uaChannel("C99", "ownposts", map[string]any{"last_read": "1700000000.000000"})
	history["C99"] = []map[string]any{uaMsg(uaTS(-200, 100), "U1", "status: all quiet "+strings.Repeat("x", 200), nil)}

	f.uaConversations(t, channels...)
	f.uaInfo(t, infos)
	f.uaHistory(t, history)
	h := uaHub(t, f)

	p := uaUnreadDefaults()
	p.maxChars = 300
	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if got := strings.Count(out, "padding"); got < channelPreviewLines {
		t.Fatalf("want at least %d capped channels previewed, got %d:\n%s", channelPreviewLines, got, out)
	}
	if !strings.Contains(out, "newest line from someone else:") {
		t.Fatalf("missing preview block:\n%s", out)
	}
	if !strings.Contains(out, "more channels omitted by max_chars cap, with unread counts:") {
		t.Fatalf("channels past the preview limit must stay counts:\n%s", out)
	}
	if strings.Contains(out, "status: all quiet") {
		t.Fatalf("the operator's own post must not stand for a channel:\n%s", out)
	}
	if !strings.Contains(out, "ownposts (1)") {
		t.Fatalf("a channel only the operator wrote in stays a count:\n%s", out)
	}
}
