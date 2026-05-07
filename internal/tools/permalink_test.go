package tools

import (
	"errors"
	"testing"
)

func TestParseSlackPermalink_TopLevelMessage(t *testing.T) {
	// Permalink to a top-level message — TS and ThreadTS coincide.
	in := "https://example.slack.com/archives/C0ABC1234DE/p1714000000000123"
	p, err := parseSlackPermalink(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil parse result")
	}
	if p.ChannelID != "C0ABC1234DE" {
		t.Errorf("ChannelID=%q; want C0ABC1234DE", p.ChannelID)
	}
	if p.TS != "1714000000.000123" {
		t.Errorf("TS=%q; want 1714000000.000123", p.TS)
	}
	if p.ThreadTS != p.TS {
		t.Errorf("ThreadTS=%q; want same as TS for non-reply permalink", p.ThreadTS)
	}
}

func TestParseSlackPermalink_ThreadReply(t *testing.T) {
	// Permalink to a reply: thread_ts query carries the root, the "p"
	// segment is the reply's own ts.
	in := "https://example.slack.com/archives/C0ABC1234DE/p1714000099000456?thread_ts=1714000000.000123&cid=C0ABC1234DE"
	p, err := parseSlackPermalink(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.TS != "1714000099.000456" {
		t.Errorf("TS=%q; want 1714000099.000456", p.TS)
	}
	if p.ThreadTS != "1714000000.000123" {
		t.Errorf("ThreadTS=%q; want 1714000000.000123 from query param", p.ThreadTS)
	}
}

func TestParseSlackPermalink_EmptyIsNoOp(t *testing.T) {
	p, err := parseSlackPermalink("")
	if err != nil {
		t.Errorf("empty input should not error, got %v", err)
	}
	if p != nil {
		t.Errorf("empty input should return nil, got %+v", p)
	}

	p, err = parseSlackPermalink("   \t  ")
	if err != nil {
		t.Errorf("whitespace input should not error, got %v", err)
	}
	if p != nil {
		t.Errorf("whitespace input should return nil, got %+v", p)
	}
}

func TestParseSlackPermalink_NotAPermalink(t *testing.T) {
	cases := []string{
		"https://example.com/",                 // no /archives/
		"https://example.slack.com/archives/",  // no channel
		"https://example.slack.com/archives/C0ABC1234DE/", // no p<ts>
		"random gibberish",
	}
	for _, c := range cases {
		_, err := parseSlackPermalink(c)
		if !errors.Is(err, errNotASlackPermalink) {
			t.Errorf("parseSlackPermalink(%q) err=%v; want errNotASlackPermalink", c, err)
		}
	}
}

func TestDecodePermalinkTS(t *testing.T) {
	cases := map[string]string{
		"1714000000000123": "1714000000.000123",
		"1234567890123456": "1234567890.123456",
		"100":              "100", // too short — leave unchanged
	}
	for in, want := range cases {
		if got := decodePermalinkTS(in); got != want {
			t.Errorf("decodePermalinkTS(%q)=%q; want %q", in, got, want)
		}
	}
}
