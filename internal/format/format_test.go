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

func TestMentionsUser(t *testing.T) {
	cases := []struct {
		name string
		text string
		uid  string
		want bool
	}{
		{"direct mention", "ping <@U001> please", "U001", true},
		{"different user", "ping <@U002> please", "U001", false},
		{"empty uid", "ping <@U001>", "", false},
		{"empty text", "", "U001", false},
		{"substring guard", "<@U0010> not a match", "U001", false},
		{"unicode body still matches", "Привет <@U001>!", "U001", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := goslack.Message{}
			m.Text = c.text
			if got := MentionsUser(m, c.uid); got != c.want {
				t.Fatalf("MentionsUser(%q, %q) = %v; want %v", c.text, c.uid, got, c.want)
			}
		})
	}
}

func TestChannelDigest_HighlightsMentions(t *testing.T) {
	mk := func(text string) goslack.Message {
		m := goslack.Message{}
		m.Timestamp = "1700000000.000000"
		m.User = "U002"
		m.Text = text
		return m
	}
	msgs := []goslack.Message{
		mk("hello team"),
		mk("ping <@U001> can you check this"),
	}
	out := ChannelDigest("dev", msgs, map[string]string{"U002": "alex"}, 10,
		WithMentionHighlight("U001"),
	)

	if strings.Count(out, MentionMarker) != 1 {
		t.Fatalf("expected exactly one mention marker, got: %s", out)
	}
	// The marker must precede the mentioning line, not the plain one.
	plainIdx := strings.Index(out, "hello team")
	mentionIdx := strings.Index(out, "ping <@U001>")
	markerIdx := strings.Index(out, MentionMarker)
	if !(plainIdx < markerIdx && markerIdx < mentionIdx) {
		t.Fatalf("marker should be on the mentioning line; plain=%d marker=%d mention=%d output:\n%s",
			plainIdx, markerIdx, mentionIdx, out)
	}
}

func TestChannelDigest_NoSelfIDNoMarkers(t *testing.T) {
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = "U002"
	m.Text = "ping <@U001>"
	out := ChannelDigest("dev", []goslack.Message{m}, nil, 5,
		WithMentionHighlight(""),
	)
	if strings.Contains(out, MentionMarker) {
		t.Fatalf("empty selfID should disable markers, got: %s", out)
	}
}

func TestChannelDigest_InlinesThreadReplies(t *testing.T) {
	parent := goslack.Message{}
	parent.Timestamp = "1700000100.000000"
	parent.User = "U002"
	parent.Text = "starting thread"

	mkReply := func(ts, user, text string) goslack.Message {
		m := goslack.Message{}
		m.Timestamp = ts
		m.User = user
		m.Text = text
		return m
	}
	replies := []goslack.Message{
		mkReply("1700000200.000000", "U003", "first reply"),
		mkReply("1700000300.000000", "U004", "second reply with <@U001>"),
	}

	users := map[string]string{
		"U002": "alex", "U003": "bob", "U004": "carol",
	}
	out := ChannelDigest("dev", []goslack.Message{parent}, users, 10,
		WithMentionHighlight("U001"),
		WithThreadReplies(map[string][]goslack.Message{
			parent.Timestamp: replies,
		}),
	)

	if !strings.Contains(out, ReplyIndent+"[") {
		t.Fatalf("expected reply indent in output, got:\n%s", out)
	}
	if !strings.Contains(out, "first reply") || !strings.Contains(out, "second reply with <@U001>") {
		t.Fatalf("expected both reply bodies in output, got:\n%s", out)
	}
	// Mention marker must apply to the reply containing <@U001>, not to others.
	markerCount := strings.Count(out, MentionMarker)
	if markerCount != 1 {
		t.Fatalf("expected exactly one mention marker (on the reply), got %d in:\n%s", markerCount, out)
	}
}

func TestChannelDigest_TruncatesExtraReplies(t *testing.T) {
	parent := goslack.Message{}
	parent.Timestamp = "1700000100.000000"
	parent.User = "U002"
	parent.Text = "thread"

	var replies []goslack.Message
	for i := 0; i < ThreadPreviewReplies+5; i++ {
		m := goslack.Message{}
		m.Timestamp = "1700000200.000000"
		m.User = "U003"
		m.Text = "reply"
		replies = append(replies, m)
	}

	out := ChannelDigest("dev", []goslack.Message{parent}, nil, 10,
		WithThreadReplies(map[string][]goslack.Message{parent.Timestamp: replies}),
	)
	want := "+5 more replies"
	if !strings.Contains(out, want) {
		t.Fatalf("expected truncation marker %q, got:\n%s", want, out)
	}
}

func TestChannelDigest_OverridesReplyCap(t *testing.T) {
	parent := goslack.Message{}
	parent.Timestamp = "1700000100.000000"
	parent.User = "U002"
	parent.Text = "thread"

	var replies []goslack.Message
	for i := 0; i < 10; i++ {
		m := goslack.Message{}
		m.Timestamp = "1700000200.000000"
		m.User = "U003"
		m.Text = "reply"
		replies = append(replies, m)
	}

	cases := []struct {
		name        string
		cap         int
		wantHidden  string
		wantVisible int
	}{
		{"override to 1", 1, "+9 more replies", 1},
		{"override to 7", 7, "+3 more replies", 7},
		{"override to 0 falls back to default", 0, "+7 more replies", ThreadPreviewReplies},
		{"override to negative falls back to default", -5, "+7 more replies", ThreadPreviewReplies},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := ChannelDigest("dev", []goslack.Message{parent}, nil, 10,
				WithThreadReplies(map[string][]goslack.Message{parent.Timestamp: replies}),
				WithThreadPreviewReplies(c.cap),
			)
			if !strings.Contains(out, c.wantHidden) {
				t.Fatalf("cap=%d: expected %q, got:\n%s", c.cap, c.wantHidden, out)
			}
			gotVisible := strings.Count(out, ReplyIndent)
			// One indent prefix per visible reply, plus one for the
			// "+N more replies" line itself.
			wantIndents := c.wantVisible + 1
			if gotVisible != wantIndents {
				t.Fatalf("cap=%d: visible reply lines = %d; want %d. Output:\n%s",
					c.cap, gotVisible, wantIndents, out)
			}
		})
	}
}

func TestChannelDigest_RepliesWithoutOptionStillRenders(t *testing.T) {
	// Backwards compatibility: existing callers that don't pass any
	// options must still render unchanged output.
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = "U002"
	m.Text = "hello"
	out := ChannelDigest("dev", []goslack.Message{m}, map[string]string{"U002": "alex"}, 5)
	if strings.Contains(out, MentionMarker) || strings.Contains(out, ReplyIndent) {
		t.Fatalf("expected no markers/replies without options, got:\n%s", out)
	}
}
