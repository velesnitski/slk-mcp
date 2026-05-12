package digest

import (
	"time"

	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// RankUnread orders ChannelUnread results. The components, in order
// of dominance:
//
//  1. A direct mention of selfID — adds 1_000_000, so any mention
//     beats any volume or urgency from non-mentioning channels.
//  2. Urgency heuristic — keywords ("urgent" / "срочно"), question
//     marks, urgency reactions, recency. See urgency.go.
//     Tunable per call via UrgencyOpts (weight + extra keywords).
//  3. Raw volume — total unread messages + replies.
//
// `now` is injected so tests can fix recency. Pass time.Time{} to
// disable the recency component.
func RankUnread(cu *slack.ChannelUnread, selfID string, now time.Time, urg UrgencyOpts) int {
	rank := len(cu.Messages)
	for _, rs := range cu.Replies {
		rank += len(rs)
	}
	rank += UrgencyScore(cu, now, urg)
	if ChannelMentions(cu, selfID) {
		rank += 1_000_000
	}
	return rank
}

// ChannelMentions reports whether any top-level message OR inlined
// thread reply in cu contains a `<@selfID>` mention. Empty selfID is
// treated as "no mentions can match", returning false — never errors,
// never panics on nil-replies maps.
func ChannelMentions(cu *slack.ChannelUnread, selfID string) bool {
	if selfID == "" {
		return false
	}
	for _, m := range cu.Messages {
		if format.MentionsUser(m, selfID) {
			return true
		}
	}
	for _, rs := range cu.Replies {
		for _, r := range rs {
			if format.MentionsUser(r, selfID) {
				return true
			}
		}
	}
	return false
}
