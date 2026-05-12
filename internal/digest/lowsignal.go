package digest

import (
	"fmt"
	"sort"
	"strings"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

// lowSignalNameKeywords recognise channels that are typically
// status-only (presence checks, lunch markers, "+1" voting). Their
// per-message detail rarely carries information worth the tokens.
var lowSignalNameKeywords = []string{"checkin", "presence", "standup-status"}

// lowSignalShortMessageThreshold — average body length below which a
// channel is treated as low-signal in the absence of an explicit
// name match.
const lowSignalShortMessageThreshold = 16

// DetectLowSignalChannel reports whether a ChannelUnread is a
// status-update / "+ обед" style channel that should collapse to a
// one-line summary instead of per-message rendering.
//
// Two signals OR'd:
//
//   - Channel name contains a known low-signal keyword.
//   - At least 5 messages, no thread replies, and the average body
//     length is under lowSignalShortMessageThreshold characters.
func DetectLowSignalChannel(cu *slack.ChannelUnread) bool {
	name := strings.ToLower(cu.Channel.Name)
	for _, kw := range lowSignalNameKeywords {
		if strings.Contains(name, kw) {
			return true
		}
	}
	if len(cu.Messages) < 5 || len(cu.Replies) > 0 {
		return false
	}
	totalLen := 0
	for _, m := range cu.Messages {
		totalLen += len(strings.TrimSpace(m.Text))
	}
	if len(cu.Messages) == 0 {
		return false
	}
	return totalLen/len(cu.Messages) < lowSignalShortMessageThreshold
}

// RenderLowSignalChannel collapses a status-only channel into one
// line: header + count + a few unique authors. No per-message body.
func RenderLowSignalChannel(channelLabel string, cu *slack.ChannelUnread) string {
	authors := map[string]struct{}{}
	for _, m := range cu.Messages {
		if m.User != "" {
			authors[m.User] = struct{}{}
		}
	}
	names := make([]string, 0, len(authors))
	for a := range authors {
		names = append(names, a)
	}
	sort.Strings(names)

	short := names
	if len(short) > 4 {
		short = short[:4]
	}
	suffix := ""
	if len(names) > 4 {
		suffix = fmt.Sprintf(" + %d others", len(names)-4)
	}

	return fmt.Sprintf("## %s — %d short status updates from %d people (%s%s)",
		channelLabel, len(cu.Messages), len(authors),
		strings.Join(short, ", "), suffix)
}
