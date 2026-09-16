package tools

import "testing"

// get_thread takes the same permalink get_message takes. Routing them to
// different workspaces makes one of the two answer "channel_not_found"
// for a thread that plainly exists, and the error names the channel
// rather than the workspace, so the reader blames the wrong thing.
func TestPermalinkHost_identifiesTheWorkspaceHost(t *testing.T) {
	cases := []struct{ link, want string }{
		{"https://example-one.slack.com/archives/C0EXAMPLE01/p1714492800000100", "example-one.slack.com"},
		{"https://example-two.slack.com/archives/D0EXAMPLE01/p1714492800000100?thread_ts=1714492800.000100", "example-two.slack.com"},
		{"", ""},
		{"not-a-url", ""},
	}
	for _, c := range cases {
		if got := permalinkHost(c.link); got != c.want {
			t.Errorf("permalinkHost(%q) = %q, want %q", c.link, got, c.want)
		}
	}
}

// The thread header carried the same doubled-sigil defect the digest
// heading had: a DM rendered as "#@person", a channel as "##name".
func TestConversationLabel_threadHeaderShapes(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"example-channel", "#example-channel"},
		{"#example-channel", "#example-channel"},
		{"@example.person", "@example.person"},
		{"C0EXAMPLE01", "C0EXAMPLE01"},
	}
	for _, c := range cases {
		if got := conversationLabel(c.ref); got != c.want {
			t.Errorf("conversationLabel(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}
