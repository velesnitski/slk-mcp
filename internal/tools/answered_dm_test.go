package tools

import (
	"context"
	"errors"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func dmUnread(chID string, msgs ...goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{}
	cu.Channel.ID = chID
	cu.Channel.IsIM = true
	cu.Messages = msgs
	return cu
}

func chanUnread(chID string) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{}
	cu.Channel.ID = chID
	cu.Channel.IsChannel = true
	return cu
}

func msgFromText(user, ts, text string) goslack.Message {
	m := goslack.Message{}
	m.User = user
	m.Timestamp = ts
	m.Text = text
	return m
}

func msgFrom(user, ts string) goslack.Message {
	return msgFromText(user, ts, "real content")
}

func TestIsAnsweredDM_WindowSemantics(t *testing.T) {
	self := "U0SELF11111"
	other := "U0OTHER2222"
	dm := dmUnread("D1", msgFrom(other, "1.0"))

	// Operator holds the last word → answered.
	if !isAnsweredDM(dm, []goslack.Message{msgFrom(self, "3.0"), msgFrom(other, "2.0")}, self) {
		t.Error("operator-last → answered")
	}
	// Counterpart's ack tail after operator's reply → still answered.
	win := []goslack.Message{
		msgFromText(other, "4.0", "Спасибо! Прилетел"),
		msgFrom(self, "3.0"),
		msgFrom(other, "2.0"),
	}
	if !isAnsweredDM(dm, win, self) {
		t.Error("ack tail after operator's reply → answered")
	}
	// Live counterpart content after the reply → NOT answered.
	win[0] = msgFromText(other, "4.0", "а когда будет доступ?")
	if isAnsweredDM(dm, win, self) {
		t.Error("live follow-up must keep the DM visible")
	}
	// Operator absent from window → not answered.
	if isAnsweredDM(dm, []goslack.Message{msgFrom(other, "2.0")}, self) {
		t.Error("no operator message in window → not answered")
	}
	// Guards: empty window / empty self / non-DM.
	if isAnsweredDM(dm, nil, self) || isAnsweredDM(dm, win, "") {
		t.Error("nil window / empty self must never mark answered")
	}
	if isAnsweredDM(chanUnread("C1"), []goslack.Message{msgFrom(self, "9.0")}, self) {
		t.Error("regular channels are never 'answered DMs'")
	}
}

func TestIsClosingAckText(t *testing.T) {
	for _, ack := range []string{"Спасибо!", "спасибо", "thx", "ok)", "+1", "Спасибо! Прилетел", "Ок, принял", "done"} {
		if !isClosingAckText(ack) {
			t.Errorf("%q should be a closing ack", ack)
		}
	}
	for _, live := range []string{"спасибо, а когда будет доступ?", "Спасибо! Но есть проблема с логами и сервером", "когда результаты?", "нужна помощь"} {
		if isClosingAckText(live) {
			t.Errorf("%q must NOT be a closing ack", live)
		}
	}
}

func TestDropAnsweredDMs_SplitsAndFailsOpen(t *testing.T) {
	self := "U0SELF11111"
	other := "U0OTHER2222"
	windows := map[string][]goslack.Message{
		"D1": {msgFromText(other, "6.0", "Спасибо! Прилетел"), msgFrom(self, "5.0")}, // ack tail — drop
		"D2": {msgFrom(other, "5.0"), msgFrom(self, "4.0")},                          // live question — keep
	}
	recentFn := func(_ context.Context, chID string) ([]goslack.Message, error) {
		if chID == "D3" {
			return nil, errors.New("probe failed")
		}
		return windows[chID], nil
	}
	in := []*slack.ChannelUnread{
		dmUnread("D1", msgFrom(other, "1.0")),
		dmUnread("D2", msgFrom(other, "1.0")),
		dmUnread("D3", msgFrom(other, "1.0")), // probe error — fail-open, keep
		chanUnread("C1"),                      // non-DM passes through, no probe
	}
	kept, answered := dropAnsweredDMs(context.Background(), recentFn, self, in)
	if len(answered) != 1 || answered[0].Channel.ID != "D1" {
		t.Fatalf("want D1 answered; got %+v", answered)
	}
	if len(kept) != 3 {
		t.Fatalf("want D2+D3+C1 kept; got %d", len(kept))
	}
}

func TestDropAnsweredDMs_EmptySelfKeepsAll(t *testing.T) {
	recentFn := func(_ context.Context, _ string) ([]goslack.Message, error) {
		t.Fatal("must not probe when self is unknown")
		return nil, nil
	}
	in := []*slack.ChannelUnread{dmUnread("D1", msgFrom("U0OTHER2222", "1.0"))}
	kept, answered := dropAnsweredDMs(context.Background(), recentFn, "", in)
	if len(kept) != 1 || len(answered) != 0 {
		t.Fatalf("empty self must keep everything; got kept=%d answered=%d", len(kept), len(answered))
	}
}
