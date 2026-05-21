package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
)

func mkSearchHit(channelID, ts, threadTSinPermalink, text string) goslack.SearchMessage {
	m := goslack.SearchMessage{}
	m.Channel.ID = channelID
	m.Timestamp = ts
	m.Text = text
	if threadTSinPermalink != "" {
		m.Permalink = "https://example.slack.com/archives/" + channelID +
			"/p" + ts + "?thread_ts=" + threadTSinPermalink
	}
	return m
}

func TestThreadKey_dedupsAcrossSameThread(t *testing.T) {
	// Two replies in the same thread of the same channel.
	a := mkSearchHit("C1", "1.001", "1.000", "first reply")
	b := mkSearchHit("C1", "1.002", "1.000", "second reply")
	if threadKey(a) != threadKey(b) {
		t.Fatalf("same-thread hits should share key; got %q vs %q",
			threadKey(a), threadKey(b))
	}
}

func TestThreadKey_separatesDifferentChannels(t *testing.T) {
	// Same thread_ts in two different channels — must produce
	// distinct keys (a channel-local timestamp is not globally unique).
	a := mkSearchHit("C1", "1.001", "1.000", "x")
	b := mkSearchHit("C2", "1.001", "1.000", "x")
	if threadKey(a) == threadKey(b) {
		t.Fatalf("cross-channel hits must not share key")
	}
}

func TestExtractThreadTS_falsBackToTimestampForToplevel(t *testing.T) {
	// A search hit without thread_ts in its permalink IS the top-level
	// message — ExtractThreadTS returns m.Timestamp as a sentinel that
	// callers interpret as "this is its own parent, no fetch needed".
	m := goslack.SearchMessage{}
	m.Timestamp = "1.000"
	m.Permalink = "https://example.slack.com/archives/C1/p1000"
	if got := format.ExtractThreadTS(m); got != "1.000" {
		t.Fatalf("toplevel hit should yield m.Timestamp; got %q", got)
	}
}

func TestExtractThreadTS_extractsFromPermalink(t *testing.T) {
	m := mkSearchHit("C1", "1.002", "1.000", "reply")
	if got := format.ExtractThreadTS(m); got != "1.000" {
		t.Fatalf("expected thread_ts=1.000 from permalink; got %q", got)
	}
}
