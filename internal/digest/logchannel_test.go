package digest

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
)

// ----------------------- classifyLogSeverity -----------------------

func TestClassifyLogSeverity(t *testing.T) {
	cases := []struct {
		text string
		want LogSeverity
	}{
		// Positive cases — strongest match wins
		{"FATAL: backend not responding", SeverityFatal},
		{"panic: runtime error", SeverityFatal}, // both panic and error → FATAL wins
		{"GitLab pipeline #123 failed", SeverityError},
		{"connection refused — error", SeverityError},
		{"unhandled exception in worker", SeverityError},
		{"image scan failure", SeverityError},
		{"alert: us-east-1 outage", SeverityAlert},
		{"connection timed out", SeverityAlert},
		{"приложение не отвечает", SeverityAlert},
		{"WARN: disk usage 78%", SeverityWarn},
		{"warning — slow query detected", SeverityWarn},
		// Negative — no severity terms
		{"pipeline succeeded", SeverityInfo},
		{"merged !42 by alex", SeverityInfo},
		{"", SeverityInfo},
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

func TestLogSeverity_StringStable(t *testing.T) {
	want := []string{"INFO", "WARN", "ALERT", "ERROR", "FATAL"}
	got := []string{
		SeverityInfo.String(),
		SeverityWarn.String(),
		SeverityAlert.String(),
		SeverityError.String(),
		SeverityFatal.String(),
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("severity[%d] = %q; want %q", i, got[i], w)
		}
	}
}

// ----------------------- isBotMessage -----------------------

func TestIsBotMessage(t *testing.T) {
	cases := []struct {
		name    string
		botID   string
		subType string
		want    bool
	}{
		{"plain user message", "", "", false},
		{"bot id set", "B12345", "", true},
		{"bot_message subtype", "", "bot_message", true},
		{"both set", "B12345", "bot_message", true},
		{"non-bot subtype", "", "channel_join", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := goslack.Message{}
			m.BotID = c.botID
			m.SubType = c.subType
			if got := isBotMessage(m); got != c.want {
				t.Fatalf("isBotMessage botID=%q subType=%q = %v; want %v",
					c.botID, c.subType, got, c.want)
			}
		})
	}
}

// ----------------------- isLogChannelName -----------------------

func TestIsLogChannelName(t *testing.T) {
	// Synthetic names exercising each keyword in logChannelNameKeywords.
	// Keep these generic so the test fixtures don't accidentally
	// document a real workspace's channel inventory.
	positive := []string{
		"app-logs",            // log
		"team-alerts",         // alert
		"infra-alarm-feed",    // alarm
		"infra-monitor-low",   // monitor
		"perf-monitoring",     // monitoring
		"metric-feed",         // metric
		"metrics-stream",      // metrics
		"weekly-report",       // report
		"daily-reports",       // reports
		"build-cron-feed",     // cron
		"team-incident-room",  // incident
	}
	for _, n := range positive {
		t.Run(n, func(t *testing.T) {
			if !isLogChannelName(n) {
				t.Errorf("isLogChannelName(%q) = false; want true", n)
			}
		})
	}

	negative := []string{
		"general",
		"team-room",
		"random",
		"announcements",
		"meeting-notes",
	}
	for _, n := range negative {
		t.Run("neg/"+n, func(t *testing.T) {
			if isLogChannelName(n) {
				t.Errorf("isLogChannelName(%q) = true; want false", n)
			}
		})
	}
}

// ----------------------- DetectLogChannel -----------------------

func TestDetectLogChannel_BotMajority(t *testing.T) {
	cu := mkChannelUnread("anything", []goslack.Message{
		botMsg("alert 1"),
		botMsg("alert 2"),
		botMsg("alert 3"),
		humanMsg("U1", "human reply"),
	}, nil)
	if !DetectLogChannel(cu) {
		t.Fatal("3 of 4 bot messages should classify as log channel")
	}
}

func TestDetectLogChannel_BelowBotThreshold(t *testing.T) {
	cu := mkChannelUnread("general", []goslack.Message{
		botMsg("notification"),
		humanMsg("U1", "morning"),
		humanMsg("U2", "afternoon"),
		humanMsg("U3", "evening"),
	}, nil)
	if DetectLogChannel(cu) {
		t.Fatal("1-of-4 bot share + neutral name should NOT classify as log")
	}
}

func TestDetectLogChannel_NameFallback(t *testing.T) {
	// Webhook-style: real user account posts but the channel is
	// clearly a feed by name. Name fallback catches it.
	cu := mkChannelUnread("infra-monitor-critical", []goslack.Message{
		humanMsg("U_WEBHOOK", "FATAL trigger fired"),
		humanMsg("U_WEBHOOK", "ERROR another one"),
	}, nil)
	if !DetectLogChannel(cu) {
		t.Fatal("name pattern should classify as log channel even with no bot_id")
	}
}

func TestDetectLogChannel_EmptyChannelIsNotLog(t *testing.T) {
	cu := mkChannelUnread("infra-monitor-low", nil, nil)
	if DetectLogChannel(cu) {
		t.Fatal("empty channel must not be classified as log")
	}
}

// ----------------------- BuildLogBands -----------------------

func TestBuildLogBands_DistributesAndOrders(t *testing.T) {
	msgs := []goslack.Message{
		mkLogMsg("FATAL: dc1 down"),
		mkLogMsg("ERROR: pipeline failed"),
		mkLogMsg("ERROR: pipeline failed (different)"),
		mkLogMsg("WARN: high cpu"),
		mkLogMsg("alert: cert expiring"),
		mkLogMsg("merged !42"), // INFO
	}
	bands := BuildLogBands(msgs, 5)

	wantOrder := []string{"FATAL", "ERROR", "ALERT", "WARN", "INFO"}
	for i, b := range bands {
		if b.Label != wantOrder[i] {
			t.Fatalf("band[%d] = %q; want %q", i, b.Label, wantOrder[i])
		}
	}

	wantTotals := map[string]int{
		"FATAL": 1, "ERROR": 2, "ALERT": 1, "WARN": 1, "INFO": 1,
	}
	for _, b := range bands {
		if b.Total != wantTotals[b.Label] {
			t.Errorf("band %s total = %d; want %d", b.Label, b.Total, wantTotals[b.Label])
		}
	}
}

func TestBuildLogBands_DedupesIdenticalMessages(t *testing.T) {
	// 10 messages that share a signature → 1 pattern with Count=10.
	var msgs []goslack.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, mkLogMsg("ERROR: pipeline failed"))
	}
	bands := BuildLogBands(msgs, 3)
	for _, b := range bands {
		if b.Label != "ERROR" {
			continue
		}
		if b.Total != 10 {
			t.Fatalf("ERROR total = %d; want 10", b.Total)
		}
		if len(b.Patterns) != 1 {
			t.Fatalf("ERROR patterns = %d; want 1 (all share a signature)", len(b.Patterns))
		}
		if got := b.Patterns[0].Count; got != 10 {
			t.Fatalf("ERROR pattern count = %d; want 10", got)
		}
	}
}

func TestBuildLogBands_PatternsCappedAtPerBandLimit(t *testing.T) {
	// 10 distinct-body ERROR messages → 10 distinct patterns; cap at 3.
	var msgs []goslack.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, mkLogMsg("ERROR: distinct alert "+string(rune('A'+i))))
	}
	bands := BuildLogBands(msgs, 3)
	for _, b := range bands {
		if b.Label != "ERROR" {
			continue
		}
		if b.Total != 10 {
			t.Fatalf("ERROR total = %d; want 10", b.Total)
		}
		if len(b.Patterns) != 3 {
			t.Fatalf("ERROR patterns = %d; want 3 (capped)", len(b.Patterns))
		}
	}
}

func TestBuildLogBands_ZeroPatternsUsesDefault(t *testing.T) {
	var msgs []goslack.Message
	for i := 0; i < 10; i++ {
		msgs = append(msgs, mkLogMsg("ERROR distinct "+string(rune('A'+i))))
	}
	bands := BuildLogBands(msgs, 0)
	for _, b := range bands {
		if b.Label == "ERROR" && len(b.Patterns) != defaultPatternsPerBand {
			t.Fatalf("default patternsPerBand should be %d, got %d",
				defaultPatternsPerBand, len(b.Patterns))
		}
	}
}

// ----------------------- LogChannelDigest -----------------------

func TestLogChannelDigest_RendersHistogramAndSamples(t *testing.T) {
	bands := []format.LogBand{
		{Label: "FATAL", Total: 2, Samples: []goslack.Message{
			mkLogMsg("FATAL one"), mkLogMsg("FATAL two"),
		}},
		{Label: "ERROR", Total: 5, Samples: []goslack.Message{
			mkLogMsg("ERROR a"), mkLogMsg("ERROR b"),
		}},
		{Label: "ALERT", Total: 0, Samples: nil}, // skipped
		{Label: "WARN", Total: 1, Samples: []goslack.Message{mkLogMsg("WARN c")}},
		{Label: "INFO", Total: 8, Samples: []goslack.Message{mkLogMsg("info pipe")}},
	}
	out := format.LogChannelDigest("#metrics-feed-alerts", 16, bands, nil)

	for _, want := range []string{
		"## #metrics-feed-alerts [LOG MODE — 16 msgs]",
		"FATAL=2",
		"ERROR=5",
		"WARN=1",
		"INFO=8",
		"recent FATAL:",
		"recent ERROR:",
		"... +3 more",     // ERROR has 5 total, only 2 samples → +3
		"... +7 more",     // INFO 8 total, 1 sample → +7
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ALERT=0") {
		t.Errorf("zero-total bands must NOT appear in histogram:\n%s", out)
	}
	if strings.Contains(out, "recent ALERT:") {
		t.Errorf("zero-sample bands must NOT have a 'recent X' section:\n%s", out)
	}
}

func TestLogChannelDigest_AllInfo(t *testing.T) {
	bands := []format.LogBand{
		{Label: "INFO", Total: 3, Samples: []goslack.Message{mkLogMsg("ok")}},
	}
	out := format.LogChannelDigest("#team-reports", 3, bands, nil)
	if !strings.Contains(out, "INFO=3") {
		t.Fatalf("INFO band should appear in histogram: %s", out)
	}
}

func TestLogChannelDigest_NoClassifiedMessages(t *testing.T) {
	// No bands with non-zero total — render the placeholder line.
	bands := []format.LogBand{
		{Label: "FATAL", Total: 0},
		{Label: "ERROR", Total: 0},
		{Label: "INFO", Total: 0},
	}
	out := format.LogChannelDigest("#team-cron-reports", 0, bands, nil)
	if !strings.Contains(out, "(no classified messages)") {
		t.Fatalf("expected placeholder line, got: %s", out)
	}
}

// ----------------------- shared helpers -----------------------

func botMsg(text string) goslack.Message {
	m := mkLogMsg(text)
	m.BotID = "B_test"
	return m
}

func humanMsg(user, text string) goslack.Message {
	m := mkLogMsg(text)
	m.User = user
	return m
}

// mkLogMsg is the minimal logged-message constructor used across the
// log-mode test suite. Distinct from the test-only mkMsg helper in
// urgency tests; kept private to logchannel_test.go for isolation.
func mkLogMsg(text string) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.Text = text
	return m
}
