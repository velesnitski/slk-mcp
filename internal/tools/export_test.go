package tools

import (
	"path/filepath"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestChannelKind(t *testing.T) {
	mk := func(id string, im, mpim, private bool) goslack.Channel {
		ch := goslack.Channel{}
		ch.ID = id
		ch.IsIM, ch.IsMpIM, ch.IsPrivate = im, mpim, private
		return ch
	}
	cases := []struct {
		ch   goslack.Channel
		want string
	}{
		{mk("C1", false, false, false), "channel"},
		{mk("G1", false, false, true), "private"},
		{mk("D1", true, false, false), "im"},
		{mk("C9", false, true, false), "mpim"},
		// Flags are not always populated on a search-derived channel, so
		// the ID prefix has to carry the classification too.
		{mk("D9", false, false, false), "im"},
		{mk("G9", false, false, false), "private"},
	}
	for _, c := range cases {
		if got := channelKind(c.ch); got != c.want {
			t.Errorf("channelKind(%s) = %q, want %q", c.ch.ID, got, c.want)
		}
	}
	// mpim wins over the D/G prefix check: a group DM is not a 1:1.
	if got := channelKind(mk("G5", false, true, true)); got != "mpim" {
		t.Errorf("mpim must win over private; got %q", got)
	}
}

func TestWorkspaceFilename(t *testing.T) {
	if got := workspaceFilename("primary"); got != "primary" {
		t.Errorf("got %q", got)
	}
	if got := workspaceFilename("Team Alpha / QA"); got != "Team_Alpha___QA" {
		t.Errorf("separators must not survive into a path: %q", got)
	}
	if got := workspaceFilename(""); got != "workspace" {
		t.Errorf("empty label must not produce a bare .jsonl: %q", got)
	}
	// sanitizeFilename falls back to "audio"; a corpus must not inherit
	// that, but a workspace genuinely called "audio" should keep its name.
	if got := workspaceFilename("///"); got != "workspace" {
		t.Errorf("unusable label got %q", got)
	}
	if got := workspaceFilename("audio"); got != "audio" {
		t.Errorf("a real label must survive: %q", got)
	}
}

func TestExportDir_DefaultsUnderHome(t *testing.T) {
	if got, err := exportDir("/tmp/corpus"); err != nil || got != "/tmp/corpus" {
		t.Fatalf("explicit dir must win: %q, %v", got, err)
	}
	got, err := exportDir("")
	if err != nil {
		t.Fatalf("default dir: %v", err)
	}
	// A corpus is long-lived, so the default must not be a temp dir that
	// gets swept out from under it.
	if filepath.Base(got) != "slk-export" || !filepath.IsAbs(got) {
		t.Fatalf("unexpected default %q", got)
	}
}

func TestTsLessStr(t *testing.T) {
	if !tsLessStr("1784012484.000100", "1784012484.000200") {
		t.Error("same second, later fraction must sort after")
	}
	if tsLessStr("1784012485.0", "1784012484.9") {
		t.Error("later second must not sort first")
	}
	// Lexical comparison would get this wrong: "9" > "10".
	if !tsLessStr("9.0", "10.0") {
		t.Error("must compare numerically, not lexically")
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" alpha , ,bravo,")
	if len(got) != 2 || got[0] != "alpha" || got[1] != "bravo" {
		t.Fatalf("got %v", got)
	}
	if splitCSV("") != nil {
		t.Fatal("empty input must yield nil, not a one-element slice")
	}
}
