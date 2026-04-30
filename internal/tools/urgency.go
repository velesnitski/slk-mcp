package tools

import (
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// urgencyKeywords are substrings (matched case-insensitively) that
// bump a message's urgency score. Spans English and Russian since the
// primary workspace is bilingual.
//
// Entries must NOT be substrings of each other — that would
// double-count one literal hit. "помоги" is kept; "помогите" omitted
// because it already contains "помоги" and would match twice.
var urgencyKeywords = []string{
	// English
	"urgent", "asap", "blocker", "critical", "important", "stuck",
	// Russian — verb stems / imperatives that don't overlap.
	"срочно", "критично", "блокер", "помоги",
	"сломалось", "упало", "блокирует", "не работает", "горит",
}

// urgencyReactions are reaction names typically used to flag
// importance or breakage.
var urgencyReactions = map[string]bool{
	"rotating_light": true,
	"siren":          true,
	"fire":           true,
	"warning":        true,
	"exclamation":    true,
	"bangbang":       true,
	"x":              true,
	"no_entry":       true,
}

// Per-signal weights. Tuned so a single channel-mention (1_000_000 in
// rankUnread) always dominates urgency, but urgency clearly outranks
// raw message count between non-mention channels.
const (
	urgencyQuestionWeight = 2 // per "?" mark, capped per message
	urgencyQuestionCap    = 3 // max question marks counted per message

	urgencyKeywordWeight = 10 // per unique keyword hit per message
	urgencyReactionWeight = 3 // per matching reaction

	urgencyRecentBonus  = 5 // age < 1h
	urgencyFreshBonus   = 2 // age < 6h
	urgencyRecentWindow = time.Hour
	urgencyFreshWindow  = 6 * time.Hour
)

// urgencyScore returns a heuristic rank bonus for a ChannelUnread,
// summing per-message signals across both top-level messages and
// thread replies. Independent of volume — added alongside the volume
// score in rankUnread.
//
// `now` is injected (rather than calling time.Now internally) so
// tests can pin recency bands deterministically. Pass time.Time{} to
// disable recency entirely.
func urgencyScore(cu *slack.ChannelUnread, now time.Time) int {
	score := 0
	for _, m := range cu.Messages {
		score += messageUrgency(m, now)
	}
	for _, rs := range cu.Replies {
		for _, r := range rs {
			score += messageUrgency(r, now)
		}
	}
	return score
}

// messageUrgency scores a single message. Public-friendly internals
// stay package-private — callers should always go through urgencyScore.
func messageUrgency(m goslack.Message, now time.Time) int {
	score := 0

	// Question marks. Count both ASCII and full-width (CJK / RU
	// keyboards sometimes produce '？'). Capped to prevent a single
	// flame message from dominating.
	qm := strings.Count(m.Text, "?") + strings.Count(m.Text, "？")
	if qm > urgencyQuestionCap {
		qm = urgencyQuestionCap
	}
	score += qm * urgencyQuestionWeight

	// Urgency keywords (en + ru). One bonus per *unique* keyword
	// hit per message — repeating "срочно срочно срочно" doesn't
	// triple-score, but two distinct keywords ("urgent" + "blocker")
	// do.
	lower := strings.ToLower(m.Text)
	for _, kw := range urgencyKeywords {
		if strings.Contains(lower, kw) {
			score += urgencyKeywordWeight
		}
	}

	// Reactions
	for _, r := range m.Reactions {
		if urgencyReactions[r.Name] {
			score += urgencyReactionWeight
		}
	}

	// Recency
	if !now.IsZero() {
		if t := format.ParseTS(m.Timestamp); !t.IsZero() {
			age := now.Sub(t)
			switch {
			case age >= 0 && age < urgencyRecentWindow:
				score += urgencyRecentBonus
			case age >= 0 && age < urgencyFreshWindow:
				score += urgencyFreshBonus
			}
		}
	}

	return score
}
