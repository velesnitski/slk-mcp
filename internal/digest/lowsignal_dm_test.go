package digest

import (
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// ADR 128: short lines in a DM are the conversation, never status noise.
func TestDetectLowSignalChannel_NeverADM(t *testing.T) {
	short := func(n int) []goslack.Message {
		out := make([]goslack.Message, n)
		for i := range out {
			out[i].Text = "ok"
		}
		return out
	}
	dm := &slack.ChannelUnread{Messages: short(8)}
	dm.Channel.ID, dm.Channel.IsIM = "D1", true
	if DetectLowSignalChannel(dm) {
		t.Fatal("a DM of short lines was collapsed as low-signal")
	}
	ch := &slack.ChannelUnread{Messages: short(8)}
	ch.Channel.ID, ch.Channel.Name = "C1", "alpha"
	if !DetectLowSignalChannel(ch) {
		t.Fatal("a channel of short status lines must still collapse")
	}
}
