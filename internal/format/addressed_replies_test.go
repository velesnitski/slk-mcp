package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func addrMsg(ts, user, text, thread string) goslack.Message {
	var m goslack.Message
	m.Timestamp, m.User, m.Text, m.ThreadTimestamp = ts, user, text, thread
	return m
}

func TestAddressedReplies_RenderFullerThanTheChannelPreview(t *testing.T) {
	long := strings.Repeat("слово ", 100) // ~600 chars, over the 280 preview
	const self = "U0SELF"
	parents := []goslack.Message{
		addrMsg("100.000001", "U0OTHER", "my-thread parent", ""),
		addrMsg("200.000001", "U0OTHER", "their thread", ""),
		addrMsg("300.000001", "U0OTHER", "backstop thread", ""),
	}
	replies := map[string][]goslack.Message{
		// The operator replied earlier: the later reply is addressed to them.
		"100.000001": {addrMsg("100.000002", self, "my reply", "100.000001"), addrMsg("100.000003", "U0OTHER", "A"+long, "100.000001")},
		// Nobody addresses the operator: compact preview.
		"200.000001": {addrMsg("200.000002", "U0OTHER", "B"+long, "200.000001")},
		// Marked by the own-thread backstop, no own post in view.
		"300.000001": {addrMsg("300.000002", "U0OTHER", "C"+long, "300.000001")},
	}
	out := ChannelDigest("ch", parents, nil, 10,
		WithMentionHighlight(self),
		WithThreadReplies(replies),
		WithAddressedReplies(map[string]bool{"300.000001": true}, 1500))

	line := func(prefix string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, prefix+"слово") {
				return l
			}
		}
		t.Fatalf("no line for %s:\n%s", prefix, out)
		return ""
	}
	if strings.Contains(line("A"), "chars)") || strings.Contains(line("C"), "chars)") {
		t.Fatalf("addressed replies must not be cut to the preview:\n%s", out)
	}
	if !strings.Contains(line("B"), "chars)") {
		t.Fatalf("an unaddressed reply keeps the compact preview:\n%s", out)
	}
}

func TestAddressedReplies_MentionAndOrphanBlock(t *testing.T) {
	long := strings.Repeat("x ", 300)
	const self = "U0SELF"
	replies := map[string][]goslack.Message{
		"100.000001": {addrMsg("100.000002", "U0OTHER", "<@U0SELF> "+long, "100.000001")},
	}
	out := ThreadRepliesBlock(replies, nil, WithMentionHighlight(self), WithAddressedReplies(nil, 1500))
	if strings.Contains(out, "chars)") {
		t.Fatalf("a reply mentioning the operator is addressed to them:\n%s", out)
	}
	if out2 := ThreadRepliesBlock(replies, nil, WithMentionHighlight(self)); !strings.Contains(out2, "chars)") {
		t.Fatalf("without the option the preview cap stays:\n%s", out2)
	}
}
