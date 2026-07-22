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
	if out := ChannelDigest("dev", nil, nil, 5); out != "" {
		t.Fatalf("empty channel must return empty string for token efficiency, got: %s", out)
	}
}

// A channel with no top-level messages but lingering thread replies
// (e.g. a DM pulled in by dm_window_hours) renders "(no activity)" by
// default — useful for single-channel get_channel_digest.
func TestChannelDigest_NoTopLevelShowsNoActivity(t *testing.T) {
	replies := map[string][]goslack.Message{
		"1.0": {{Msg: goslack.Msg{Timestamp: "1.1", User: "U2", Text: "stale reply"}}},
	}
	out := ChannelDigest("@peer", nil, nil, 5, WithThreadReplies(replies))
	if !strings.Contains(out, "(no activity)") {
		t.Fatalf("default should render (no activity); got %q", out)
	}
}

// With WithOmitEmpty (the unread-sweep path), that same content-less
// channel collapses to "" so the aggregator drops it instead of
// emitting an empty stub block.
func TestChannelDigest_OmitEmptySuppressesNoActivity(t *testing.T) {
	replies := map[string][]goslack.Message{
		"1.0": {{Msg: goslack.Msg{Timestamp: "1.1", User: "U2", Text: "stale reply"}}},
	}
	out := ChannelDigest("@peer", nil, nil, 5,
		WithThreadReplies(replies), WithOmitEmpty())
	if out != "" {
		t.Fatalf("WithOmitEmpty must suppress the (no activity) stub; got %q", out)
	}
}

// WithOmitEmpty must NOT affect channels that have real content.
func TestChannelDigest_OmitEmptyKeepsRealContent(t *testing.T) {
	msgs := []goslack.Message{{Msg: goslack.Msg{Timestamp: "1.0", User: "U1", Text: "hello"}}}
	out := ChannelDigest("dev", msgs, map[string]string{"U1": "alex"}, 5, WithOmitEmpty())
	if !strings.Contains(out, "hello") {
		t.Fatalf("WithOmitEmpty dropped real content; got %q", out)
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
	// RenderText resolves <@USERID> to @USERID (or @Name when known).
	plainIdx := strings.Index(out, "hello team")
	mentionIdx := strings.Index(out, "ping @U001")
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
	if !strings.Contains(out, "first reply") || !strings.Contains(out, "second reply with @U001") {
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

func TestRenderText_ResolvesMentionsAndStripsLinks(t *testing.T) {
	users := map[string]string{"U001": "Alice", "U002": "Bob"}
	cases := []struct {
		in   string
		want string
	}{
		{"ping <@U001> please", "ping @Alice please"},
		{"ping <@U999>", "ping @U999"},
		{"see <https://example.test/foo|FOO-1> for details", "see FOO-1 for details"},
		{"raw link <https://example.test/x>", "raw link "},
		{"<@U002> please look at <https://example.test/abc|item>", "@Bob please look at item"},
		{"plain text", "plain text"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := RenderText(c.in, users); got != c.want {
				t.Fatalf("RenderText(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRenderText_ResolvesChannelRefs(t *testing.T) {
	refs := map[string]string{
		"U001":        "Alice",
		"C0ABCDEFGHI": "team-alpha",
		"C0AAAABBBBC": "team-bravo",
	}
	cases := []struct {
		in   string
		want string
	}{
		// Inline pipe label always wins, even when the ref isn't in the map.
		{"check <#C0XXXXXXXXX|team-alpha> please", "check #team-alpha please"},
		// No pipe → look up in the refs map.
		{"see <#C0ABCDEFGHI>", "see #team-alpha"},
		// Two refs in one body, only the first resolves via the map.
		{"<#C0AAAABBBBC> then <#C0ZZZZZZZZZ>", "#team-bravo then #C0ZZZZZZZZZ"},
		// Unknown ID with no pipe must NOT vanish — keep `#CID` as a marker.
		{"ping <#C0ZZZZZZZZZ>", "ping #C0ZZZZZZZZZ"},
		// Mixed with a user mention in the same body.
		{"<@U001> in <#C0ABCDEFGHI>", "@Alice in #team-alpha"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := RenderText(c.in, refs); got != c.want {
				t.Fatalf("RenderText(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestCollectMentionedChannelIDs_DedupesAndIgnoresInvalid(t *testing.T) {
	mk := func(text string) goslack.Message {
		m := goslack.Message{}
		m.Text = text
		return m
	}
	msgs := []goslack.Message{
		mk("see <#C0AAAAAAAAA> and <#C0BBBBBBBBB|name>"),
		mk("again <#C0AAAAAAAAA>"),    // dup
		mk("private <#G0PRIVATE12>"),  // G-prefix is also a channel
		mk("user <@U001>"),            // not a channel
		mk("not-a-channel <#XYZ123>"), // wrong prefix
	}
	got := CollectMentionedChannelIDs(msgs)
	want := map[string]bool{
		"C0AAAAAAAAA": true,
		"C0BBBBBBBBB": true,
		"G0PRIVATE12": true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d ids %v; want %d (%v)", len(got), got, len(want), want)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected id %q", id)
		}
	}
}

func TestCollectMentionedUserIDs_DedupesAcrossMessages(t *testing.T) {
	mk := func(text string) goslack.Message {
		m := goslack.Message{}
		m.Text = text
		return m
	}
	msgs := []goslack.Message{
		mk("ping <@U001> and <@U002>"),
		mk("again <@U001>"),
		mk("<@U003|already-named>"),
	}
	got := CollectMentionedUserIDs(msgs)
	want := []string{"U001", "U002", "U003"}
	if len(got) != len(want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("got[%d] = %q; want %q", i, got[i], w)
		}
	}
}

func TestRenderFiles_ImageWithDimensions(t *testing.T) {
	msg := goslack.Message{}
	msg.Timestamp = "1700000000.000000"
	msg.User = "U1"
	msg.Files = []goslack.File{
		{Name: "screenshot.png", Mimetype: "image/png", OriginalW: 1280, OriginalH: 720},
	}
	out := MessageLine(msg, "alex")
	if !strings.Contains(out, "[🖼 screenshot.png (1280x720)]") {
		t.Fatalf("expected image marker with dimensions, got: %s", out)
	}
}

func TestRenderFiles_NonImageGenericMarker(t *testing.T) {
	msg := goslack.Message{}
	msg.Timestamp = "1700000000.000000"
	msg.User = "U1"
	msg.Files = []goslack.File{
		{Name: "report.pdf", Mimetype: "application/pdf"},
	}
	out := MessageLine(msg, "alex")
	if !strings.Contains(out, "[📎 report.pdf]") {
		t.Fatalf("expected pdf marker, got: %s", out)
	}
}

func TestRenderFiles_MultipleAttachments(t *testing.T) {
	msg := goslack.Message{}
	msg.Timestamp = "1700000000.000000"
	msg.User = "U1"
	msg.Files = []goslack.File{
		{Name: "a.jpg", Mimetype: "image/jpeg", OriginalW: 800, OriginalH: 600},
		{Name: "b.zip", Mimetype: "application/zip"},
	}
	out := MessageLine(msg, "alex")
	for _, want := range []string{"🖼 a.jpg (800x600)", "📎 b.zip"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in: %s", want, out)
		}
	}
}

func TestHasContent_FilesAlone(t *testing.T) {
	msg := goslack.Message{}
	msg.Files = []goslack.File{{Name: "x.png", Mimetype: "image/png"}}
	if !HasContent(msg) {
		t.Fatal("message with file attachment but empty body must still be content-ful")
	}
}

func TestExtractThreadTS(t *testing.T) {
	cases := []struct {
		name      string
		ts        string
		permalink string
		want      string
	}{
		{"top-level message: thread_ts == ts", "1714492800.123456", "https://x.slack.com/archives/C01/p1714492800123456", "1714492800.123456"},
		{"threaded reply: parsed from permalink", "1714492900.000000", "https://x.slack.com/archives/C01/p1714492900000000?thread_ts=1714492800.123456&cid=C01", "1714492800.123456"},
		{"empty permalink: fall back to ts", "1714492800.123456", "", "1714492800.123456"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := goslack.SearchMessage{Permalink: c.permalink}
			m.Timestamp = c.ts
			if got := ExtractThreadTS(m); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestSearchResultExt_truncation(t *testing.T) {
	long := strings.Repeat("a", 300)
	m := goslack.SearchMessage{Permalink: "https://x.slack.com/archives/C01/p1714492800000000"}
	m.Timestamp = "1714492800.000000"
	m.Channel.Name = "general"
	m.Username = "alice"
	m.Text = long

	short := SearchResultExt(m, false)
	if !strings.Contains(short, "...") {
		t.Fatalf("expected truncation marker in: %q", short)
	}
	full := SearchResultExt(m, true)
	if strings.Contains(full, "...") {
		t.Fatalf("expected no truncation in: %q", full)
	}
	if !strings.Contains(short, "thread_ts=1714492800.000000") {
		t.Fatalf("expected thread_ts continuation line: %q", short)
	}
}
