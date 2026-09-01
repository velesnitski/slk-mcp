package format

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func orphanMsg(user, ts, text string) goslack.Message {
	m := goslack.Message{}
	m.User = user
	m.Timestamp = ts
	m.Text = text
	return m
}

// The regression: an escalation lands as a reply to a report filed
// hours earlier. The parent is outside the window, so the channel has
// no top-level message — and the whole channel used to vanish from the
// sweep while its counters still counted the reply.
func TestChannelDigest_RepliesWithoutParentAreRendered(t *testing.T) {
	replies := map[string][]goslack.Message{
		"1714000000.000100": {
			orphanMsg("U1", "1714003600.000200", "looked into it, narrowing down the cause"),
			orphanMsg("U2", "1714003700.000300", "the second page comes back empty"),
		},
	}
	got := ChannelDigest("#lounge", nil, map[string]string{"U1": "Ada", "U2": "Grace"},
		20, WithThreadReplies(replies), WithOmitEmpty())

	if got == "" {
		t.Fatal("a channel whose only new content is thread replies must not be dropped")
	}
	for _, want := range []string{"#lounge", "second page comes back empty", "Ada", "Grace", "in earlier threads"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestChannelDigest_OrphanRepliesCountAndParentTime(t *testing.T) {
	replies := map[string][]goslack.Message{
		"1714000000.000100": {orphanMsg("U1", "1714003600.000200", "one")},
		"1714000500.000100": {
			orphanMsg("U1", "1714003700.000300", "two"),
			orphanMsg("U2", "1714003800.000400", "three"),
		},
	}
	got := ChannelDigest("#platform-main", nil, nil, 20, WithThreadReplies(replies), WithOmitEmpty())

	if !strings.Contains(got, "3 replies in earlier threads") {
		t.Errorf("header must total every reply, got:\n%s", got)
	}
	// Both threads are labelled, so the reader can find each parent.
	if strings.Count(got, "thread from ") != 2 {
		t.Errorf("each thread needs its own parent-time label, got:\n%s", got)
	}
	// Threads read chronologically by parent.
	if strings.Index(got, "one") > strings.Index(got, "two") {
		t.Errorf("threads must be ordered by parent timestamp, got:\n%s", got)
	}
}

func TestChannelDigest_SingularReplyWording(t *testing.T) {
	replies := map[string][]goslack.Message{
		"1714000000.000100": {orphanMsg("U1", "1714003600.000200", "only one")},
	}
	got := ChannelDigest("#platform-main", nil, nil, 20, WithThreadReplies(replies), WithOmitEmpty())
	if !strings.Contains(got, "1 reply in earlier threads") {
		t.Errorf("want singular wording, got:\n%s", got)
	}
}

func TestChannelDigest_TrulyEmptyChannelStillDropped(t *testing.T) {
	// omitEmpty must keep working for its original case: nothing at all.
	if got := ChannelDigest("#release-notes", nil, nil, 20, WithOmitEmpty()); got != "" {
		t.Fatalf("an empty channel must still be dropped, got %q", got)
	}
	// And an empty reply map is not content either.
	empty := map[string][]goslack.Message{"1714000000.000100": {}}
	if got := ChannelDigest("#release-notes", nil, nil, 20, WithThreadReplies(empty), WithOmitEmpty()); got != "" {
		t.Fatalf("empty reply chains must not resurrect a channel, got %q", got)
	}
}

func TestChannelDigest_TopLevelPathUnchanged(t *testing.T) {
	// Channels that DO have top-level messages must render exactly as
	// before — replies nested under their parent, not as an orphan block.
	parent := orphanMsg("U1", "1714003600.000200", "the original note")
	replies := map[string][]goslack.Message{
		"1714003600.000200": {orphanMsg("U2", "1714003700.000300", "the follow-up")},
	}
	got := ChannelDigest("#platform-main", []goslack.Message{parent}, nil, 20,
		WithThreadReplies(replies), WithOmitEmpty())

	if strings.Contains(got, "in earlier threads") {
		t.Fatalf("the orphan path must not trigger when a parent is present:\n%s", got)
	}
	if !strings.Contains(got, "the original note") || !strings.Contains(got, "the follow-up") {
		t.Fatalf("normal rendering lost content:\n%s", got)
	}
}
