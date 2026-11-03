package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

// ADR 132: a DM is addressed to the operator without an @-mention, so
// mentions_only keeps it.
func TestFilterMentions_KeepsDMs(t *testing.T) {
	dm := mkChannelUnread("dm", []goslack.Message{mkMsg("1", "U2", "принял")}, nil)
	dm.Channel.ID = "D_dm"
	dm.Channel.IsIM = true
	plain := mkChannelUnread("plain", []goslack.Message{mkMsg("1", "U2", "plain")}, nil)

	got := filterMentions([]*slack.ChannelUnread{dm, plain}, "U_SELF")
	if len(got) != 1 || got[0] != dm {
		t.Fatalf("filterMentions kept %d entries; want only the DM", len(got))
	}
}

// ADR 132: a channel cut by max_chars whose news is all in threads is
// listed with its replies, not as "(0)".
func TestOmittedCount(t *testing.T) {
	replies := map[string][]goslack.Message{"1": {mkMsg("2", "U2", "a"), mkMsg("3", "U3", "b")}}
	cases := []struct {
		cu   *slack.ChannelUnread
		want string
	}{
		{mkChannelUnread("a", []goslack.Message{mkMsg("1", "U2", "x")}, nil), "1"},
		{mkChannelUnread("b", nil, replies), "2 replies"},
		{mkChannelUnread("c", []goslack.Message{mkMsg("1", "U2", "x")}, replies), "1+2 replies"},
	}
	for _, c := range cases {
		if got := omittedCount(c.cu); got != c.want {
			t.Errorf("%s: omittedCount = %q, want %q", c.cu.Channel.Name, got, c.want)
		}
	}
}
