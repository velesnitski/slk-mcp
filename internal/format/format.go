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
	"regexp"
	"strconv"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
)

var (
	mentionRefRe = regexp.MustCompile(`<@([UW][A-Z0-9]+)(?:\|[^>]*)?>`)
	channelRefRe = regexp.MustCompile(`<#([CG][A-Z0-9]{6,})(?:\|([^>]*))?>`)
	linkRefRe    = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]+)>`)
	bareLinkRe   = regexp.MustCompile(`<(https?://[^>]+)>`)
)

// RenderText cleans Slack-flavoured markup in a message body for
// readable, token-efficient output:
//
//   - <@USERID>           → @Display Name (looked up in refs; falls
//     back to USERID when unknown)
//   - <#CHANNELID|name>   → #name (the inline pipe label, when present)
//   - <#CHANNELID>        → #channel-name (looked up in refs) or
//     #CHANNELID as a last-resort fallback — never
//     dropped, since the ID alone is correlatable
//   - <url|label>         → label
//   - <url>               → (dropped — caller can re-add a [link]
//     marker if needed)
//
// refs may be nil and may mix user and channel display names; Slack ID
// prefixes (U/W vs C/G) keep the namespaces distinct so a single map is
// safe. Returns text unchanged if it contains no markup.
func RenderText(text string, refs map[string]string) string {
	if text == "" {
		return ""
	}
	text = mentionRefRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := mentionRefRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		uid := sub[1]
		if name, ok := refs[uid]; ok && name != "" {
			return "@" + name
		}
		return "@" + uid
	})
	text = channelRefRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := channelRefRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		// Inline pipe label wins — Slack already resolved the name for us.
		if len(sub) >= 3 && sub[2] != "" {
			return "#" + sub[2]
		}
		cid := sub[1]
		if name, ok := refs[cid]; ok && name != "" {
			return "#" + name
		}
		// Last resort: leave the ID visible so a downstream caller (or
		// the LLM) can still correlate it instead of dropping the ref.
		return "#" + cid
	})
	text = linkRefRe.ReplaceAllString(text, "$2")
	text = bareLinkRe.ReplaceAllString(text, "")
	return text
}

// CollectMentionedUserIDs returns the unique set of <@USERID> tokens
// referenced inside the bodies of the given messages — useful for
// pre-resolving names before calling RenderText.
func CollectMentionedUserIDs(messages []goslack.Message) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range messages {
		for _, match := range mentionRefRe.FindAllStringSubmatch(m.Text, -1) {
			if len(match) < 2 {
				continue
			}
			id := match[1]
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// CollectMentionedChannelIDs returns the unique set of <#CHANNELID>
// references found in message bodies — mirrors CollectMentionedUserIDs
// and is meant to feed Channels.NamesForIDs so RenderText can resolve
// `<#CID>` to `#channel-name`.
//
// Inline-label refs (`<#CID|name>`) are still collected, even though
// RenderText doesn't need the lookup for them — callers may want to
// pre-warm the channel cache for downstream tools.
func CollectMentionedChannelIDs(messages []goslack.Message) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, m := range messages {
		for _, match := range channelRefRe.FindAllStringSubmatch(m.Text, -1) {
			if len(match) < 2 {
				continue
			}
			id := match[1]
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

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
				b.WriteString(MessageLine(p.Sample, users[p.Sample.User], users))
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
				b.WriteString(MessageLine(m, users[m.User], users))
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
	selfID           string
	replies          map[string][]goslack.Message
	threadPreviewCap int  // 0 means use ThreadPreviewReplies default
	omitEmpty        bool // return "" instead of a "(no activity)" line
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

// WithOmitEmpty makes ChannelDigest return "" instead of a
// "## label\n(no activity)" block when a channel has no displayable
// top-level messages. Callers that aggregate many channels (the
// unread sweep) pass this so a content-less channel — e.g. a DM
// pulled in by dm_window_hours with only stale thread replies — is
// dropped rather than rendered as an empty stub. Single-channel
// callers (get_channel_digest) omit it, keeping "(no activity)" as a
// useful "you asked, there's nothing here" answer. See ADR 021.
func WithOmitEmpty() DigestOption {
	return func(o *digestOpts) { o.omitEmpty = true }
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
// rendering — non-empty body, any reaction, any thread reply, file
// attachment, Block Kit content, or legacy rich attachment. Used to
// filter out empty Slackbot / webhook pings.
//
// Block Kit and Attachments matter here because some Slack clients
// (mobile, integrations) post structured content with an empty Text
// field — dropping those would silently hide a real message.
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
	if len(msg.Files) > 0 {
		return true
	}
	if len(msg.Attachments) > 0 {
		return true
	}
	if len(msg.Blocks.BlockSet) > 0 {
		return true
	}
	return false
}

// renderHiddenPayloadMarker returns a short marker describing any
// non-text payload (legacy Attachments or Block Kit Blocks) attached
// to msg. Callers gate the call on body-empty-and-files-empty —
// otherwise we'd add noise to every URL-preview message.
//
// The marker exists so MessageLine never renders an effectively
// empty line for a real message; the reader knows there is content
// reachable via the permalink even if the renderer can't surface it
// as plain text.
func renderHiddenPayloadMarker(msg goslack.Message) string {
	var parts []string
	if n := len(msg.Attachments); n > 0 {
		parts = append(parts, fmt.Sprintf("[attached: %d]", n))
	}
	if n := len(msg.Blocks.BlockSet); n > 0 {
		parts = append(parts, fmt.Sprintf("[blocks: %d]", n))
	}
	return strings.Join(parts, " ")
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
//
// allUsers is optional; when present, <@USERID> mentions and Slack
// link markup inside the body are resolved to readable form.
func MessageLine(msg goslack.Message, userName string, allUsers ...map[string]string) string {
	var users map[string]string
	if len(allUsers) > 0 {
		users = allUsers[0]
	}
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

	body := collapseWhitespace(RenderText(msg.Text, users))
	if len(body) > MessageLineLimit {
		over := len(body) - MessageLineLimit
		body = body[:MessageLineLimit]
		b.WriteString(body)
		fmt.Fprintf(&b, " (+%d chars)", over)
	} else {
		b.WriteString(body)
	}

	if files := renderFiles(msg.Files); files != "" {
		b.WriteByte(' ')
		b.WriteString(files)
	} else if body == "" {
		// Body has no text AND no file uploads — flag any remaining
		// non-text payload (Block Kit, legacy Attachments) so the
		// line isn't silently empty. URL-preview messages with text
		// stay clean because this branch only fires when body == "".
		if marker := renderHiddenPayloadMarker(msg); marker != "" {
			b.WriteString(marker)
		}
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

// renderFiles emits compact attachment markers for any files
// attached to the message. Image files use 🖼, everything else uses
// 📎. Includes filename and (for images) original dimensions when
// available.
func renderFiles(files []goslack.File) string {
	if len(files) == 0 {
		return ""
	}
	var parts []string
	for _, f := range files {
		marker := "📎"
		if strings.HasPrefix(f.Mimetype, "image/") {
			marker = "🖼"
		}
		name := f.Name
		if name == "" {
			name = f.Title
		}
		if name == "" {
			name = "file"
		}
		piece := marker + " " + name
		if f.OriginalW > 0 && f.OriginalH > 0 {
			piece += fmt.Sprintf(" (%dx%d)", f.OriginalW, f.OriginalH)
		}
		parts = append(parts, "["+piece+"]")
	}
	return strings.Join(parts, " ")
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
		if cfg.omitEmpty {
			return ""
		}
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
		b.WriteString(MessageLine(m, users[m.User], users))
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
		b.WriteString(MessageLine(r, users[r.User], users))
		b.WriteByte('\n')
	}
	if hidden > 0 {
		fmt.Fprintf(b, "%s+%d more replies\n", ReplyIndent, hidden)
	}
}

// DecisionLine renders a single decision entry for a recap.
//
//   - #dev 2026-04-14 14:30 (alex) [approved] body preview
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

// SearchResult renders a single search hit as a compact line. Body
// is collapsed to single-space and truncated to 200 chars.
func SearchResult(m goslack.SearchMessage) string {
	return searchResultLine(m, false)
}

// SearchResultExt renders a search hit with a permalink + thread_ts
// appended on a tab-indented continuation line. When fullText is true
// the body is not truncated. The continuation line lets the LLM chain
// to get_thread without re-searching.
func SearchResultExt(m goslack.SearchMessage, fullText bool) string {
	line := searchResultLine(m, fullText)
	threadTS := ExtractThreadTS(m)
	if threadTS == "" || m.Permalink == "" {
		return line
	}
	return line + "\n\tthread_ts=" + threadTS + " " + m.Permalink
}

// ThreadContextLine renders one indented context line shown beneath
// a search hit (parent or surrounding reply). Kept in format/ so the
// look-and-feel matches sibling renderers (ChannelDigest, MessageLine).
//
// `marker` is the bullet glyph: "↑" for the parent, "↳" for a
// preceding reply, "↪" for a following one. Body is collapsed and
// truncated identically to SearchResult.
func ThreadContextLine(marker string, m goslack.Message, displayName string) string {
	body := collapseWhitespace(m.Text)
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	name := displayName
	if name == "" {
		name = m.User
	}
	t := ParseTS(m.Timestamp)
	when := ""
	if !t.IsZero() {
		when = t.Format("15:04")
	}
	return "\t" + marker + " [" + when + " " + name + "] " + body
}

func searchResultLine(m goslack.SearchMessage, fullText bool) string {
	body := collapseWhitespace(m.Text)
	if !fullText && len(body) > 200 {
		body = body[:200] + "..."
	}
	t := ParseTS(m.Timestamp)
	when := ""
	if !t.IsZero() {
		when = t.Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("- #%s %s (%s) %s", m.Channel.Name, when, m.Username, body)
}

// ExtractThreadTS pulls thread_ts from a Slack permalink, falling
// back to the message's own timestamp (which is correct for
// top-level messages: thread_ts == ts when the message is the parent).
//
// Callers can compare the result against m.Timestamp to decide
// whether the hit is a thread reply (differs) or a top-level message
// / thread parent (equals).
func ExtractThreadTS(m goslack.SearchMessage) string {
	if i := strings.Index(m.Permalink, "thread_ts="); i >= 0 {
		rest := m.Permalink[i+len("thread_ts="):]
		if amp := strings.IndexByte(rest, '&'); amp >= 0 {
			return rest[:amp]
		}
		return rest
	}
	return m.Timestamp
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
