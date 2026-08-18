package export

import (
	"bytes"
	"strings"
	"testing"
)

func TestPermalink_DeterministicAndThreadAware(t *testing.T) {
	got := Permalink("https://example.slack.com/", "C123", "1784012484.471999", "")
	want := "https://example.slack.com/archives/C123/p1784012484471999"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// A reply must link back through its parent, or it opens the channel
	// instead of the thread.
	reply := Permalink("https://example.slack.com", "C123", "1784012500.000100", "1784012484.471999")
	if !strings.Contains(reply, "?thread_ts=1784012484.471999&cid=C123") {
		t.Fatalf("reply permalink missing thread context: %q", reply)
	}
	// A parent must NOT get the thread suffix.
	parent := Permalink("https://example.slack.com", "C123", "1784012484.471999", "1784012484.471999")
	if strings.Contains(parent, "thread_ts") {
		t.Fatalf("parent must not carry thread_ts: %q", parent)
	}
	if Permalink("", "C123", "1.0", "") != "" {
		t.Fatal("no team URL must yield no link, not a broken one")
	}
}

func TestWriteAndReadKeys_RoundTrip(t *testing.T) {
	recs := []Record{
		{Workspace: "primary", ChannelID: "C1", TS: "1.000100", Text: "a"},
		{Workspace: "primary", ChannelID: "C1", TS: "2.000200", Text: "b"},
	}
	var buf bytes.Buffer
	if err := Write(&buf, recs); err != nil {
		t.Fatalf("write: %v", err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 2 {
		t.Fatalf("want 2 JSONL lines, got %d", n)
	}
	if !strings.Contains(buf.String(), `"v":1`) {
		t.Fatal("every record must carry the schema version")
	}
	seen := ReadKeys(strings.NewReader(buf.String()))
	if len(seen) != 2 {
		t.Fatalf("want 2 keys, got %d", len(seen))
	}
}

func TestReadKeys_SkipsTruncatedLastLine(t *testing.T) {
	// An interrupted run leaves a half-written line; it must not block
	// every future append.
	corpus := `{"v":1,"ws":"primary","ch":"C1","ts":"1.000100","text":"a"}` + "\n" + `{"v":1,"ws":"prim`
	seen := ReadKeys(strings.NewReader(corpus))
	if len(seen) != 1 {
		t.Fatalf("want the one intact record, got %d", len(seen))
	}
}

func TestDedup_SkipsSeenAndSelfDedupes(t *testing.T) {
	seen := ReadKeys(strings.NewReader(`{"v":1,"ws":"primary","ch":"C1","ts":"1.000100"}` + "\n"))
	in := []Record{
		{Workspace: "primary", ChannelID: "C1", TS: "1.000100"}, // already in corpus
		{Workspace: "primary", ChannelID: "C1", TS: "2.000200"},
		{Workspace: "primary", ChannelID: "C1", TS: "2.000200"}, // dup within batch
	}
	got := Dedup(in, seen)
	if len(got) != 1 || got[0].TS != "2.000200" {
		t.Fatalf("want only the new record once; got %+v", got)
	}
}

func TestKey_SeparatesWorkspaces(t *testing.T) {
	a := Record{Workspace: "primary", ChannelID: "C1", TS: "1.0"}
	b := Record{Workspace: "secondary", ChannelID: "C1", TS: "1.0"}
	if a.Key() == b.Key() {
		t.Fatal("same channel id in two workspaces must not collide")
	}
}
