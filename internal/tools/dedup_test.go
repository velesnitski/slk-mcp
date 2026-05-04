package tools

import (
	"reflect"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
)

// ----------------------- canonicalSignature -----------------------

func TestCanonicalSignature_Numbers(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"GitLab pipeline #1234 failed", "gitlab pipeline #<N> failed"},
		{"build 5678 finished", "build <N> finished"},
		{"latency 234ms", "latency <N>ms"},
		{"CPU 87%", "cpu <N>%"},
		{"no digits here", "no digits here"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := canonicalSignature(c.in); got != c.want {
				t.Fatalf("canonicalSignature(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCanonicalSignature_IPv4(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"host 192.168.1.5 down", "host <IP> down"},
		{"connection from 10.0.0.1 refused", "connection from <IP> refused"},
		// Make sure number runs in the IP aren't pre-replaced. Done
		// by ordering: IP regex runs before number regex.
		{"primary 10.0.0.1 backup 10.0.0.2", "primary <IP> backup <IP>"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := canonicalSignature(c.in); got != c.want {
				t.Fatalf("canonicalSignature(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCanonicalSignature_URLs(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"see https://example.test/build/1234 for details", "see <URL> for details"},
		{"check http://example.test/x?y=1", "check <URL>"},
		// URLs get replaced first so embedded numbers don't leak.
		{"deploy at https://10.0.0.1:8080/path", "deploy at <URL>"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := canonicalSignature(c.in); got != c.want {
				t.Fatalf("canonicalSignature(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCanonicalSignature_HexIDs(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"commit abc1234 broke main", "commit <HEX> broke main"},
		{"uuid f47ac10b58cc4372a567 expired", "uuid <HEX> expired"},
		// Short hex strings stay (avoid mangling real words).
		{"the cafe is open", "the cafe is open"},
		{"deed of trust", "deed of trust"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := canonicalSignature(c.in); got != c.want {
				t.Fatalf("canonicalSignature(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCanonicalSignature_LowercaseAndCollapse(t *testing.T) {
	in := "FATAL:\n\tBackend\n  not  responding"
	want := "fatal: backend not responding"
	if got := canonicalSignature(in); got != want {
		t.Fatalf("canonicalSignature(%q) = %q; want %q", in, got, want)
	}
}

func TestCanonicalSignature_Truncates(t *testing.T) {
	long := strings.Repeat("x", signatureMaxLen+50)
	got := canonicalSignature(long)
	if len(got) != signatureMaxLen {
		t.Fatalf("expected truncation to %d chars, got %d", signatureMaxLen, len(got))
	}
}

func TestCanonicalSignature_MergesFamilies(t *testing.T) {
	// All five lines should land on the same signature — that's the
	// whole point. Same alert, different runtime details.
	inputs := []string{
		"Trigger fired: high cpu on dc1 server-3 87%",
		"Trigger fired: high cpu on dc2 server-7 91%",
		"Trigger fired: high cpu on dc1 server-12 88%",
		"trigger fired: high cpu on dc1 server-1 85%",
		"TRIGGER fired: high cpu on dc2 server-5 92%",
	}
	first := canonicalSignature(inputs[0])
	for _, in := range inputs[1:] {
		if got := canonicalSignature(in); got != first {
			t.Fatalf("expected merge:\n  %q → %q\n  %q → %q", inputs[0], first, in, got)
		}
	}
}

func TestCanonicalSignature_DistinctAlertsStayDistinct(t *testing.T) {
	// Different alert text should NOT merge even if shape is similar.
	a := canonicalSignature("Trigger fired: high cpu on dc1")
	b := canonicalSignature("Trigger fired: low memory on dc1")
	if a == b {
		t.Fatalf("distinct alerts merged into same signature %q", a)
	}
}

func TestCanonicalSignature_MergesZabbixFlapping(t *testing.T) {
	a := canonicalSignature("Problem: Load average is too high (per CPU load over 2 for 5m)")
	b := canonicalSignature("Resolved in 56s: Load average is too high (per CPU load over 2 for 5m)")
	c := canonicalSignature("Resolved in 1m 0s: Load average is too high (per CPU load over 2 for 5m)")
	d := canonicalSignature("Resolved in 7m 0s: Load average is too high (per CPU load over 2 for 5m)")
	if a != b || a != c || a != d {
		t.Fatalf("flapping zabbix trigger should merge:\n problem=%q\n 56s=%q\n 1m=%q\n 7m=%q", a, b, c, d)
	}
}

func TestClassifyLogSeverity_Zabbix(t *testing.T) {
	cases := []struct {
		text string
		want LogSeverity
	}{
		{"Problem: ... Severity Disaster ...", SeverityFatal},
		{"Problem: ... Severity High ...", SeverityError},
		{"Problem: ... Severity Average ...", SeverityAlert},
		{"Problem: ... Severity Warning ...", SeverityWarn},
		{"Problem: routine info, no severity tag", SeverityInfo},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			m := goslack.Message{}
			m.Text = c.text
			if got := classifyLogSeverity(m); got != c.want {
				t.Fatalf("classifyLogSeverity(%q) = %v; want %v", c.text, got, c.want)
			}
		})
	}
}

// ----------------------- dedupLogSamples -----------------------

func dedupMsg(text, ts string) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = ts
	m.User = "U_BOT"
	m.Text = text
	return m
}

func TestDedupLogSamples_GroupsAndCounts(t *testing.T) {
	msgs := []goslack.Message{
		dedupMsg("trigger fired: high cpu on dc1", "1700000100.000000"),
		dedupMsg("trigger fired: high cpu on dc2", "1700000200.000000"),
		dedupMsg("trigger fired: high cpu on dc3", "1700000300.000000"),
		dedupMsg("pipeline #1234 failed", "1700000400.000000"),
		dedupMsg("pipeline #5678 failed", "1700000500.000000"),
	}
	patterns, remainder := dedupLogSamples(msgs, 0)
	if remainder != 0 {
		t.Fatalf("remainder = %d; want 0 (no cap)", remainder)
	}
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns (high cpu × 3, pipeline failed × 2), got %d", len(patterns))
	}
	// First pattern (highest count) should be the cpu trigger.
	if patterns[0].Count != 3 {
		t.Fatalf("top pattern count = %d; want 3", patterns[0].Count)
	}
	if patterns[1].Count != 2 {
		t.Fatalf("second pattern count = %d; want 2", patterns[1].Count)
	}
}

func TestDedupLogSamples_RepresentativeIsNewest(t *testing.T) {
	msgs := []goslack.Message{
		dedupMsg("trigger fired: high cpu on dc1", "1700000100.000000"),
		dedupMsg("trigger fired: high cpu on dc2", "1700000300.000000"), // newest
		dedupMsg("trigger fired: high cpu on dc3", "1700000200.000000"),
	}
	patterns, _ := dedupLogSamples(msgs, 0)
	if len(patterns) != 1 {
		t.Fatalf("expected single merged pattern, got %d", len(patterns))
	}
	if got := patterns[0].Sample.Timestamp; got != "1700000300.000000" {
		t.Fatalf("representative ts = %q; want newest 1700000300.000000", got)
	}
}

func TestDedupLogSamples_TopNAndRemainder(t *testing.T) {
	// Five distinct patterns with counts 5, 4, 3, 2, 1. Cap at 2 →
	// top 2 patterns kept (5 + 4), remainder = 3 + 2 + 1 = 6.
	var msgs []goslack.Message
	addN := func(text string, n int) {
		for i := 0; i < n; i++ {
			msgs = append(msgs, dedupMsg(text, "1700000000.000000"))
		}
	}
	addN("alpha alert", 5)
	addN("beta alert", 4)
	addN("gamma alert", 3)
	addN("delta alert", 2)
	addN("epsilon alert", 1)

	patterns, remainder := dedupLogSamples(msgs, 2)
	if len(patterns) != 2 {
		t.Fatalf("expected top 2 patterns, got %d", len(patterns))
	}
	if patterns[0].Count != 5 || patterns[1].Count != 4 {
		t.Fatalf("counts = %d, %d; want 5, 4", patterns[0].Count, patterns[1].Count)
	}
	if remainder != 6 {
		t.Fatalf("remainder = %d; want 6", remainder)
	}
}

func TestDedupLogSamples_TiebreakByRecency(t *testing.T) {
	// Two patterns each with count 2. Tiebreak: most-recent
	// representative wins.
	msgs := []goslack.Message{
		dedupMsg("alpha alert", "1700000100.000000"),
		dedupMsg("alpha alert", "1700000200.000000"),
		dedupMsg("beta alert", "1700000300.000000"),
		dedupMsg("beta alert", "1700000400.000000"), // newest
	}
	patterns, _ := dedupLogSamples(msgs, 0)
	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(patterns))
	}
	if patterns[0].Sample.Timestamp != "1700000400.000000" {
		t.Fatalf("tiebreak should prefer most-recent rep; got %q", patterns[0].Sample.Timestamp)
	}
}

func TestDedupLogSamples_Empty(t *testing.T) {
	patterns, remainder := dedupLogSamples(nil, 5)
	if patterns != nil || remainder != 0 {
		t.Fatalf("nil/0 expected, got %v / %d", patterns, remainder)
	}
}

// ----------------------- LogChannelDigest with patterns -----------------------

func TestLogChannelDigest_RendersPatternCounts(t *testing.T) {
	bands := []format.LogBand{
		{
			Label: "ERROR", Total: 12,
			Patterns: []format.LogPattern{
				{Sample: dedupMsg("pipeline #1234 failed", "1700000100.000000"), Count: 8, Signature: "pipeline #<N> failed"},
				{Sample: dedupMsg("image scan exception", "1700000200.000000"), Count: 3, Signature: "image scan exception"},
				{Sample: dedupMsg("rare timeout edge case", "1700000300.000000"), Count: 1, Signature: "rare timeout edge case"},
			},
		},
	}
	out := format.LogChannelDigest("#metrics-feed-alerts", 12, bands, nil)

	for _, want := range []string{
		"## #metrics-feed-alerts [LOG MODE — 12 msgs]",
		"recent ERROR:",
		"pipeline #1234 failed",
		"(×8 similar)",
		"image scan exception",
		"(×3 similar)",
		"rare timeout edge case",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
	// Count == 1 must NOT get a (×N) suffix.
	if strings.Contains(out, "(×1 similar)") {
		t.Errorf("count=1 must not emit (×1 similar):\n%s", out)
	}
}

func TestLogChannelDigest_PatternsOverflow(t *testing.T) {
	bands := []format.LogBand{
		{
			Label: "ERROR", Total: 20,
			Patterns: []format.LogPattern{
				{Sample: dedupMsg("a", "1700000100.000000"), Count: 5},
				{Sample: dedupMsg("b", "1700000200.000000"), Count: 4},
			},
		},
	}
	out := format.LogChannelDigest("#alerts", 20, bands, nil)
	want := "+11 other"
	if !strings.Contains(out, want) {
		t.Fatalf("expected %q (Total 20 - rendered 9), got:\n%s", want, out)
	}
}

func TestLogChannelDigest_LegacySamplesStillWork(t *testing.T) {
	// Backwards compat: callers that populate Samples but not
	// Patterns get the legacy per-message rendering.
	bands := []format.LogBand{
		{
			Label: "WARN", Total: 4,
			Samples: []goslack.Message{
				dedupMsg("disk usage 78%", "1700000100.000000"),
				dedupMsg("disk usage 81%", "1700000200.000000"),
			},
		},
	}
	out := format.LogChannelDigest("#storage-alerts", 4, bands, nil)
	for _, want := range []string{
		"recent WARN:",
		"disk usage 78%",
		"disk usage 81%",
		"... +2 more", // legacy "+N more", not "+N other"
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in legacy output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(×") {
		t.Errorf("legacy output must not include pattern counts:\n%s", out)
	}
}

// ----------------------- buildLogBands integration -----------------------

func TestBuildLogBands_DedupesAndOrders(t *testing.T) {
	msgs := []goslack.Message{
		dedupMsg("FATAL backend not responding", "1700000100.000000"),
		dedupMsg("FATAL backend not responding", "1700000200.000000"),
		dedupMsg("ERROR pipeline #1 failed", "1700000300.000000"),
		dedupMsg("ERROR pipeline #2 failed", "1700000400.000000"),
		dedupMsg("ERROR pipeline #3 failed", "1700000500.000000"),
		dedupMsg("ERROR distinct exception", "1700000600.000000"),
	}
	bands := buildLogBands(msgs, 5)

	for _, b := range bands {
		switch b.Label {
		case "FATAL":
			if b.Total != 2 || len(b.Patterns) != 1 || b.Patterns[0].Count != 2 {
				t.Errorf("FATAL: total=%d patterns=%d count[0]=%d (want 2/1/2)",
					b.Total, len(b.Patterns), b.Patterns[0].Count)
			}
		case "ERROR":
			// The three pipeline-failed messages share a signature
			// (numbers replaced); the lone "distinct exception" is
			// its own pattern.
			if b.Total != 4 {
				t.Errorf("ERROR total = %d; want 4", b.Total)
			}
			if len(b.Patterns) != 2 {
				t.Errorf("ERROR patterns = %d; want 2 (pipeline ×3 + distinct ×1)", len(b.Patterns))
			}
			counts := []int{b.Patterns[0].Count, b.Patterns[1].Count}
			if !reflect.DeepEqual(counts, []int{3, 1}) {
				t.Errorf("ERROR pattern counts = %v; want [3 1]", counts)
			}
		}
	}
}
