package slack

import (
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestCountUnread(t *testing.T) {
	msg := func(ts, user string) goslack.Message {
		m := goslack.Message{}
		m.Timestamp, m.User = ts, user
		return m
	}
	page := []goslack.Message{msg("300.0", "U2"), msg("200.0", "U1"), msg("100.0", "U2")}
	if n, c := countUnread(page, "150.0", "U1"); n == nil || n.Timestamp != "300.0" || c != 1 {
		t.Fatalf("own message counted or newest wrong: %v %d", n, c)
	}
	if _, c := countUnread(page, "", "U1"); c != 0 {
		t.Fatal("no marker must count nothing")
	}
	if n, c := countUnread(nil, "150.0", "U1"); n != nil || c != 0 {
		t.Fatal("empty page")
	}
}
