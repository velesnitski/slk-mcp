package digest

import (
	"fmt"
	"regexp"
	"strings"
)

// ZabbixAlert is the structured form of a Zabbix-style alert payload
// posted to a Slack monitoring channel. parseZabbixAlert returns a
// non-nil *ZabbixAlert iff the message looks like one — only then is
// OneLine() worth substituting for the raw body.
type ZabbixAlert struct {
	State    string // "Problem" | "Resolved" | "Resolved in 56s"
	Trigger  string // human-readable trigger description, first line
	Host     string // host name (deduplicated when shown twice)
	Severity string // Disaster | High | Average | Warning | Information
	Opdata   string // metric values: "load5=7.77, CPUs=2"
}

var (
	zabbixStateLineRe = regexp.MustCompile(`^(Problem|Resolved(?: in [^:]+)?):\s*(.+)$`)
	// Matches "FieldName" on one line, value on the next. Handles
	// the way Slack renders Zabbix alerts (label / newline / value).
	zabbixFieldRe = regexp.MustCompile(`(?m)^(Host|Severity|Opdata|Trigger description)\s*\n([^\n]+)`)
)

// parseZabbixAlert returns nil when text doesn't look like a Zabbix
// alert. Cheap rejection: requires both "Severity" and "Host" labels.
func parseZabbixAlert(text string) *ZabbixAlert {
	if !strings.Contains(text, "Severity") || !strings.Contains(text, "Host") {
		return nil
	}
	a := &ZabbixAlert{}

	firstLine := text
	if i := strings.IndexByte(text, '\n'); i > 0 {
		firstLine = text[:i]
	}
	if m := zabbixStateLineRe.FindStringSubmatch(firstLine); len(m) > 2 {
		a.State = m[1]
		a.Trigger = strings.TrimSpace(m[2])
	} else {
		a.Trigger = strings.TrimSpace(firstLine)
	}

	for _, m := range zabbixFieldRe.FindAllStringSubmatch(text, -1) {
		val := strings.TrimSpace(m[2])
		switch m[1] {
		case "Host":
			// Slack renders "host.name [host.name]" — strip the
			// bracketed duplicate.
			if i := strings.Index(val, "["); i > 0 {
				val = strings.TrimSpace(val[:i])
			}
			a.Host = val
		case "Severity":
			a.Severity = val
		case "Opdata":
			a.Opdata = val
		case "Trigger description":
			// Use the explicit trigger description if the first-line
			// version was empty or generic.
			if a.Trigger == "" {
				a.Trigger = val
			}
		}
	}
	return a
}

// OneLine returns a compact structured form suitable for the log-mode
// digest renderer. Empty fields are omitted to keep the line tight.
func (a *ZabbixAlert) OneLine() string {
	var b strings.Builder
	if a.State != "" {
		b.WriteString(a.State)
		b.WriteString(": ")
	}
	if a.Host != "" {
		b.WriteString(a.Host)
		b.WriteString(" — ")
	}
	if a.Trigger != "" {
		b.WriteString(a.Trigger)
	}
	if a.Severity != "" && a.Severity != "Information" {
		fmt.Fprintf(&b, " (sev %s)", a.Severity)
	}
	if a.Opdata != "" {
		fmt.Fprintf(&b, " [%s]", compactOpdata(a.Opdata))
	}
	return b.String()
}

// compactOpdata trims known-verbose opdata patterns. Currently:
//
//   - "Load averages(1m 5m 15m): (a b c), # of CPUs: N" →
//     "load5=b, CPUs=N"
//   - "Space used: A of B (P %)" → "P% (A of B)"
//
// Anything else is returned unchanged but truncated at 80 chars.
func compactOpdata(s string) string {
	if m := loadAvgRe.FindStringSubmatch(s); len(m) == 3 {
		return fmt.Sprintf("load5=%s, CPUs=%s", m[1], m[2])
	}
	if m := diskRe.FindStringSubmatch(s); len(m) == 4 {
		return fmt.Sprintf("%s%% (%s of %s)", strings.TrimSpace(m[3]),
			strings.TrimSpace(m[1]), strings.TrimSpace(m[2]))
	}
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}

var (
	loadAvgRe = regexp.MustCompile(`Load averages\([^)]+\):\s*\([^ ]+\s+([0-9.]+)\s+[0-9.]+\),\s*# of CPUs:\s*(\d+)`)
	diskRe    = regexp.MustCompile(`Space used:\s*([\d.]+\s*\w+)\s+of\s+([\d.]+\s*\w+)\s*\(([\d.]+\s*%)\)`)
)
