package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// Bot-driven channels post with an empty text field and everything the
// human reads inside an attachment. Rendering only "[attached: 1]" made
// those channels unreadable through this server.

func msgWith(atts ...goslack.Attachment) goslack.Message {
	m := goslack.Message{}
	m.Attachments = atts
	return m
}

func TestHiddenPayload_LiftsAttachmentTitle(t *testing.T) {
	m := msgWith(goslack.Attachment{
		Title:     "Nightly job finished with 3 warnings",
		TitleLink: "https://example.invalid/run/1",
		Fallback:  "Nightly job finished with 3 warnings",
	})
	got := renderHiddenPayloadMarker(m)
	if !strings.Contains(got, "Nightly job finished with 3 warnings") {
		t.Fatalf("title must be surfaced; got %q", got)
	}
	if strings.Contains(got, "[attached:") {
		t.Errorf("a counter must not accompany real text; got %q", got)
	}
}

func TestHiddenPayload_FallsBackToFallbackText(t *testing.T) {
	// The common shape: no title, no text, prose only in `fallback`.
	m := msgWith(goslack.Attachment{Fallback: "Queue depth above threshold"})
	got := renderHiddenPayloadMarker(m)
	if got != "Queue depth above threshold" {
		t.Fatalf("fallback should be the whole marker; got %q", got)
	}
}

func TestHiddenPayload_ReadsBlocksNestedInAttachment(t *testing.T) {
	att := goslack.Attachment{
		Fallback: "summary line",
		Blocks: goslack.Blocks{BlockSet: []goslack.Block{
			goslack.NewSectionBlock(goslack.NewTextBlockObject("mrkdwn", "detail from the section block", false, false), nil, nil),
		}},
	}
	got := renderHiddenPayloadMarker(msgWith(att))
	if !strings.Contains(got, "detail from the section block") {
		t.Fatalf("nested block text must be read; got %q", got)
	}
	if strings.Contains(got, "summary line") {
		t.Errorf("fallback is a last resort, not an addition; got %q", got)
	}
}

func TestHiddenPayload_RendersAttachmentFields(t *testing.T) {
	m := msgWith(goslack.Attachment{
		Fallback: "ignored",
		Fields: []goslack.AttachmentField{
			{Title: "Status", Value: "degraded"},
			{Title: "Duration", Value: "4m"},
		},
	})
	got := renderHiddenPayloadMarker(m)
	for _, want := range []string{"Status degraded", "Duration 4m"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing field %q in %q", want, got)
		}
	}
}

func TestHiddenPayload_DeduplicatesRepeatedText(t *testing.T) {
	// Slack sets `fallback` to a copy of the title on most bot posts;
	// printing both would double every alert line.
	m := msgWith(goslack.Attachment{Title: "Same line", Fallback: "Same line", Text: "Same line"})
	if got := renderHiddenPayloadMarker(m); got != "Same line" {
		t.Fatalf("identical fields must collapse to one; got %q", got)
	}
}

func TestHiddenPayload_TruncatesVerboseAttachments(t *testing.T) {
	m := msgWith(goslack.Attachment{Text: strings.Repeat("x", HiddenPayloadLimit+120)})
	got := renderHiddenPayloadMarker(m)
	if len(got) > HiddenPayloadLimit+40 {
		t.Fatalf("marker must be capped; got %d chars", len(got))
	}
	if !strings.Contains(got, "+120 chars") {
		t.Errorf("truncation must report the exact overflow; got %q", got)
	}
}

func TestHiddenPayload_KeepsCounterWhenThereIsNoText(t *testing.T) {
	// An attachment with nothing readable still has to leave a mark, or
	// the line renders empty and the reader never learns to follow the
	// permalink.
	m := msgWith(goslack.Attachment{}, goslack.Attachment{})
	if got := renderHiddenPayloadMarker(m); got != "[attached: 2]" {
		t.Fatalf("textless attachments keep the count; got %q", got)
	}
}

func TestHiddenPayload_HuddleStillWins(t *testing.T) {
	m := msgWith(goslack.Attachment{Fallback: "should not be shown"})
	m.SubType = HuddleSubtype
	if got := renderHiddenPayloadMarker(m); got != "[huddle]" {
		t.Fatalf("huddle detection must precede text lifting; got %q", got)
	}
}
