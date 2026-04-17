// Package format renders Slack data into compact, LLM-friendly text.
//
// Output is optimised for token efficiency:
//   - One message per line where possible.
//   - Truncation markers with exact counts ("+127 chars", "+5 more").
//   - Empty/zero fields omitted.
//   - Stable field order so cached prompts stay cached.
package format

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
)

// MessageLineLimit caps a single message body before truncation.
const MessageLineLimit = 280

// ThreadPreviewReplies is the max replies we inline in a digest.
const ThreadPreviewReplies = 3

// ParseTS converts a Slack "1234567890.123456" timestamp to time.Time.
func ParseTS(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	// Strip sub-second precision; Slack formats as "<sec>.<usec>".
	parts := strings.SplitN(ts, ".", 2)
	sec, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// MessageLine renders one message as a single compact line:
//
//	[HH:MM alex] message body (+127 chars) :thumbsup:(3) (5 replies)
func MessageLine(msg goslack.Message, userName string) string {
	var b strings.Builder
	b.Grow(64 + len(msg.Text))

	t := ParseTS(msg.Timestamp)
	b.WriteByte('[')
	if !t.IsZero() {
		b.WriteString(t.Format("15:04"))
		b.WriteByte(' ')
	}
	b.WriteString(displayName(userName, msg.User))
	b.WriteString("] ")

	body := collapseWhitespace(msg.Text)
	if len(body) > MessageLineLimit {
		over := len(body) - MessageLineLimit
		body = body[:MessageLineLimit]
		b.WriteString(body)
		fmt.Fprintf(&b, " (+%d chars)", over)
	} else {
		b.WriteString(body)
	}

	if rs := renderReactions(msg.Reactions); rs != "" {
		b.WriteByte(' ')
		b.WriteString(rs)
	}
	if msg.ReplyCount > 0 {
		fmt.Fprintf(&b, " (%d replies)", msg.ReplyCount)
	}
	return b.String()
}

// ChannelDigest renders all messages for a channel with a header.
//
// Reserves n messages for detailed rendering; extras collapse to "+N more".
func ChannelDigest(channelName string, messages []goslack.Message, users map[string]string, maxShow int) string {
	if len(messages) == 0 {
		return fmt.Sprintf("## #%s\n(no activity)", channelName)
	}
	if maxShow <= 0 {
		maxShow = len(messages)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## #%s (%d msgs)\n", channelName, len(messages))

	show := messages
	var hidden int
	if len(show) > maxShow {
		hidden = len(show) - maxShow
		show = show[:maxShow]
	}
	for _, m := range show {
		b.WriteString(MessageLine(m, users[m.User]))
		b.WriteByte('\n')
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "... +%d more messages\n", hidden)
	}
	return strings.TrimRight(b.String(), "\n")
}

// DecisionLine renders a single decision entry for a recap.
//
//	- #dev 2026-04-14 14:30 (alex) [approved] body preview
func DecisionLine(msg goslack.Message, channel, user, reason string) string {
	body := collapseWhitespace(msg.Text)
	if len(body) > 160 {
		body = body[:160] + "..."
	}
	t := ParseTS(msg.Timestamp)
	when := ""
	if !t.IsZero() {
		when = t.Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("- #%s %s (%s) [%s] %s", channel, when, user, reason, body)
}

// SearchResult renders a single search hit as a compact line.
func SearchResult(m goslack.SearchMessage) string {
	body := collapseWhitespace(m.Text)
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	t := ParseTS(m.Timestamp)
	when := ""
	if !t.IsZero() {
		when = t.Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("- #%s %s (%s) %s", m.Channel.Name, when, m.Username, body)
}

func displayName(name, fallback string) string {
	if name != "" {
		return name
	}
	if fallback != "" {
		return fallback
	}
	return "?"
}

func collapseWhitespace(s string) string {
	s = strings.TrimSpace(s)
	if !strings.ContainsAny(s, "\n\t  ") {
		return s
	}
	// Replace all whitespace runs with single spaces.
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r == '\n' || r == '\t' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		if r == ' ' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

func renderReactions(rs []goslack.ItemReaction) string {
	if len(rs) == 0 {
		return ""
	}
	var parts []string
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf(":%s:(%d)", r.Name, r.Count))
	}
	return strings.Join(parts, " ")
}
