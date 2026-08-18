package digest

import (
	"time"

	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// Rank tiers. Each dominates everything below it. The gap to the
// urgency/volume band (realistically < ~100k even for a very noisy
// 200-message log channel) is wide enough that no amount of volume
// or keyword spam can promote a non-mention channel into a tier.
const (
	// mentionBonus: a direct <@selfID> mention in any channel —
	// always the top tier. A real ping outranks everything.
	mentionBonus = 1_000_000

	// dmBonus: a 1:1 or multi-party DM. DMs sit below explicit
	// mentions but above every non-mention channel, so under a
	// max_chars cap a personal DM is never dropped in favour of a
	// high-volume log/git feed. A DM that also mentions you stacks
	// both tiers (1.5M) and stays at the very top. See ADR 020.
	dmBonus = 500_000

	// feedPenalty: a bot-driven log / git / low-signal feed. Its two
	// loudest rank signals -- raw volume and error-shaped keywords --
	// are what such a channel is MADE of, not evidence that it matters:
	// a feed of 50 identical "[attached: 1]" lines outranked every
	// human channel in the workspace and evicted them under the
	// max_chars cap. So a feed gets a tier of its own BELOW every
	// ordinary channel, symmetric to dmBonus above them, and wide
	// enough that no amount of volume climbs back out.
	//
	// The tiers still stack: a feed that @-mentions you keeps
	// mentionBonus and stays visible, just under a human mention.
	feedPenalty = 250_000
)

// RankUnread orders ChannelUnread results. The components, in order
// of dominance:
//
//  1. A direct mention of selfID — adds mentionBonus, so any mention
//     beats any volume or urgency from non-mentioning channels.
//  2. A direct message (1:1 / mpdm) — adds dmBonus, so DMs outrank
//     every non-mention channel (including log- and git-mode feeds)
//     and survive the max_chars cap ahead of them.
//  3. Urgency heuristic — keywords ("urgent" / "срочно"), question
//     marks, urgency reactions, recency. See urgency.go.
//     Tunable per call via UrgencyOpts (weight + extra keywords).
//  4. Raw volume — total unread messages + replies.
//  5. A bot feed (log / git / low-signal) — subtracts feedPenalty, so
//     bot volume can never evict a human channel under max_chars.
//
// `now` is injected so tests can fix recency. Pass time.Time{} to
// disable the recency component.
func RankUnread(cu *slack.ChannelUnread, selfID string, now time.Time, urg UrgencyOpts) int {
	rank := len(cu.Messages)
	for _, rs := range cu.Replies {
		rank += len(rs)
	}
	rank += UrgencyScore(cu, now, urg)
	if slack.IsDirectMessage(cu.Channel) {
		rank += dmBonus
	}
	if ChannelMentions(cu, selfID) {
		rank += mentionBonus
	}
	if IsBotFeed(cu) {
		rank -= feedPenalty
	}
	return rank
}

// IsBotFeed reports whether a channel is machine-driven: a git/CI feed,
// a log feed, or a name-flagged low-signal channel. Grouping the three
// detectors here keeps the ranker's notion of "feed" identical to the
// renderer's, so a channel can never be collapsed to a histogram while
// still being ranked as if it were a conversation.
func IsBotFeed(cu *slack.ChannelUnread) bool {
	return DetectGitChannel(cu) || DetectLogChannel(cu) || DetectLowSignalChannel(cu)
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
