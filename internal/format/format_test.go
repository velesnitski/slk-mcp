package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestMessageLine_Truncation(t *testing.T) {
	long := strings.Repeat("x", MessageLineLimit+100)
	msg := goslack.Message{}
	msg.Timestamp = "1700000000.000000"
	msg.User = "U123"
	msg.Text = long

	out := MessageLine(msg, "alex")
	if !strings.Contains(out, "(+100 chars)") {
		t.Fatalf("expected truncation marker with exact count, got: %s", out)
	}
	if !strings.Contains(out, "[") || !strings.Contains(out, "alex") {
		t.Fatalf("expected header with time and user, got: %s", out)
	}
}

func TestMessageLine_CollapsesWhitespace(t *testing.T) {
	msg := goslack.Message{}
	msg.Timestamp = "1700000000.000000"
	msg.User = "U123"
	msg.Text = "line1\n\n\nline2  \t  line3"

	out := MessageLine(msg, "alex")
	if strings.Contains(out, "\n") {
		t.Fatalf("expected single-line output, got: %q", out)
	}
	if !strings.Contains(out, "line1 line2 line3") {
		t.Fatalf("expected collapsed whitespace, got: %q", out)
	}
}

func TestChannelDigest_HidesExtras(t *testing.T) {
	var msgs []goslack.Message
	for i := 0; i < 10; i++ {
		m := goslack.Message{}
		m.Timestamp = "1700000000.000000"
		m.User = "U1"
		m.Text = "msg"
		msgs = append(msgs, m)
	}
	out := ChannelDigest("dev", msgs, map[string]string{"U1": "alex"}, 3)
	if !strings.Contains(out, "+7 more messages") {
		t.Fatalf("expected +7 more marker, got: %s", out)
	}
}

func TestChannelDigest_Empty(t *testing.T) {
	out := ChannelDigest("dev", nil, nil, 5)
	if !strings.Contains(out, "(no activity)") {
		t.Fatalf("expected no activity marker, got: %s", out)
	}
}
