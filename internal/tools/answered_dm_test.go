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

func msgFrom(user, ts string) goslack.Message {
	m := goslack.Message{}
	m.User = user
	m.Timestamp = ts
	return m
}

func TestIsAnsweredDM(t *testing.T) {
	self := "U0SELF11111"
	dm := dmUnread("D1", msgFrom("U0OTHER2222", "1.0"))
	mine := msgFrom(self, "2.0")
	theirs := msgFrom("U0OTHER2222", "2.0")

	if !isAnsweredDM(dm, &mine, self) {
		t.Error("operator's message as newest → answered")
	}
	if isAnsweredDM(dm, &theirs, self) {
		t.Error("counterpart's message as newest → NOT answered")
	}
	if isAnsweredDM(dm, nil, self) || isAnsweredDM(dm, &mine, "") {
		t.Error("nil latest / empty self must never mark answered")
	}
	if isAnsweredDM(chanUnread("C1"), &mine, self) {
		t.Error("regular channels are never 'answered DMs'")
	}
}

func TestDropAnsweredDMs_SplitsAndFailsOpen(t *testing.T) {
	self := "U0SELF11111"
	latest := map[string]goslack.Message{
		"D1": msgFrom(self, "5.0"),          // answered — drop
		"D2": msgFrom("U0OTHER2222", "5.0"), // live question — keep
	}
	latestFn := func(_ context.Context, chID string) (*goslack.Message, error) {
		if chID == "D3" {
			return nil, errors.New("probe failed")
		}
		m := latest[chID]
		return &m, nil
	}
	in := []*slack.ChannelUnread{
		dmUnread("D1", msgFrom("U0OTHER2222", "1.0")),
		dmUnread("D2", msgFrom("U0OTHER2222", "1.0")),
		dmUnread("D3", msgFrom("U0OTHER2222", "1.0")), // probe error — fail-open, keep
		chanUnread("C1"), // non-DM passes through, no probe
	}
	kept, answered := dropAnsweredDMs(context.Background(), latestFn, self, in)
	if len(answered) != 1 || answered[0].Channel.ID != "D1" {
		t.Fatalf("want D1 answered; got %+v", answered)
	}
	if len(kept) != 3 {
		t.Fatalf("want D2+D3+C1 kept; got %d", len(kept))
	}
	for _, cu := range kept {
		if cu.Channel.ID == "D1" {
			t.Fatal("answered DM leaked into kept set")
		}
	}
}

func TestDropAnsweredDMs_EmptySelfKeepsAll(t *testing.T) {
	latestFn := func(_ context.Context, _ string) (*goslack.Message, error) {
		t.Fatal("must not probe when self is unknown")
		return nil, nil
	}
	in := []*slack.ChannelUnread{dmUnread("D1", msgFrom("U0OTHER2222", "1.0"))}
	kept, answered := dropAnsweredDMs(context.Background(), latestFn, "", in)
	if len(kept) != 1 || len(answered) != 0 {
		t.Fatalf("empty self must keep everything; got kept=%d answered=%d", len(kept), len(answered))
	}
}
