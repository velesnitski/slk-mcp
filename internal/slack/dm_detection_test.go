package slack

import (
	"testing"

	goslack "github.com/slack-go/slack"
)

// TestIsDirectMessage_flagsSetCorrectly is the happy-path: when Slack
// populates IsIM/IsMpIM, the helper trusts them.
func TestIsDirectMessage_flagsSetCorrectly(t *testing.T) {
	cases := []struct {
		name string
		ch   goslack.Channel
		want bool
	}{
		{
			name: "1:1 IM",
			ch:   chanWith("D01ABC", "", true, false),
			want: true,
		},
		{
			name: "multi-party DM",
			ch:   chanWith("G01XYZ", "mpdm-a--b--c-1", false, true),
			want: true,
		},
		{
			name: "public channel",
			ch:   chanWith("C01PUB", "general", false, false),
			want: false,
		},
		{
			name: "private channel (group, not mpdm)",
			ch:   chanWith("G01PRIV", "secret-project", false, false),
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDirectMessage(c.ch); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

// TestIsDirectMessage_fallsBackOnIDPrefix is the bug-fix case. Slack's
// users.conversations occasionally returns DM channel objects with
// IsIM=false (read-state-stale on the listing side). Without the
// fallback, RecentDMActivity silently skips them and the merge keeps
// the truncated unread-only view — exactly the silent-miss we hit
// when an outgoing-only DM didn't surface in the digest.
func TestIsDirectMessage_fallsBackOnIDPrefix(t *testing.T) {
	cases := []struct {
		name string
		ch   goslack.Channel
		want bool
	}{
		{
			name: "DM by ID prefix, IsIM not set",
			ch:   chanWith("D0DEADBEEF", "", false, false),
			want: true,
		},
		{
			name: "MPIM by ID + mpdm- name, IsMpIM not set",
			ch:   chanWith("G09MPDM01", "mpdm-alice--bob--carol-1", false, false),
			want: true,
		},
		{
			name: "G-prefixed without mpdm name is NOT a DM",
			ch:   chanWith("G01PRIV", "private-channel", false, false),
			want: false,
		},
		{
			name: "C-prefixed is never a DM",
			ch:   chanWith("C01PUB", "general", false, false),
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDirectMessage(c.ch); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func chanWith(id, name string, isIM, isMpIM bool) goslack.Channel {
	ch := goslack.Channel{}
	ch.ID = id
	ch.Name = name
	ch.IsIM = isIM
	ch.IsMpIM = isMpIM
	return ch
}
