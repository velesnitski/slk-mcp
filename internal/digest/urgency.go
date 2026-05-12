package digest

import (
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// urgencyKeywords are substrings (matched case-insensitively) that
// bump a message's urgency score. Spans English (human chat + log /
// alert channel severity terms) and Russian since the primary
// workspace is bilingual and many bot-driven channels (zabbix, gitlab,
// harbor, aws) speak only English.
//
// Entries must NOT be substrings of each other — that would
// double-count one literal hit. Examples avoided for that reason:
//   - "fail" is omitted; "failed" / "failure" cover the cases that
//     matter.
//   - "помогите" is omitted; "помоги" is already a substring of it.
//   - "down" is omitted; it would match "downloaded" / "downstream"
//     / "markdown" and produce too many false positives.
var urgencyKeywords = []string{
	// English — human chat
	"urgent", "asap", "blocker", "critical", "important", "stuck",
	// English — log / alert severity (monitoring, ci, registry, cloud)
	"error", "errors", "failed", "failure", "fatal",
	"alert", "exception", "panic", "outage", "timed out",
	// Russian — verb stems / imperatives that don't overlap.
	"срочно", "критично", "блокер", "помоги",
	"сломалось", "упало", "блокирует", "не работает", "горит",
	"не отвечает",
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

// UrgencyOpts tunes the urgency heuristic per call. Zero value means
// "use defaults" — Weight 1.0, no extra keywords beyond the built-in
// en + ru list.
type UrgencyOpts struct {
	// Weight scales the raw urgency score before it is folded into
	// rankUnread. 0 means "use default" (1.0). Pass values like 0.5
	// to dampen urgency or 2.0 to amplify it. Negative values are
	// treated as the default.
	Weight float64

	// ExtraKeywords are additional substrings (matched
	// case-insensitively, expected pre-lowercased by the caller)
	// that also bump a message's urgency score. Additive to
	// urgencyKeywords; not a replacement.
	ExtraKeywords []string
}

// effectiveWeight returns Weight with the "0 / negative = default"
// convention applied.
func (o UrgencyOpts) effectiveWeight() float64 {
	if o.Weight <= 0 {
		return 1.0
	}
	return o.Weight
}

// UrgencyScore returns a heuristic rank bonus for a ChannelUnread,
// summing per-message signals across both top-level messages and
// thread replies. Independent of volume — added alongside the volume
// score in rankUnread.
//
// `now` is injected (rather than calling time.Now internally) so
// tests can pin recency bands deterministically. Pass time.Time{} to
// disable recency entirely.
func UrgencyScore(cu *slack.ChannelUnread, now time.Time, opts UrgencyOpts) int {
	raw := 0
	for _, m := range cu.Messages {
		raw += messageUrgency(m, now, opts)
	}
	for _, rs := range cu.Replies {
		for _, r := range rs {
			raw += messageUrgency(r, now, opts)
		}
	}
	return int(float64(raw) * opts.effectiveWeight())
}

// messageUrgency scores a single message. Public-friendly internals
// stay package-private — callers should always go through UrgencyScore.
func messageUrgency(m goslack.Message, now time.Time, opts UrgencyOpts) int {
	score := 0

	// Question marks. Count both ASCII and full-width (CJK / RU
	// keyboards sometimes produce '？'). Capped to prevent a single
	// flame message from dominating.
	qm := strings.Count(m.Text, "?") + strings.Count(m.Text, "？")
	if qm > urgencyQuestionCap {
		qm = urgencyQuestionCap
	}
	score += qm * urgencyQuestionWeight

	// Urgency keywords (built-in en + ru, plus any caller-supplied
	// extras). One bonus per *unique* keyword hit per message —
	// repeating "срочно срочно срочно" doesn't triple-score, but
	// two distinct keywords ("urgent" + "blocker") do.
	lower := strings.ToLower(m.Text)
	for _, kw := range urgencyKeywords {
		if strings.Contains(lower, kw) {
			score += urgencyKeywordWeight
		}
	}
	for _, kw := range opts.ExtraKeywords {
		if kw == "" {
			continue
		}
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

// ParseExtraKeywords converts a comma-separated MCP arg into a clean,
// lowercased keyword list. Empty / whitespace-only entries are
// dropped. Used by tools/unread.go to feed urgency_keywords into
// UrgencyOpts.
func ParseExtraKeywords(arg string) []string {
	if arg == "" {
		return nil
	}
	parts := strings.Split(arg, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		kw := strings.ToLower(strings.TrimSpace(p))
		if kw == "" {
			continue
		}
		out = append(out, kw)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
