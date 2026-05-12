package digest

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
)

// signatureMaxLen caps the canonical signature so a single very-long
// message can't blow up the dedup map keys.
const signatureMaxLen = 200

var (
	// urlRegex matches http(s) URLs first (they contain digits/dots
	// that the IP/number regexes would otherwise eat).
	urlRegex = regexp.MustCompile(`https?://[^\s]+`)

	// ipRegex matches IPv4 addresses. Run BEFORE numberRegex so the
	// number regex doesn't eat individual octets first.
	ipRegex = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)

	// numberRegex matches any digit run. Captures pipeline IDs, build
	// numbers, percentages, latency values, and timestamps embedded
	// in message bodies.
	numberRegex = regexp.MustCompile(`\d+`)

	// hexIDRegex matches hex strings of length 7+ (commit shas, uuids
	// without dashes). Avoids matching short hex sequences that may
	// be part of a real word ("cafe", "deed", "face").
	hexIDRegex = regexp.MustCompile(`\b[0-9a-f]{7,}\b`)
)

// canonicalSignature normalizes a message body so similar alerts
// share a signature. Order of replacements matters:
//
//  1. Zabbix-style state prefixes ("Problem:", "Resolved in <duration>:")
//     are stripped so the same trigger flapping in/out of state
//     merges into a single signature.
//  2. URLs first — they contain digits and dots.
//  3. IPv4 addresses — stricter than bare digit runs.
//  4. Long hex IDs — after URLs (which can contain hex).
//  5. Bare digit runs.
//
// Whitespace is collapsed and the result lowercased so "FATAL: x"
// merges with "fatal: x". Truncated to signatureMaxLen.
func canonicalSignature(text string) string {
	t := strings.ToLower(text)
	t = zabbixStatePrefixRe.ReplaceAllString(t, "")
	t = urlRegex.ReplaceAllString(t, "<URL>")
	t = ipRegex.ReplaceAllString(t, "<IP>")
	t = hexIDRegex.ReplaceAllString(t, "<HEX>")
	t = numberRegex.ReplaceAllString(t, "<N>")
	t = strings.Join(strings.Fields(t), " ")
	if len(t) > signatureMaxLen {
		t = t[:signatureMaxLen]
	}
	return t
}

var zabbixStatePrefixRe = regexp.MustCompile(`^(?:problem:\s*|resolved in [^:]+:\s*)`)

// dedupLogSamples groups messages by canonicalSignature, picks the
// most-recent representative per group, sorts groups by count
// descending (recency-tiebroken), and returns the top maxGroups
// patterns. The second return is the remainder count — total
// messages dropped from the rendered output, suitable for an
// "+N other" overflow line.
//
// maxGroups <= 0 disables truncation and returns every distinct
// pattern.
func dedupLogSamples(messages []goslack.Message, maxGroups int) ([]format.LogPattern, int) {
	if len(messages) == 0 {
		return nil, 0
	}

	type group struct {
		sample goslack.Message
		count  int
	}
	bySig := make(map[string]*group)
	var sigOrder []string

	for _, m := range messages {
		sig := canonicalSignature(m.Text)
		g, ok := bySig[sig]
		if !ok {
			g = &group{sample: m}
			bySig[sig] = g
			sigOrder = append(sigOrder, sig)
		}
		g.count++
		// Keep the representative as the newest in the group; ties
		// preserve the first-seen sample (slack timestamps are
		// strict-monotonic at sub-second precision in practice).
		if msgTSGreater(m.Timestamp, g.sample.Timestamp) {
			g.sample = m
		}
	}

	patterns := make([]format.LogPattern, 0, len(bySig))
	for _, sig := range sigOrder {
		g := bySig[sig]
		patterns = append(patterns, format.LogPattern{
			Sample:    g.sample,
			Count:     g.count,
			Signature: sig,
		})
	}

	sort.SliceStable(patterns, func(i, j int) bool {
		if patterns[i].Count != patterns[j].Count {
			return patterns[i].Count > patterns[j].Count
		}
		return msgTSGreater(patterns[i].Sample.Timestamp, patterns[j].Sample.Timestamp)
	})

	if maxGroups <= 0 || len(patterns) <= maxGroups {
		return patterns, 0
	}
	remainder := 0
	for _, p := range patterns[maxGroups:] {
		remainder += p.Count
	}
	return patterns[:maxGroups], remainder
}

// msgTSGreater reports whether a is strictly newer than b. Empty
// timestamps lose to any present one; both empty returns false.
func msgTSGreater(a, b string) bool {
	if a == "" {
		return false
	}
	if b == "" {
		return true
	}
	ta, _ := strconv.ParseFloat(a, 64)
	tb, _ := strconv.ParseFloat(b, 64)
	return ta > tb
}
