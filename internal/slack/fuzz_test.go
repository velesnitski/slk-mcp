package slack

import (
	"strings"
	"testing"
)

// ADR 118: permalinks arrive pasted by people, so the parsers must hold
// on anything.

func FuzzParseSlackPermalink(f *testing.F) {
	for _, s := range []string{
		"https://example.slack.com/archives/C0GENERAL1/p1700000000000100",
		"https://example.slack.com/archives/C0GENERAL1/p1700000000000200?thread_ts=1700000000.000100&cid=C0GENERAL1",
		"https://example.slack.com/archives/D0DM000001/p1",
		"not a link", "", "https://example.slack.com/archives//p",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParseSlackPermalink(s)
		if err != nil || p == nil { // nil, nil = no permalink given, by contract
			return
		}
		if p.ChannelID == "" || p.TS == "" || p.ThreadTS == "" {
			t.Fatalf("parsed %q into an incomplete permalink: %+v", s, p)
		}
		if !strings.Contains(p.TS, ".") {
			t.Fatalf("message ts %q has no seconds/micros split", p.TS)
		}
	})
}

func FuzzParseSlackFileURL(f *testing.F) {
	for _, s := range []string{
		"https://example.slack.com/files/U0OWNER001/F0FILE0001/report.pdf",
		"https://example.slack.com/files/U0OWNER001/",
		"F0FILE0001", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		id, ok := ParseSlackFileURL(s)
		if ok && (id == "" || id[0] != 'F') {
			t.Fatalf("%q parsed to a non-file id %q", s, id)
		}
	})
}
