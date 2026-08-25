package tools

import (
	"regexp"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestPermalinkHostAndHostsEqual(t *testing.T) {
	if got := permalinkHost("https://example.slack.com/archives/C1/p1714000000000123"); got != "example.slack.com" {
		t.Fatalf("host: %q", got)
	}
	if permalinkHost("not a url") != "" {
		t.Fatal("garbage must yield empty host, not a guess")
	}
	// auth.test returns the base URL with a trailing slash; the match
	// must survive that and case differences.
	if !hostsEqual("example.slack.com", "https://Example.Slack.com/") {
		t.Fatal("case + trailing slash must not break the match")
	}
	if hostsEqual("other.slack.com", "https://example.slack.com/") {
		t.Fatal("different workspaces must not match")
	}
}

func mkFullMsg(user, ts, text string) goslack.Message {
	m := goslack.Message{}
	m.User = user
	m.Timestamp = ts
	m.Text = text
	return m
}

func TestRenderFullMessage_NeverTruncates(t *testing.T) {
	// The tool's entire reason to exist: a body far past every preview
	// cap must come out byte-identical.
	long := strings.Repeat("а", 5000) // multibyte on purpose
	msg := mkFullMsg("U1", "1714000000.000123", long)
	got := renderFullMessage(msg, nil, map[string]string{"U1": "Alice"}, "primary", "#general", "")

	if !strings.Contains(got, long) {
		t.Fatal("the full body must survive verbatim")
	}
	if !strings.Contains(got, "chars: 5000") {
		t.Fatalf("rune count must be reported (not bytes):\n%.200s", got)
	}
	// The timestamp renders in the runner's local zone, so assert the
	// shape, not a specific date — a zone east of UTC flips the day.
	if !regexp.MustCompile(`from: Alice at \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`).MatchString(got) {
		t.Fatalf("author + absolute datetime missing:\n%.200s", got)
	}
}

func TestRenderFullMessage_ReplyShowsParentContext(t *testing.T) {
	parent := mkFullMsg("U2", "1714000000.000100", strings.Repeat("parent words ", 40))
	reply := mkFullMsg("U1", "1714000000.000200", "the reply")
	got := renderFullMessage(reply, &parent, map[string]string{"U1": "Alice", "U2": "Bob"}, "primary", "#general", "")

	if !strings.Contains(got, "reply in thread of: [Bob]") {
		t.Fatalf("parent context line missing:\n%s", got)
	}
	// The PARENT is context and is capped; only the target message is
	// exempt from truncation.
	if strings.Contains(got, strings.Repeat("parent words ", 40)) {
		t.Fatal("parent context must be a preview, not the full parent")
	}
	if !strings.Contains(got, "…") {
		t.Fatal("capped parent preview must show the ellipsis")
	}
}

func TestRenderFullMessage_ParentPointsAtThreadTool(t *testing.T) {
	msg := mkFullMsg("U1", "1714000000.000100", "root post")
	msg.ReplyCount = 7
	got := renderFullMessage(msg, nil, nil, "primary", "#general", "")
	if !strings.Contains(got, "thread parent: 7 replies (use get_thread") {
		t.Fatalf("thread hint missing:\n%s", got)
	}
}

func TestRenderFullMessage_MetadataAndNote(t *testing.T) {
	msg := mkFullMsg("U1", "1714000000.000100", "body")
	msg.Edited = &goslack.Edited{User: "U1", Timestamp: "1714000001.000000"}
	msg.Reactions = []goslack.ItemReaction{{Name: "eyes", Count: 3}, {Name: "fire", Count: 1}}
	msg.Files = []goslack.File{{ID: "F1", Name: "spec.pdf", Mimetype: "application/pdf", Size: 1234}}

	got := renderFullMessage(msg, nil, nil, "secondary", "#general", "workspace auto-detected from permalink")

	for _, want := range []string{
		"[secondary] (workspace auto-detected from permalink)",
		"(edited)",
		":eyes: ×3  :fire: ×1",
		"spec.pdf (application/pdf, 1234 bytes)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderFullMessage_BotUsernameFallback(t *testing.T) {
	msg := mkFullMsg("", "1714000000.000100", "bot says")
	msg.Username = "release bot"
	got := renderFullMessage(msg, nil, nil, "primary", "#general", "")
	if !strings.Contains(got, "from: release bot") {
		t.Fatalf("bot username fallback missing:\n%s", got)
	}
}

func TestPreviewLine(t *testing.T) {
	if got := previewLine("a  b\n\nc", 100); got != "a b c" {
		t.Fatalf("whitespace must flatten: %q", got)
	}
	long := strings.Repeat("я", 250)
	got := previewLine(long, 200)
	if len([]rune(got)) != 201 || !strings.HasSuffix(got, "…") {
		t.Fatalf("rune-safe cap broken: %d runes", len([]rune(got)))
	}
}
