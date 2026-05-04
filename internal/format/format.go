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

// LogPattern is a deduped group of similar messages from one
// severity band. Sample is the most-recent representative of the
// group; Count is the total membership including Sample. Signature
// is the canonical form used for grouping (kept on the struct mostly
// so tests can assert on it).
type LogPattern struct {
	Sample    goslack.Message
	Count     int
	Signature string
}

// LogBand is one severity slice rendered by LogChannelDigest. Two
// modes:
//
//   - Patterns is preferred — populated by callers that pre-deduped
//     the band. Each pattern renders one line with a "(×N similar)"
//     suffix when Count > 1.
//   - Samples is the legacy field used by callers that haven't
//     deduped. Renders one line per message. Used when Patterns is
//     empty.
//
// Total is the full membership of the band, used for the histogram
// header and overflow ("+N other") lines.
type LogBand struct {
	Label    string
	Total    int
	Samples  []goslack.Message
	Patterns []LogPattern
}

// LogChannelDigest renders a bot-driven log / alert channel as a
// severity histogram followed by per-band sample listings, in
// dominance order (caller-chosen). Empty bands are omitted from
// both the histogram and the body.
//
// channelLabel is used as the heading verbatim — caller is
// responsible for any "#"/"@" prefix. Total is the full unread
// count (so the header reflects the entire channel even when most
// messages are histogram-only). Pass an empty users map if user
// resolution failed — the underlying MessageLine renderer falls
// back to user IDs.
func LogChannelDigest(channelLabel string, total int, bands []LogBand, users map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s [LOG MODE — %d msgs]\n", channelLabel, total)

	var hist []string
	for _, band := range bands {
		if band.Total == 0 {
			continue
		}
		hist = append(hist, fmt.Sprintf("%s=%d", band.Label, band.Total))
	}
	if len(hist) == 0 {
		b.WriteString("severity: (no classified messages)\n")
	} else {
		fmt.Fprintf(&b, "severity: %s\n", strings.Join(hist, " "))
	}

	for _, band := range bands {
		switch {
		case len(band.Patterns) > 0:
			nonEmpty := band.Patterns[:0]
			for _, p := range band.Patterns {
				if HasContent(p.Sample) {
					nonEmpty = append(nonEmpty, p)
				}
			}
			if len(nonEmpty) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\nrecent %s:\n", band.Label)
			rendered := 0
			for _, p := range nonEmpty {
				b.WriteString("  ")
				b.WriteString(MessageLine(p.Sample, users[p.Sample.User]))
				if p.Count > 1 {
					fmt.Fprintf(&b, " (×%d similar)", p.Count)
				}
				b.WriteByte('\n')
				rendered += p.Count
			}
			if hidden := band.Total - rendered; hidden > 0 {
				fmt.Fprintf(&b, "  ... +%d other\n", hidden)
			}

		case len(band.Samples) > 0:
			fmt.Fprintf(&b, "\nrecent %s:\n", band.Label)
			for _, m := range band.Samples {
				b.WriteString("  ")
				b.WriteString(MessageLine(m, users[m.User]))
				b.WriteByte('\n')
			}
			if hidden := band.Total - len(band.Samples); hidden > 0 {
				fmt.Fprintf(&b, "  ... +%d more\n", hidden)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ThreadPreviewReplies is the max replies we inline in a digest.
const ThreadPreviewReplies = 3

// MentionMarker prefixes messages that mention the authenticated user
// in a channel digest. Chosen to be conspicuous to LLMs without
// disturbing humans skim-reading the output.
const MentionMarker = "🏷️ "

// ReplyIndent prefixes inlined thread replies under their parent.
const ReplyIndent = "    ↳ "

// digestOpts holds optional behaviour for ChannelDigest, populated via
// DigestOption functions so existing callers stay source-compatible.
type digestOpts struct {
	selfID            string
	replies           map[string][]goslack.Message
	threadPreviewCap  int // 0 means use ThreadPreviewReplies default
}

// DigestOption configures ChannelDigest output.
type DigestOption func(*digestOpts)

// WithMentionHighlight prepends MentionMarker to messages whose body
// contains "<@selfID>". Pass an empty string to disable.
func WithMentionHighlight(selfID string) DigestOption {
	return func(o *digestOpts) { o.selfID = selfID }
}

// WithThreadReplies attaches reply chains to the digest, keyed by
// thread_ts of the parent message. Replies are rendered indented
// beneath their parent. Up to ThreadPreviewReplies are shown per
// thread by default (see WithThreadPreviewReplies to override); the
// rest collapse to "+N more replies".
func WithThreadReplies(replies map[string][]goslack.Message) DigestOption {
	return func(o *digestOpts) { o.replies = replies }
}

// WithThreadPreviewReplies overrides the per-thread inline-reply cap
// for this call. Pass <= 0 to fall back to the ThreadPreviewReplies
// default.
func WithThreadPreviewReplies(n int) DigestOption {
	return func(o *digestOpts) { o.threadPreviewCap = n }
}

// MentionsUser reports whether msg.Text contains a Slack-style
// "<@userID>" mention of userID. Returns false for empty userID.
func MentionsUser(msg goslack.Message, userID string) bool {
	if userID == "" {
		return false
	}
	return strings.Contains(msg.Text, "<@"+userID+">")
}

// HasContent reports whether a message carries any signal worth
// rendering — non-empty body, any reaction, or any thread reply.
// Used to filter out empty Slackbot / webhook pings.
func HasContent(msg goslack.Message) bool {
	if collapseWhitespace(msg.Text) != "" {
		return true
	}
	if len(msg.Reactions) > 0 {
		return true
	}
	if msg.ReplyCount > 0 {
		return true
	}
	return false
}

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
// channelLabel is the heading verbatim — caller picks the prefix
// ("#general" for channels, "@alex" for DMs, "mpdm-..." for group
// DMs). Reserves maxShow messages for detailed rendering; extras
// collapse to "+N more". Optional behaviour (mention highlighting,
// thread replies) is configured via DigestOption.
func ChannelDigest(channelLabel string, messages []goslack.Message, users map[string]string, maxShow int, opts ...DigestOption) string {
	cfg := digestOpts{}
	for _, opt := range opts {
		opt(&cfg)
	}

	filtered := messages[:0]
	for _, m := range messages {
		if HasContent(m) {
			filtered = append(filtered, m)
		}
	}
	messages = filtered

	if len(messages) == 0 && len(cfg.replies) == 0 {
		return ""
	}
	if len(messages) == 0 {
		return fmt.Sprintf("## %s\n(no activity)", channelLabel)
	}
	if maxShow <= 0 {
		maxShow = len(messages)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s (%d msgs)\n", channelLabel, len(messages))

	show := messages
	var hidden int
	if len(show) > maxShow {
		hidden = len(show) - maxShow
		show = show[:maxShow]
	}
	for _, m := range show {
		if MentionsUser(m, cfg.selfID) {
			b.WriteString(MentionMarker)
		}
		b.WriteString(MessageLine(m, users[m.User]))
		b.WriteByte('\n')

		if replies, ok := cfg.replies[m.Timestamp]; ok && len(replies) > 0 {
			writeReplies(&b, replies, users, cfg.selfID, cfg.threadPreviewCap)
		}
	}
	if hidden > 0 {
		fmt.Fprintf(&b, "... +%d more messages\n", hidden)
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeReplies renders replies indented under the thread parent, up
// to a cap (cap <= 0 means use ThreadPreviewReplies default). Mentions
// are highlighted using selfID just like the parent message.
func writeReplies(b *strings.Builder, replies []goslack.Message, users map[string]string, selfID string, cap int) {
	if cap <= 0 {
		cap = ThreadPreviewReplies
	}
	show := replies
	var hidden int
	if len(show) > cap {
		hidden = len(show) - cap
		show = show[:cap]
	}
	for _, r := range show {
		b.WriteString(ReplyIndent)
		if MentionsUser(r, selfID) {
			b.WriteString(MentionMarker)
		}
		b.WriteString(MessageLine(r, users[r.User]))
		b.WriteByte('\n')
	}
	if hidden > 0 {
		fmt.Fprintf(b, "%s+%d more replies\n", ReplyIndent, hidden)
	}
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
