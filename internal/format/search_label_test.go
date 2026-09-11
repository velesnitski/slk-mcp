package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// Slack leaves channel.name empty for a DM and parks the counterpart's
// user ID there instead, so the old "#"+name rendering printed a
// channel that does not exist, named after a raw user ID.
func TestSearchChannelLabel_dmIsNotAChannel(t *testing.T) {
	ch := goslack.CtxChannel{ID: "D0EXAMPLE01", Name: "U0EXAMPLE99"}

	bare := SearchChannelLabel(ch, nil)
	if strings.HasPrefix(bare, "#") {
		t.Fatalf("a DM must not be labelled as a channel: %q", bare)
	}
	if bare != "@U0EXAMPLE99" {
		t.Fatalf("want the bare id behind @, got %q", bare)
	}

	named := SearchChannelLabel(ch, map[string]string{"U0EXAMPLE99": "example.person"})
	if named != "@example.person" {
		t.Fatalf("want the resolved handle, got %q", named)
	}
}

func TestSearchChannelLabel_mpimAndChannel(t *testing.T) {
	mpim := goslack.CtxChannel{ID: "G0EXAMPLE01", Name: "U0EXAMPLE99", IsMPIM: true}
	if got := SearchChannelLabel(mpim, nil); got != "@U0EXAMPLE99" {
		t.Fatalf("mpim label wrong: %q", got)
	}

	pub := goslack.CtxChannel{ID: "C0EXAMPLE01", Name: "example-channel"}
	if got := SearchChannelLabel(pub, nil); got != "#example-channel" {
		t.Fatalf("channel label wrong: %q", got)
	}
}

// A channel hit must be unaffected by the DM name map.
func TestSearchResultExt_channelHitKeepsHash(t *testing.T) {
	m := goslack.SearchMessage{Permalink: "https://example.invalid/archives/C0EXAMPLE01/p1714492800000000"}
	m.Timestamp = "1714492800.000000"
	m.Channel.ID = "C0EXAMPLE01"
	m.Channel.Name = "example-channel"
	m.Username = "example.person"
	m.Text = "nightly job finished"

	line := SearchResultExt(m, false, map[string]string{"U0EXAMPLE99": "someone"})
	if !strings.Contains(line, "#example-channel") {
		t.Fatalf("channel hit lost its name: %q", line)
	}
}

func TestSearchResultExt_dmHitShowsHandle(t *testing.T) {
	m := goslack.SearchMessage{Permalink: "https://example.invalid/archives/D0EXAMPLE01/p1714492800000000"}
	m.Timestamp = "1714492800.000000"
	m.Channel.ID = "D0EXAMPLE01"
	m.Channel.Name = "U0EXAMPLE99"
	m.Username = "example.person"
	m.Text = "nightly job finished"

	line := SearchResultExt(m, false, map[string]string{"U0EXAMPLE99": "example.peer"})
	if !strings.Contains(line, "@example.peer") {
		t.Fatalf("DM hit did not name its counterpart: %q", line)
	}
	if strings.Contains(line, "#U0EXAMPLE99") {
		t.Fatalf("DM hit still rendered as a channel: %q", line)
	}
}
