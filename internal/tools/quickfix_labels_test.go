package tools

import (
	"context"
	"testing"

	goslack "github.com/slack-go/slack"
)

// Small output defects seen in live digests.

func TestChannelDisplayLabel_DMBuiltFromSearchIsNotAChannel(t *testing.T) {
	// A DM reconstructed from a search hit has no is_im flag and the
	// peer's user ID as its name. It rendered as "#U0…".
	f := newFakeSlack(t)
	tmUsers(f, map[string]string{"U0PEER0001": "sam"})
	h := newFakeHub(t, f)
	ch := goslack.Channel{GroupConversation: goslack.GroupConversation{
		Conversation: goslack.Conversation{ID: "D0DM000001"}, Name: "U0PEER0001",
	}}

	if got := channelDisplayLabel(context.Background(), ch, h.Users()); got != "@sam" {
		t.Fatalf("got %q, want @sam", got)
	}
}

func TestChannelDisplayLabel_OrdinaryChannelUnchanged(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)
	ch := goslack.Channel{GroupConversation: goslack.GroupConversation{Name: "general"}}

	if got := channelDisplayLabel(context.Background(), ch, h.Users()); got != "#general" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderFullMessage_BlockOnlyBotPostHasAnAuthor(t *testing.T) {
	msg := goslack.Message{Msg: goslack.Msg{
		Timestamp: "1700000000.000100", Text: "no time log for 24h",
		BotID: "B0BOT00001", BotProfile: &goslack.BotProfile{Name: "Task Alerts"},
	}}

	out := renderFullMessage(msg, nil, nil, "alpha", "#alerts", "")

	tmWant(t, out, "from: Task Alerts (bot) at ")
}

func TestRenderFullMessage_BotWithoutProfileStillNamed(t *testing.T) {
	msg := goslack.Message{Msg: goslack.Msg{Timestamp: "1700000000.000100", BotID: "B0BOT00002"}}

	tmWant(t, renderFullMessage(msg, nil, nil, "alpha", "#alerts", ""), "from: bot B0BOT00002")
}

func TestIsClosingAckText_NewClosers(t *testing.T) {
	for _, s := range []string{"Да", "да!", "Принял", "ладно", "yes", ":flushed:", ":+1: :tada:",
		"Sam Example has joined Slack – take a second to say hello."} {
		if !isClosingAckText(s) {
			t.Errorf("%q should count as a closer", s)
		}
	}
	for _, s := range []string{"Да, но когда релиз?", "Привет", "принял, сделаю завтра к обеду", "привет :wave: глянешь MR?"} {
		if isClosingAckText(s) {
			t.Errorf("%q carries an ask and must stay", s)
		}
	}
}
