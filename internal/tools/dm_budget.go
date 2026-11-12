package tools

import (
	"fmt"
	"strings"

	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// readDMMessages is how many messages a DM shows when nothing in it is
// new from the other side – one that is in the sweep only because of
// dm_window_hours. The operator has read it; a few lines are enough to
// recall where it stands, and a long read conversation must not use up
// the budget that unread ones need (ADR 147).
const readDMMessages = 5

// dmOverflowLimit caps the one line a DM keeps when the max_chars
// budget has run out (ADR 147).
const dmOverflowLimit = 200

// dmPending reports whether a direct message holds something from the
// other side the operator has not read: a message or thread reply by
// someone else, newer than last_read. With no last_read every message
// by someone else counts. Pure.
func dmPending(r *slack.ChannelUnread, selfID string) bool {
	if !slack.IsDirectMessage(r.Channel) {
		return false
	}
	fresh := func(user, ts string) bool {
		if user == "" || user == selfID {
			return false
		}
		return r.LastRead == "" || tsAfter(ts, r.LastRead)
	}
	for _, m := range r.Messages {
		if fresh(m.User, m.Timestamp) {
			return true
		}
	}
	for _, rs := range r.Replies {
		for _, m := range rs {
			if fresh(m.User, m.Timestamp) {
				return true
			}
		}
	}
	return false
}

// dmOverflowLine is what a DM keeps when the max_chars budget is spent:
// its label, its volume, and the newest message from the other side (or
// the newest at all), cut to dmOverflowLimit, plus the call that reads
// it in full. A DM is never reduced to a bare name and a count – the
// one line is what tells the reader whether to open it (ADR 147). Pure.
func dmOverflowLine(label string, r *slack.ChannelUnread, users map[string]string, selfID, hint string) string {
	var pick *int
	for i := range r.Messages {
		m := r.Messages[i]
		if !format.HasContent(m) {
			continue
		}
		if m.User != selfID && m.User != "" {
			pick = &i
			break
		}
		if pick == nil {
			idx := i
			pick = &idx
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s (%s, cut by max_chars)\n", label, omittedCount(r))
	if pick != nil {
		m := r.Messages[*pick]
		line := format.MessageLine(m, users[m.User], users)
		if runes := []rune(line); len(runes) > dmOverflowLimit {
			line = string(runes[:dmOverflowLimit]) + "…"
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if hint != "" {
		b.WriteString("  read: " + hint)
	}
	return strings.TrimRight(b.String(), "\n")
}
