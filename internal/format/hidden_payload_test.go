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

func TestChannelDigest_aggregatesHuddleNoise(t *testing.T) {
	real := goslack.Message{}
	real.Timestamp = "1700000000.000000"
	real.User = "U001"
	real.Text = "real message"

	huddle := func(ts string) goslack.Message {
		m := goslack.Message{}
		m.Timestamp = ts
		m.SubType = HuddleSubtype
		m.Blocks.BlockSet = []goslack.Block{nil}
		return m
	}
	msgs := []goslack.Message{real, huddle("1.1"), huddle("1.2"), huddle("1.3")}

	got := ChannelDigest("#general", msgs, nil, 50, WithHuddleAggregation())
	if strings.Contains(got, "[huddle]") {
		t.Fatalf("huddle pings should be aggregated, not rendered as lines; got:\n%s", got)
	}
	if !strings.Contains(got, "· 3 huddles") {
		t.Fatalf("expected aggregated huddle count; got:\n%s", got)
	}
	if !strings.Contains(got, "(1 msgs)") {
		t.Fatalf("header count should exclude aggregated huddles; got:\n%s", got)
	}
}

func TestChannelDigest_noAggregationKeepsHuddleInline(t *testing.T) {
	// Without the option (DM path), a huddle stays inline — that's the call signal.
	h := goslack.Message{}
	h.Timestamp = "1700000000.000000"
	h.User = "U001"
	h.SubType = HuddleSubtype
	h.Blocks.BlockSet = []goslack.Block{nil}
	got := ChannelDigest("@alice", []goslack.Message{h}, nil, 50)
	if !strings.Contains(got, "[huddle]") {
		t.Fatalf("DM huddle should render inline without aggregation; got:\n%s", got)
	}
}

func TestChannelDigest_huddleWithRepliesNotAggregated(t *testing.T) {
	h := goslack.Message{}
	h.Timestamp = "1700000000.000000"
	h.User = "U001"
	h.SubType = HuddleSubtype
	h.Blocks.BlockSet = []goslack.Block{nil}
	reply := goslack.Message{}
	reply.Timestamp = "1700000001.000000"
	reply.User = "U002"
	reply.Text = "add me to the call"
	replies := map[string][]goslack.Message{h.Timestamp: {reply}}

	got := ChannelDigest("#qa", []goslack.Message{h}, nil, 50,
		WithThreadReplies(replies), WithHuddleAggregation())
	if strings.Contains(got, "· 1 huddle") {
		t.Fatalf("a huddle with replies must NOT be aggregated; got:\n%s", got)
	}
	if !strings.Contains(got, "add me to the call") {
		t.Fatalf("the reply (the signal) should render; got:\n%s", got)
	}
}

func TestChannelDigest_huddleOnlyChannel_droppedWhenOmitEmpty(t *testing.T) {
	h := goslack.Message{}
	h.Timestamp = "1.1"
	h.SubType = HuddleSubtype
	h.Blocks.BlockSet = []goslack.Block{nil}
	got := ChannelDigest("#team-alpha", []goslack.Message{h}, nil, 50,
		WithHuddleAggregation(), WithOmitEmpty())
	if got != "" {
		t.Fatalf("a huddle-only channel should be dropped in sweeps; got %q", got)
	}
}

func TestMessageLineFull_skipsTruncation(t *testing.T) {
	long := strings.Repeat("x", 1000)
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = "U001"
	m.Text = long

	trunc := MessageLine(m, "alice")
	if !strings.Contains(trunc, "chars)") {
		t.Fatalf("MessageLine should truncate a long body with a (+N chars) marker; got %q", trunc)
	}
	if strings.Contains(trunc, long) {
		t.Fatal("MessageLine must not contain the full long body")
	}

	full := MessageLineFull(m, "alice")
	if !strings.Contains(full, long) {
		t.Fatal("MessageLineFull must contain the complete body")
	}
	if strings.Contains(full, "chars)") {
		t.Fatalf("MessageLineFull must not append a truncation marker; got %q", full)
	}
}

func TestChannelDigest_WithFullText(t *testing.T) {
	long := strings.Repeat("y", 1000)
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = "U001"
	m.Text = long

	full := ChannelDigest("#x", []goslack.Message{m}, nil, 50, WithFullText())
	if !strings.Contains(full, long) {
		t.Fatal("ChannelDigest WithFullText should render the full body")
	}
	def := ChannelDigest("#x", []goslack.Message{m}, nil, 50)
	if strings.Contains(def, long) {
		t.Fatal("default ChannelDigest should truncate the long body")
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
