package digest

import (
	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// mkChannelUnread builds a *slack.ChannelUnread test fixture with a
// deterministic LastRead and synthetic Channel.ID derived from the
// channel name. Test-only — never used by production code.
func mkChannelUnread(name string, msgs []goslack.Message, replies map[string][]goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{
		LastRead: "1700000000.000000",
		Messages: msgs,
		Replies:  replies,
	}
	cu.Channel.Name = name
	cu.Channel.ID = "C_" + name
	return cu
}
