package format

import (
	"strings"
	"testing"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"
)

// lgMsg builds a message with text and a resolvable author.
func lgMsg(user, ts, text string) goslack.Message {
	var m goslack.Message
	m.User = user
	m.Timestamp = ts
	m.Text = text
	return m
}

func lgUsers() map[string]string {
	return map[string]string{"U1": "Alex", "U2": "Sam"}
}

// ----------------------------- LogChannelDigest -----------------------------

func TestLogChannelDigest_HeaderCarriesFullTotal(t *testing.T) {
	// The header reports the channel's whole unread count, not the
	// number of messages actually listed below it — a histogram-only
	// band still counts.
	out := LogChannelDigest("#alpha", 500, []LogBand{
		{Label: "error", Total: 400},
		{Label: "warn", Total: 100},
	}, lgUsers())

	if !strings.Contains(out, "## #alpha [LOG MODE — 500 msgs]") {
		t.Fatalf("header missing full total:\n%s", out)
	}
	if !strings.Contains(out, "severity: error=400 warn=100") {
		t.Fatalf("histogram wrong:\n%s", out)
	}
}

func TestLogChannelDigest_EmptyBandsOmittedFromHistogram(t *testing.T) {
	out := LogChannelDigest("#alpha", 3, []LogBand{
		{Label: "error", Total: 3},
		{Label: "warn", Total: 0},
		{Label: "info", Total: 0},
	}, nil)

	if strings.Contains(out, "warn") || strings.Contains(out, "info") {
		t.Fatalf("zero-total bands should not appear:\n%s", out)
	}
	if !strings.Contains(out, "severity: error=3") {
		t.Fatalf("histogram wrong:\n%s", out)
	}
}

func TestLogChannelDigest_NoClassifiedMessagesSaysSo(t *testing.T) {
	// Every band empty must still produce a readable line. A blank
	// severity row would read as a broken renderer.
	out := LogChannelDigest("#alpha", 12, []LogBand{{Label: "error", Total: 0}}, nil)
	if !strings.Contains(out, "severity: (no classified messages)") {
		t.Fatalf("expected the explicit empty-histogram line:\n%s", out)
	}
}

func TestLogChannelDigest_NilBands(t *testing.T) {
	out := LogChannelDigest("#alpha", 0, nil, nil)
	if !strings.Contains(out, "(no classified messages)") {
		t.Fatalf("nil bands should render the empty line:\n%s", out)
	}
}

func TestLogChannelDigest_PatternsCollapseWithSimilarCount(t *testing.T) {
	out := LogChannelDigest("#alpha", 10, []LogBand{{
		Label: "error",
		Total: 10,
		Patterns: []LogPattern{
			{Sample: lgMsg("U1", "1700000000.000100", "disk full"), Count: 7},
			{Sample: lgMsg("U2", "1700000001.000100", "timeout"), Count: 1},
		},
	}}, lgUsers())

	if !strings.Contains(out, "recent error:") {
		t.Fatalf("band heading missing:\n%s", out)
	}
	if !strings.Contains(out, "(×7 similar)") {
		t.Fatalf("collapsed count missing:\n%s", out)
	}
	// Count == 1 must NOT carry a suffix — "(×1 similar)" is noise.
	if strings.Contains(out, "(×1 similar)") {
		t.Fatalf("single-occurrence pattern should have no suffix:\n%s", out)
	}
	// 7 + 1 rendered of 10 total → 2 unaccounted.
	if !strings.Contains(out, "... +2 other") {
		t.Fatalf("overflow line wrong:\n%s", out)
	}
}

func TestLogChannelDigest_PatternOverflowOmittedWhenFullyAccounted(t *testing.T) {
	out := LogChannelDigest("#alpha", 4, []LogBand{{
		Label:    "warn",
		Total:    4,
		Patterns: []LogPattern{{Sample: lgMsg("U1", "1700000000.000100", "slow"), Count: 4}},
	}}, lgUsers())

	if strings.Contains(out, "other") {
		t.Fatalf("no overflow expected when counts match:\n%s", out)
	}
}

func TestLogChannelDigest_ContentlessPatternsDropBandEntirely(t *testing.T) {
	// A band whose every sample has no renderable content must be
	// skipped rather than emitting a heading with nothing under it.
	out := LogChannelDigest("#alpha", 5, []LogBand{{
		Label:    "error",
		Total:    5,
		Patterns: []LogPattern{{Sample: goslack.Message{}, Count: 5}},
	}}, nil)

	if strings.Contains(out, "recent error:") {
		t.Fatalf("band with no renderable sample should be omitted:\n%s", out)
	}
	// The histogram still reports it — the messages exist, they just
	// cannot be shown.
	if !strings.Contains(out, "severity: error=5") {
		t.Fatalf("histogram should still count the band:\n%s", out)
	}
}

func TestLogChannelDigest_SamplesPathUsedWhenNoPatterns(t *testing.T) {
	out := LogChannelDigest("#alpha", 9, []LogBand{{
		Label: "error",
		Total: 9,
		Samples: []goslack.Message{
			lgMsg("U1", "1700000000.000100", "first failure"),
			lgMsg("U2", "1700000001.000100", "second failure"),
		},
	}}, lgUsers())

	if !strings.Contains(out, "first failure") || !strings.Contains(out, "second failure") {
		t.Fatalf("samples not rendered:\n%s", out)
	}
	// Legacy path says "more", not "other".
	if !strings.Contains(out, "... +7 more") {
		t.Fatalf("sample overflow line wrong:\n%s", out)
	}
}

func TestLogChannelDigest_PatternsWinOverSamples(t *testing.T) {
	out := LogChannelDigest("#alpha", 2, []LogBand{{
		Label:    "error",
		Total:    2,
		Patterns: []LogPattern{{Sample: lgMsg("U1", "1700000000.000100", "from pattern"), Count: 2}},
		Samples:  []goslack.Message{lgMsg("U2", "1700000001.000100", "from sample")},
	}}, lgUsers())

	if !strings.Contains(out, "from pattern") {
		t.Fatalf("patterns should be preferred:\n%s", out)
	}
	if strings.Contains(out, "from sample") {
		t.Fatalf("samples must not render when patterns are present:\n%s", out)
	}
}

func TestLogChannelDigest_EmptyUsersMapFallsBackToIDs(t *testing.T) {
	// Documented contract: an empty users map is legal and the
	// renderer falls back to the raw user ID rather than blanking
	// the author.
	out := LogChannelDigest("#alpha", 1, []LogBand{{
		Label:    "error",
		Total:    1,
		Patterns: []LogPattern{{Sample: lgMsg("U9", "1700000000.000100", "unresolved author"), Count: 1}},
	}}, map[string]string{})

	if !strings.Contains(out, "U9") {
		t.Fatalf("expected the raw user ID as fallback:\n%s", out)
	}
}

func TestLogChannelDigest_DoesNotMutateCallerPatterns(t *testing.T) {
	// The filter-in-place idiom `band.Patterns[:0]` shares a backing
	// array with the caller's slice. Rendering is a read; a caller
	// that renders twice, or inspects its bands afterwards, must see
	// what it passed in.
	patterns := []LogPattern{
		{Sample: goslack.Message{}, Count: 1},                                // no content — filtered
		{Sample: lgMsg("U1", "1700000000.000100", "real message"), Count: 2}, // kept
	}
	before := make([]LogPattern, len(patterns))
	copy(before, patterns)

	_ = LogChannelDigest("#alpha", 3, []LogBand{{Label: "error", Total: 3, Patterns: patterns}}, lgUsers())

	for i := range before {
		if patterns[i].Sample.Text != before[i].Sample.Text || patterns[i].Count != before[i].Count {
			t.Fatalf("LogChannelDigest mutated caller's Patterns at index %d: got {%q,%d}, want {%q,%d}",
				i, patterns[i].Sample.Text, patterns[i].Count, before[i].Sample.Text, before[i].Count)
		}
	}
}

// ----------------------------- ForwardedOrigin -----------------------------

func TestForwardedOrigin_ReturnsAttachmentTS(t *testing.T) {
	var m goslack.Message
	m.Attachments = []goslack.Attachment{{Ts: "1700000000.000200"}}

	if got := ForwardedOrigin(m); got != "1700000000.000200" {
		t.Fatalf("got %q; want the attachment ts", got)
	}
}

func TestForwardedOrigin_EmptyWhenMessageCarriesItsOwnFiles(t *testing.T) {
	// A message with its own files is not a forward we need to
	// redirect the reader away from.
	var m goslack.Message
	m.Files = []goslack.File{{ID: "F1"}}
	m.Attachments = []goslack.Attachment{{Ts: "1700000000.000200"}}

	if got := ForwardedOrigin(m); got != "" {
		t.Fatalf("got %q; want empty when the message has its own files", got)
	}
}

func TestForwardedOrigin_EmptyWithoutAttachments(t *testing.T) {
	if got := ForwardedOrigin(goslack.Message{}); got != "" {
		t.Fatalf("got %q; want empty", got)
	}
}

func TestForwardedOrigin_SkipsAttachmentsWithoutTS(t *testing.T) {
	var m goslack.Message
	m.Attachments = []goslack.Attachment{
		{Text: "a link preview, no ts"},
		{Ts: "1700000000.000300"},
	}
	if got := ForwardedOrigin(m); got != "1700000000.000300" {
		t.Fatalf("got %q; want the first attachment carrying a ts", got)
	}
}

// ----------------------------- DecisionLine -----------------------------

func TestDecisionLine_RendersAllFields(t *testing.T) {
	m := lgMsg("U1", "1700000000.000100", "we ship on Friday")
	got := DecisionLine(m, "alpha", "Alex", "keyword:ship")

	for _, want := range []string{"- #alpha", "(Alex)", "[keyword:ship]", "we ship on Friday"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestDecisionLine_CollapsesWhitespace(t *testing.T) {
	m := lgMsg("U1", "1700000000.000100", "decided:\n\n   ship  it")
	got := DecisionLine(m, "alpha", "Alex", "keyword:decided")

	if strings.Contains(got, "\n") {
		t.Fatalf("newlines must be collapsed: %q", got)
	}
	if !strings.Contains(got, "decided: ship it") {
		t.Fatalf("whitespace not collapsed as expected: %q", got)
	}
}

func TestDecisionLine_UnparseableTimestampLeavesTimeBlank(t *testing.T) {
	m := lgMsg("U1", "not-a-timestamp", "body")
	got := DecisionLine(m, "alpha", "Alex", "reaction::white_check_mark:")

	if !strings.Contains(got, "- #alpha  (Alex)") {
		t.Fatalf("expected an empty time slot, got %q", got)
	}
}

func TestDecisionLine_LongBodyIsTruncated(t *testing.T) {
	m := lgMsg("U1", "1700000000.000100", strings.Repeat("a", 400))
	got := DecisionLine(m, "alpha", "Alex", "keyword:ship")

	if !strings.HasSuffix(got, "...") {
		t.Fatalf("long body should be truncated with an ellipsis: %q", got)
	}
}

func TestDecisionLine_TruncationKeepsValidUTF8(t *testing.T) {
	// Truncation must cut on a character boundary. Slicing by byte
	// splits a multi-byte rune and emits U+FFFD, which is what the
	// reader sees instead of the text.
	for _, tc := range []struct{ name, body string }{
		{"cyrillic", "x" + strings.Repeat("я", 200)},
		{"cjk", strings.Repeat("日", 200)},
		{"emoji", strings.Repeat("🙂", 200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := DecisionLine(lgMsg("U1", "1700000000.000100", tc.body), "alpha", "Alex", "keyword:x")
			if !utf8.ValidString(got) {
				t.Fatalf("truncation produced invalid UTF-8: %q", got)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Fatalf("truncation produced U+FFFD: %q", got)
			}
		})
	}
}

// ----------------------------- SearchResult -----------------------------

func sgHit(channelName, user, ts, text string) goslack.SearchMessage {
	var m goslack.SearchMessage
	m.Channel.Name = channelName
	m.Username = user
	m.Timestamp = ts
	m.Text = text
	return m
}

func TestSearchResult_RendersChannelAuthorAndBody(t *testing.T) {
	got := SearchResult(sgHit("alpha", "Alex", "1700000000.000100", "the body"))

	for _, want := range []string{"alpha", "(Alex)", "the body"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestSearchResult_LongBodyTruncated(t *testing.T) {
	got := SearchResult(sgHit("alpha", "Alex", "1700000000.000100", strings.Repeat("a", 400)))
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected truncation: %q", got)
	}
}

func TestSearchResult_TruncationKeepsValidUTF8(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"cyrillic", "x" + strings.Repeat("я", 300)},
		{"cjk", strings.Repeat("日", 300)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SearchResult(sgHit("alpha", "Alex", "1700000000.000100", tc.body))
			if !utf8.ValidString(got) {
				t.Fatalf("truncation produced invalid UTF-8: %q", got)
			}
			if strings.ContainsRune(got, utf8.RuneError) {
				t.Fatalf("truncation produced U+FFFD: %q", got)
			}
		})
	}
}

func TestSearchResultExt_FullTextSkipsTruncation(t *testing.T) {
	body := strings.Repeat("a", 400)
	got := SearchResultExt(sgHit("alpha", "Alex", "1700000000.000100", body), true, nil)

	if strings.Contains(got, "...") {
		t.Fatalf("full_text should not truncate: %q", got)
	}
	if !strings.Contains(got, body) {
		t.Fatal("full body missing from full_text rendering")
	}
}

func TestSearchResultExt_ThreadSuffixOnlyWithPermalink(t *testing.T) {
	m := sgHit("alpha", "Alex", "1700000000.000100", "body")
	if got := SearchResultExt(m, false, nil); strings.Contains(got, "thread_ts=") {
		t.Fatalf("no permalink means no thread suffix: %q", got)
	}
}
