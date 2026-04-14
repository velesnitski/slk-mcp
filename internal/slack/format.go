package slack

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
)

func FormatMessage(msg goslack.Message, userName string) string {
	ts := ParseTS(msg.Timestamp)
	timeStr := ts.Format("15:04")

	thread := ""
	if msg.ReplyCount > 0 {
		thread = fmt.Sprintf(" (%d replies)", msg.ReplyCount)
	}

	return fmt.Sprintf("**%s** %s%s\n%s", userName, timeStr, thread, msg.Text)
}

func FormatDigest(channelName string, messages []goslack.Message, userNames map[string]string) string {
	if len(messages) == 0 {
		return fmt.Sprintf("## #%s\nNo activity.", channelName)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## #%s\n%d messages\n\n", channelName, len(messages))

	for _, msg := range messages {
		name := userNames[msg.User]
		if name == "" {
			name = msg.User
		}
		b.WriteString(FormatMessage(msg, name))
		b.WriteString("\n\n")
	}

	return b.String()
}

func FormatDecision(msg goslack.Message, channelName, userName, reason string) string {
	ts := ParseTS(msg.Timestamp)
	dateStr := ts.Format("2006-01-02 15:04")

	text := msg.Text
	if len(text) > 200 {
		text = text[:200]
	}

	return fmt.Sprintf("- **#%s** %s (%s) [%s]\n  %s", channelName, dateStr, userName, reason, text)
}

func ParseTS(ts string) time.Time {
	parts := strings.Split(ts, ".")
	if len(parts) == 0 {
		return time.Time{}
	}
	sec, _ := strconv.ParseInt(parts[0], 10, 64)
	return time.Unix(sec, 0)
}
