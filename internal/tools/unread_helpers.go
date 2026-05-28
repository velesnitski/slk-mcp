// Package tools — unread helpers split out of unread.go to keep that
// file focused on MCP handler registration. These functions are all
// internal to the package and exercised via the handlers above.
package tools

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/digest"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func filterEmptyMentions(matches []goslack.SearchMessage) []goslack.SearchMessage {
	out := matches[:0]
	for _, m := range matches {
		if strings.TrimSpace(m.Text) != "" {
			out = append(out, m)
		}
	}
	return out
}

// closingAckRe matches short conversation-closing acknowledgements
// in en + ru. Anchored to whole-trimmed-body so partial matches in
// longer messages are not affected.
var closingAckRe = regexp.MustCompile(`(?i)^(?:thanks|thank you|thx|ok|okay|got it|spasibo|spasiba|спасибо|спасиб|пасиб|ок|окей|\+1|👍|:thumbsup:|:\+1:|np|nice|great|ack|done)[!.)\s]*$`)

func filterClosingAcks(matches []goslack.SearchMessage) []goslack.SearchMessage {
	out := matches[:0]
	for _, m := range matches {
		if !closingAckRe.MatchString(strings.TrimSpace(m.Text)) {
			out = append(out, m)
		}
	}
	return out
}

// filterStrictMentions removes matches that don't literally tag the
// operator via <@SELFID> in the body. Slack's `to:me` search
// occasionally surfaces channel-wide messages where you're a member
// but were never directly mentioned; this filter rejects those.
func filterStrictMentions(matches []goslack.SearchMessage, selfID string) []goslack.SearchMessage {
	needle := "<@" + selfID + ">"
	prefixed := "<@" + selfID + "|"
	out := matches[:0]
	for _, m := range matches {
		if strings.Contains(m.Text, needle) || strings.Contains(m.Text, prefixed) {
			out = append(out, m)
		}
	}
	return out
}

// writeContextLines renders prior/subsequent context messages with
// two filters: (1) skip messages already shown for this channel
// (dedup across consecutive same-channel mentions), and (2) skip
// messages with no signal (HasContent == false) so empty Slackbot
// pings and reaction-only entries don't waste tokens.
func writeContextLines(b *strings.Builder, prefix string, msgs []goslack.Message, users map[string]string, channelID string, shown map[string]struct{}) {
	for _, p := range msgs {
		key := channelID + "|" + p.Timestamp
		if _, ok := shown[key]; ok {
			continue
		}
		shown[key] = struct{}{}
		if !format.HasContent(p) {
			continue
		}
		b.WriteString(prefix)
		b.WriteString(format.MessageLine(p, users[p.User], users))
		b.WriteByte('\n')
	}
}

// filterPendingMentions keeps only mentions where the operator
// (selfID) hasn't posted a text reply in the same channel after the
// mention timestamp. Reactions and empty messages do NOT count as
// replies. One conversations.history call per match (4-worker pool).
func (h *Hub) filterPendingMentions(ctx context.Context, matches []goslack.SearchMessage, selfID string) []goslack.SearchMessage {
	const workers = 4
	type job struct {
		idx   int
		match goslack.SearchMessage
	}
	type result struct {
		idx     int
		pending bool
	}

	jobs := make(chan job, len(matches))
	results := make(chan result, len(matches))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				results <- result{idx: j.idx, pending: !h.operatorReplied(ctx, j.match, selfID)}
			}
		}()
	}
	for i, m := range matches {
		jobs <- job{idx: i, match: m}
	}
	close(jobs)
	wg.Wait()
	close(results)

	keep := make([]bool, len(matches))
	for r := range results {
		keep[r.idx] = r.pending
	}
	out := matches[:0]
	for i, m := range matches {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}

func (h *Hub) operatorReplied(ctx context.Context, m goslack.SearchMessage, selfID string) bool {
	return operatorRepliedSince(ctx, h.Messages(), h.log, m, selfID)
}

// operatorRepliedSince reports whether selfID posted a non-empty text
// message in the same conversation after `m.Timestamp`. It looks in
// two places — both are needed because conversations.history alone
// misses thread replies:
//
//  1. Top-level channel/DM history newer than the mention. Catches the
//     common case: peer pings you, you respond at the top level.
//  2. Thread replies for the relevant root. Catches two cases the
//     history sweep cannot see:
//     a. The mention is itself a thread reply, and the operator
//     replied later in the same thread (e.g. continuing a DM
//     thread). conversations.history will not return either side
//     of that exchange.
//     b. The mention is a top-level message that spawned a thread,
//     and the operator's reply landed inside that thread. Again
//     conversations.history skips the in-thread reply.
//
// The thread root is taken from the mention's permalink (`thread_ts=`)
// when available, falling back to the mention's own timestamp — the
// latter is a no-op fetch for a non-threaded top-level message
// (conversations.replies returns the parent alone), so the worst case
// is one extra API call per scanned mention. Acceptable since
// pending_only is an opt-in expensive filter already.
func operatorRepliedSince(
	ctx context.Context,
	msgs MessageClient,
	log *slog.Logger,
	m goslack.SearchMessage,
	selfID string,
) bool {
	pivot, _ := strconv.ParseFloat(m.Timestamp, 64)
	channelID := m.Channel.ID
	if pivot <= 0 || channelID == "" || selfID == "" {
		return false
	}

	hist, err := msgs.History(ctx, slack.HistoryParams{
		ChannelID: channelID,
		OldestTS:  pivot,
		Limit:     20,
	})
	if err != nil {
		log.Debug("operator-reply history check failed", "channel", channelID, "err", err)
	} else if hasOperatorTextSince(hist, m.Timestamp, selfID) {
		return true
	}

	threadTS := format.ExtractThreadTS(m)
	if threadTS == "" {
		return false
	}
	replies, err := msgs.ThreadReplies(ctx, channelID, threadTS)
	if err != nil {
		log.Debug("operator-reply thread check failed",
			"channel", channelID, "thread_ts", threadTS, "err", err)
		return false
	}
	return hasOperatorTextSince(replies, m.Timestamp, selfID)
}

func hasOperatorTextSince(msgs []goslack.Message, mentionTS, selfID string) bool {
	for _, m := range msgs {
		if m.Timestamp <= mentionTS {
			continue
		}
		if m.User != selfID {
			continue
		}
		if collapseTextEmpty(m.Text) {
			continue
		}
		return true
	}
	return false
}

func collapseTextEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}

// fetchMentionContext returns up to n messages on each side of
// `pivotTS` in `channelID`, ordered oldest → newest. Both lists are
// best-effort and may be empty on error.
func (h *Hub) fetchMentionContext(ctx context.Context, channelID, pivotTS string, n int) (before, after []goslack.Message) {
	if n <= 0 {
		n = 3
	}
	hist, err := h.Messages().History(ctx, slack.HistoryParams{
		ChannelID: channelID,
		Limit:     n + 1,
	})
	if err != nil {
		h.log.Debug("fetch mention context (before) failed", "channel", channelID, "err", err)
	} else {
		for _, m := range hist {
			if m.Timestamp >= pivotTS {
				continue
			}
			before = append(before, m)
			if len(before) >= n {
				break
			}
		}
	}

	pivot, _ := strconv.ParseFloat(pivotTS, 64)
	if pivot > 0 {
		hist, err := h.Messages().History(ctx, slack.HistoryParams{
			ChannelID: channelID,
			OldestTS:  pivot,
			Limit:     n + 1,
		})
		if err != nil {
			h.log.Debug("fetch mention context (after) failed", "channel", channelID, "err", err)
		} else {
			for _, m := range hist {
				if m.Timestamp <= pivotTS {
					continue
				}
				after = append(after, m)
				if len(after) >= n {
					break
				}
			}
		}
	}

	for i, j := 0, len(before)-1; i < j; i, j = i+1, j-1 {
		before[i], before[j] = before[j], before[i]
	}
	for i, j := 0, len(after)-1; i < j; i, j = i+1, j-1 {
		after[i], after[j] = after[j], after[i]
	}
	return before, after
}

// channelDisplayLabel returns the human-friendly heading used in
// digest output:
//
//   - "#name" for public/private channels.
//   - "@peer" for direct messages (1:1) — peer ID resolved via
//     UserService for a friendly handle.
//   - the raw channel name (typically "mpdm-a--b--c-1") for
//     multi-party DMs, since Slack's mpim names already convey
//     who's in the conversation.
//
// Falls back to "#?" when nothing usable is available.
func channelDisplayLabel(ctx context.Context, ch goslack.Channel, users UserClient) string {
	switch {
	case ch.IsIM:
		if ch.User != "" {
			return "@" + users.Name(ctx, ch.User)
		}
		return "@?"
	case ch.IsMpIM:
		if ch.Name != "" {
			return ch.Name
		}
		return "mpdm-?"
	default:
		if ch.Name != "" {
			return "#" + ch.Name
		}
		return "#?"
	}
}

// rankUnread / channelMentions moved to internal/digest (rank.go).

// filterMentions returns only ChannelUnread entries that contain at
// least one direct mention of selfID, in either a top-level message
// or a thread reply. selfID must be non-empty; pass through unchanged
// if filtering should be a no-op.
func filterMentions(results []*slack.ChannelUnread, selfID string) []*slack.ChannelUnread {
	if selfID == "" {
		return results
	}
	out := results[:0]
	for _, r := range results {
		if digest.ChannelMentions(r, selfID) {
			out = append(out, r)
		}
	}
	return out
}

// resolveRefsWithReplies is the unread-channel counterpart of
// resolveRefs: gathers user IDs and channel IDs across top-level
// messages AND inlined thread replies, then merges the resolved
// names. Used by the unread-summary renderer so `<@UID>` and
// `<#CID>` references inside thread bodies render readably.
func (h *Hub) resolveRefsWithReplies(ctx context.Context, cu *slack.ChannelUnread) map[string]string {
	users := h.Users().NamesFor(ctx, collectUserIDsWithReplies(cu))
	channels := h.Channels().NamesForIDs(ctx, collectChannelIDsWithReplies(cu))
	return mergeRefs(users, channels)
}

// collectChannelIDsWithReplies mirrors collectUserIDsWithReplies for
// `<#CHANNELID>` references — scans both the top-level unread
// messages and their inlined thread replies, deduping by ID.
func collectChannelIDsWithReplies(cu *slack.ChannelUnread) []string {
	seen := make(map[string]struct{})
	var ids []string
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, id := range format.CollectMentionedChannelIDs(cu.Messages) {
		add(id)
	}
	for _, rs := range cu.Replies {
		for _, id := range format.CollectMentionedChannelIDs(rs) {
			add(id)
		}
	}
	return ids
}

// collectUserIDsWithReplies returns unique user IDs across both the
// channel's top-level unread messages and any inlined thread replies,
// PLUS any users referenced via <@USERID> mentions inside message
// bodies. Pre-resolving mentioned users makes RenderText able to
// substitute readable names instead of raw IDs.
func collectUserIDsWithReplies(cu *slack.ChannelUnread) []string {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(cu.Messages))
	add := func(uid string) {
		if uid == "" {
			return
		}
		if _, ok := seen[uid]; ok {
			return
		}
		seen[uid] = struct{}{}
		ids = append(ids, uid)
	}
	for _, m := range cu.Messages {
		add(m.User)
	}
	for _, rs := range cu.Replies {
		for _, r := range rs {
			add(r.User)
		}
	}
	for _, id := range format.CollectMentionedUserIDs(cu.Messages) {
		add(id)
	}
	for _, rs := range cu.Replies {
		for _, id := range format.CollectMentionedUserIDs(rs) {
			add(id)
		}
	}
	return ids
}
