package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func digestFixture() []goslack.Message {
	var a, b goslack.Message
	a.Timestamp = "1714492800.000100"
	a.User = "U0EXAMPLE01"
	a.Text = "nightly job finished"
	b.Timestamp = "1714492900.000200"
	b.User = "U0EXAMPLE02"
	b.Text = "second line"
	return []goslack.Message{a, b}
}

// Without the option the digest stays byte-for-byte as before: no key,
// no extra width.
func TestChannelDigest_timestampsOffByDefault(t *testing.T) {
	out := ChannelDigest("#example-channel", digestFixture(), nil, 10)
	if strings.Contains(out, "ts=") {
		t.Fatalf("ts leaked into the default rendering: %q", out)
	}
}

// A digest line carries no key at all, so a reader can see a message
// but not cite or re-fetch it. WithMessageTimestamps makes each line
// addressable by get_message(channel, ts).
func TestChannelDigest_timestampsMakeLinesAddressable(t *testing.T) {
	out := ChannelDigest("#example-channel", digestFixture(), nil, 10, WithMessageTimestamps())
	for _, want := range []string{"ts=1714492800.000100", "ts=1714492900.000200"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in:\n%s", want, out)
		}
	}
}

// A message with no ts must not render a dangling "ts=".
func TestChannelDigest_timestampsSkipEmpty(t *testing.T) {
	var m goslack.Message
	m.User = "U0EXAMPLE01"
	m.Text = "nightly job finished"

	out := ChannelDigest("#example-channel", []goslack.Message{m}, nil, 10, WithMessageTimestamps())
	if strings.Contains(out, "ts=") {
		t.Fatalf("rendered an empty ts: %q", out)
	}
}
