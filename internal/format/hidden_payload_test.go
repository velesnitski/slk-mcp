package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestRenderHiddenPayloadMarker_emptyMessageReturnsEmpty(t *testing.T) {
	m := goslack.Message{}
	if got := renderHiddenPayloadMarker(m); got != "" {
		t.Fatalf("expected empty marker for empty message; got %q", got)
	}
}

func TestRenderHiddenPayloadMarker_attachmentsOnly(t *testing.T) {
	m := goslack.Message{}
	m.Attachments = []goslack.Attachment{{}, {}, {}}
	got := renderHiddenPayloadMarker(m)
	if !strings.Contains(got, "[attached: 3]") {
		t.Fatalf("expected attachment count marker; got %q", got)
	}
}

func TestRenderHiddenPayloadMarker_blocksOnly(t *testing.T) {
	m := goslack.Message{}
	m.Blocks.BlockSet = []goslack.Block{nil, nil}
	got := renderHiddenPayloadMarker(m)
	if !strings.Contains(got, "[blocks: 2]") {
		t.Fatalf("expected blocks count marker; got %q", got)
	}
}

func TestRenderHiddenPayloadMarker_both(t *testing.T) {
	m := goslack.Message{}
	m.Attachments = []goslack.Attachment{{}}
	m.Blocks.BlockSet = []goslack.Block{nil}
	got := renderHiddenPayloadMarker(m)
	if !strings.Contains(got, "[attached: 1]") || !strings.Contains(got, "[blocks: 1]") {
		t.Fatalf("expected both markers; got %q", got)
	}
}

func TestMessageLine_flagsHiddenPayloadWhenBodyEmpty(t *testing.T) {
	// The bug we're fixing: an empty-text message with an attached
	// payload rendered as effectively blank.
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = "U001"
	m.Attachments = []goslack.Attachment{{}, {}}
	got := MessageLine(m, "alice")
	if !strings.Contains(got, "[attached: 2]") {
		t.Fatalf("MessageLine should mark hidden attachments when body empty; got %q", got)
	}
}

func TestMessageLine_doesNotFlagWhenBodyHasText(t *testing.T) {
	// URL-preview messages set both Text and Attachments. We must
	// not append `[attached: N]` for those — it's noise.
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = "U001"
	m.Text = "check this https://example.com"
	m.Attachments = []goslack.Attachment{{}}
	got := MessageLine(m, "alice")
	if strings.Contains(got, "[attached:") {
		t.Fatalf("MessageLine must not flag attachments when body is non-empty; got %q", got)
	}
}

func TestRenderHiddenPayloadMarker_huddleBeatsBlocks(t *testing.T) {
	// A huddle arrives as a block-kit message with empty text. It must
	// render as "[huddle]", not the opaque "[blocks: N]".
	m := goslack.Message{}
	m.SubType = HuddleSubtype
	m.Blocks.BlockSet = []goslack.Block{nil}
	if got := renderHiddenPayloadMarker(m); got != "[huddle]" {
		t.Fatalf("huddle should render as [huddle], not blocks; got %q", got)
	}
}

func TestMessageLine_rendersHuddle(t *testing.T) {
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = "U001"
	m.SubType = HuddleSubtype
	m.Blocks.BlockSet = []goslack.Block{nil}
	got := MessageLine(m, "alice")
	if !strings.Contains(got, "[huddle]") || strings.Contains(got, "[blocks") {
		t.Fatalf("MessageLine should surface huddle, not blocks; got %q", got)
	}
}

func TestHasContent_huddleWithoutBlocks(t *testing.T) {
	// Even a huddle with neither text nor blocks must survive the filter.
	m := goslack.Message{}
	m.SubType = HuddleSubtype
	if !HasContent(m) {
		t.Fatal("HasContent should be true for a huddle even with no text/blocks")
	}
}

func TestHasContent_attachmentsOnly(t *testing.T) {
	m := goslack.Message{}
	m.Attachments = []goslack.Attachment{{}}
	if !HasContent(m) {
		t.Fatal("HasContent should be true when only Attachments present")
	}
}

func TestHasContent_blocksOnly(t *testing.T) {
	m := goslack.Message{}
	m.Blocks.BlockSet = []goslack.Block{nil}
	if !HasContent(m) {
		t.Fatal("HasContent should be true when only Blocks present")
	}
}
