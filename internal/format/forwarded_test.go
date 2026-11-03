package format

import (
	"encoding/json"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// forward builds the attachment Slack gives a forwarded message.
func forward(authorID, authorName, text string) goslack.Attachment {
	return goslack.Attachment{
		AuthorID:   authorID,
		AuthorName: authorName,
		Text:       text,
		Fallback:   "[November 2nd] " + authorName + ": " + text,
		FromURL:    "https://example.slack.com/archives/D0000000001/p1700000000000100",
		Ts:         json.Number("1700000000.000100"),
	}
}

// ADR 132: a forward is labelled with its author, so it does not read as
// the forwarder's own words.
func TestMessageLine_ForwardNamesTheAuthor(t *testing.T) {
	msg := goslack.Message{Msg: goslack.Msg{
		Timestamp:   "1700000100.000000",
		User:        "U_ME",
		Attachments: []goslack.Attachment{forward("U_FW", "Dana", "Привет, Принял:ok_hand::skin-tone-2:")},
	}}
	got := MessageLine(msg, "me", map[string]string{"U_FW": "Dana R"})
	if !strings.Contains(got, "↪ Dana R: Привет, Принял") {
		t.Fatalf("forward must name its author:\n%s", got)
	}
}

func TestMessageLine_ForwardWithCommentKeepsBoth(t *testing.T) {
	msg := goslack.Message{Msg: goslack.Msg{
		Timestamp:   "1700000100.000000",
		User:        "U_ME",
		Text:        "see this",
		Attachments: []goslack.Attachment{forward("", "Dana", "the plan is ready")},
	}}
	got := MessageLine(msg, "me")
	if !strings.Contains(got, "see this ↪ Dana: the plan is ready") {
		t.Fatalf("a commented forward must keep the forwarded text:\n%s", got)
	}
}

func TestMessageLine_ForwardFallsBackToFallback(t *testing.T) {
	a := forward("", "", "")
	a.Fallback = "[date] someone: hi"
	msg := goslack.Message{Msg: goslack.Msg{Timestamp: "1700000100.000000", Attachments: []goslack.Attachment{a}}}
	if got := MessageLine(msg, "me"); !strings.Contains(got, "↪ forwarded: [date] someone: hi") {
		t.Fatalf("got %s", got)
	}
}

// A bot card may carry ts and author_name; without a message permalink it
// is not a forward and renders as before.
func TestMessageLine_BotCardIsNotAForward(t *testing.T) {
	msg := goslack.Message{Msg: goslack.Msg{
		Timestamp: "1700000100.000000",
		Attachments: []goslack.Attachment{{
			AuthorName: "Monitor", Text: "disk full", Ts: json.Number("1700000000"),
			FromURL: "https://monitor.example.com/alert/1",
		}},
	}}
	got := MessageLine(msg, "bot")
	if strings.Contains(got, "↪") || !strings.Contains(got, "disk full") {
		t.Fatalf("got %s", got)
	}
}

// ADR 132: the same text with and without a skin tone is one part.
func TestHiddenPayload_DedupsSkinTones(t *testing.T) {
	msg := goslack.Message{Msg: goslack.Msg{Attachments: []goslack.Attachment{
		{Text: "ok :ok_hand::skin-tone-2:"},
		{Text: "ok :ok_hand:"},
	}}}
	if got := HiddenPayload(msg); strings.Contains(got, " · ") {
		t.Fatalf("duplicate kept: %s", got)
	}
}
