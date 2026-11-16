package tools

import (
	"strings"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// channelPreviewLines is how many channels cut by max_chars keep a
// one-line preview, in rank order; the rest stay a name and a count.
// channelPreviewLimit caps each line. Together they bound the overflow
// block at roughly 1.5k characters however long the tail is (ADR 148).
const (
	channelPreviewLines = 8
	channelPreviewLimit = 160
)

// channelPreviewPick is the message a channel cut by max_chars is
// previewed by: the newest one by a person other than the operator,
// among top-level messages and thread replies – and, when someone
// @-mentions the operator, the newest such mention instead. Bot posts,
// joins and the operator's own lines never stand for the channel: the
// question is whether anyone else said something worth opening it for.
// mention reports whether the pick names the operator. Pure.
func channelPreviewPick(r *slack.ChannelUnread, selfID string) (pick *goslack.Message, mention bool) {
	human := func(m goslack.Message) bool {
		if m.User == "" || m.User == selfID || m.BotID != "" {
			return false
		}
		switch m.SubType {
		case "", "thread_broadcast", "file_share":
		default:
			return false // joins, bot_message, channel events
		}
		return format.HasContent(m)
	}
	newer := func(cur *goslack.Message, m goslack.Message) bool {
		return cur == nil || tsAfter(m.Timestamp, cur.Timestamp)
	}
	var newest, newestMention *goslack.Message
	consider := func(m goslack.Message) {
		if !human(m) {
			return
		}
		if newer(newest, m) {
			mm := m
			newest = &mm
		}
		if selfID != "" && strings.Contains(m.Text, "<@"+selfID) && newer(newestMention, m) {
			mm := m
			newestMention = &mm
		}
	}
	for _, m := range r.Messages {
		consider(m)
	}
	for _, rs := range r.Replies {
		for _, m := range rs {
			consider(m)
		}
	}
	if newestMention != nil {
		return newestMention, true
	}
	return newest, false
}

// channelPreviewLine renders a channel cut by max_chars as one line: its
// label and volume, then the pick, cut to channelPreviewLimit. ok is
// false when nobody but the operator or a bot wrote there – such a
// channel stays a count in the footer. Pure.
func channelPreviewLine(label string, r *slack.ChannelUnread, users map[string]string, selfID string) (string, bool) {
	m, mention := channelPreviewPick(r, selfID)
	if m == nil {
		return "", false
	}
	line := format.MessageLine(*m, users[m.User], users)
	line = strings.Join(strings.Fields(line), " ")
	if runes := []rune(line); len(runes) > channelPreviewLimit {
		line = string(runes[:channelPreviewLimit]) + "…"
	}
	mark := ""
	if mention {
		mark = " · mentions you"
	}
	return "  " + label + " (" + omittedCount(r) + mark + "): " + line, true
}
