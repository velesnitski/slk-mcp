package tools

import (
	"strings"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// LogSeverity bands a single message during log/alert channel
// rendering. Bands are ordered low → high; classifyLogSeverity
// returns the highest-severity match.
type LogSeverity int

const (
	SeverityInfo LogSeverity = iota
	SeverityWarn
	SeverityAlert
	SeverityError
	SeverityFatal
)

// String returns the human-readable severity label used in digest output.
func (s LogSeverity) String() string {
	switch s {
	case SeverityFatal:
		return "FATAL"
	case SeverityError:
		return "ERROR"
	case SeverityAlert:
		return "ALERT"
	case SeverityWarn:
		return "WARN"
	default:
		return "INFO"
	}
}

// orderedSeverities lists the bands that buildLogBands produces, in
// the order LogChannelDigest renders them — strongest first.
var orderedSeverities = []LogSeverity{
	SeverityFatal, SeverityError, SeverityAlert, SeverityWarn, SeverityInfo,
}

// classifyLogSeverity scans a message body for severity terms and
// returns the strongest band detected. Term order follows the
// log/alert vocabulary that zabbix, gitlab CI, harbor, and aws
// alarms emit. Falls back to SeverityInfo when nothing matches.
func classifyLogSeverity(m goslack.Message) LogSeverity {
	lower := strings.ToLower(m.Text)
	// Status reports that explicitly succeeded ("Status: PASSED",
	// "Pass rate: 100%", "Failed: 0") shouldn't bin as ERROR just
	// because they contain the literal word "Failed:". Re-classify
	// to INFO when the text is clearly a success summary.
	if isSuccessReport(lower) {
		return SeverityInfo
	}
	switch {
	case containsAny(lower, "severity disaster", "severitydisaster", "fatal", "panic"):
		return SeverityFatal
	case containsAny(lower, "severity high", "severityhigh", "error", "errors", "exception", "failed", "failure"):
		return SeverityError
	case containsAny(lower, "severity average", "severityaverage", "alert", "outage", "timed out", "не отвечает"):
		return SeverityAlert
	case containsAny(lower, "severity warning", "severitywarning", "warn", "warning"):
		return SeverityWarn
	default:
		return SeverityInfo
	}
}

// isSuccessReport reports whether the body looks like a passing-test
// or healthy-status summary, regardless of any severity-keyword
// substrings it happens to contain.
func isSuccessReport(lower string) bool {
	hasPassed := containsAny(lower, "status: passed", "status:passed", "pass rate: 100", "pass rate:100")
	failedZero := strings.Contains(lower, "failed: 0") || strings.Contains(lower, "failed:0")
	return hasPassed && failedZero
}

func containsAny(s string, terms ...string) bool {
	for _, t := range terms {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// detectLogChannel heuristically identifies a bot-driven log/alert
// channel. Two signals, OR'd together:
//
//  1. Bot authorship — if at least logChannelBotThreshold of the
//     unread messages have a bot_id or "bot_message" subtype, the
//     channel is treated as machine-driven regardless of name.
//  2. Channel name pattern — fallback for human-relayed feeds where
//     a real user account is posting through a webhook (so BotID is
//     empty). Catches names like "*-alerts", "*-monitoring",
//     "*-cron", "*-incident", etc.
//
// Returns false on empty channels (nothing to classify).
func detectLogChannel(cu *slack.ChannelUnread) bool {
	if len(cu.Messages) == 0 {
		return false
	}
	botCount := 0
	for _, m := range cu.Messages {
		if isBotMessage(m) {
			botCount++
		}
	}
	if float64(botCount)/float64(len(cu.Messages)) >= logChannelBotThreshold {
		return true
	}
	return isLogChannelName(cu.Channel.Name)
}

// logChannelBotThreshold — fraction of bot messages required to
// auto-classify a channel as log mode purely on authorship.
const logChannelBotThreshold = 0.5

// isBotMessage reports whether a message was authored by an
// integration / webhook / bot user.
func isBotMessage(m goslack.Message) bool {
	return m.BotID != "" || m.SubType == "bot_message"
}

// logChannelNameKeywords drive the fallback channel-name signal in
// detectLogChannel. Substrings are matched case-insensitively.
var logChannelNameKeywords = []string{
	"log", "alert", "alarm", "monitor", "monitoring",
	"metric", "metrics", "report", "reports", "cron",
	"incident",
}

func isLogChannelName(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range logChannelNameKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// buildLogBands groups messages by severity and dedupes similar
// messages within each band into LogPatterns, capped at
// patternsPerBand distinct patterns per severity. Returns one entry
// per severity in dominance order (FATAL → INFO) — empty bands have
// Total=0 and no patterns, so LogChannelDigest hides them.
//
// patternsPerBand <= 0 falls back to defaultPatternsPerBand.
func buildLogBands(messages []goslack.Message, patternsPerBand int) []format.LogBand {
	if patternsPerBand <= 0 {
		patternsPerBand = defaultPatternsPerBand
	}

	bins := make(map[LogSeverity][]goslack.Message, len(orderedSeverities))
	totals := make(map[LogSeverity]int, len(orderedSeverities))
	for _, m := range messages {
		sev := classifyLogSeverity(m)
		// Zabbix-style alerts: rewrite to a structured one-liner so
		// the renderer surfaces host + metric data instead of the
		// labelled multi-line payload. Severity classification stays
		// driven by the original text (above) so "Severity Average"
		// still maps to ALERT.
		if alert := parseZabbixAlert(m.Text); alert != nil {
			m.Text = alert.OneLine()
		}
		bins[sev] = append(bins[sev], m)
		totals[sev]++
	}

	bands := make([]format.LogBand, 0, len(orderedSeverities))
	for _, sev := range orderedSeverities {
		msgs := bins[sev]
		patterns, _ := dedupLogSamples(msgs, patternsPerBand)
		bands = append(bands, format.LogBand{
			Label:    sev.String(),
			Total:    totals[sev],
			Patterns: patterns,
		})
	}
	return bands
}

// defaultPatternsPerBand caps the number of distinct deduped
// patterns shown per severity band when the caller passes <= 0.
const defaultPatternsPerBand = 3
