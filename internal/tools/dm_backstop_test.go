package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func dmMsg(user, ts, text string) goslack.Message {
	m := goslack.Message{}
	m.User = user
	m.Timestamp = ts
	m.Text = text
	return m
}

func dmChannelUnread(id string, msgs []goslack.Message, replies map[string][]goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{Messages: msgs, Replies: replies}
	cu.Channel.ID = id
	return cu
}

func TestDMActivityToHits_KeepsOthersDropsSelf(t *testing.T) {
	cus := []*slack.ChannelUnread{
		dmChannelUnread("D1",
			[]goslack.Message{
				dmMsg("OTHER", "200.0", "their line"),
				dmMsg("ME", "150.0", "my own line"),
			},
			map[string][]goslack.Message{
				"100.0": {dmMsg("OTHER", "300.0", "their reply")},
			},
		),
	}
	hits := dmActivityToHits(cus, "ME")
	if len(hits) != 2 {
		t.Fatalf("want 2 hits (others only), got %d: %+v", len(hits), hits)
	}
	byTS := map[string]goslack.SearchMessage{}
	for _, h := range hits {
		if h.User == "ME" {
			t.Fatalf("own message must be excluded; got %+v", h)
		}
		if h.Channel.ID != "D1" {
			t.Errorf("channel id not carried; got %q", h.Channel.ID)
		}
		byTS[h.Timestamp] = h
	}
	// The top-level hit's permalink has no thread_ts; the reply's does.
	if top, ok := byTS["200.0"]; !ok || strings.Contains(top.Permalink, "thread_ts") {
		t.Errorf("top-level hit should have a plain permalink; got %+v", top)
	}
	if rep, ok := byTS["300.0"]; !ok || !strings.Contains(rep.Permalink, "thread_ts=100.0") {
		t.Errorf("reply hit permalink should carry thread_ts=100.0; got %+v", rep)
	}
}

func TestDMActivityToHits_EmptySelfIsNoop(t *testing.T) {
	cus := []*slack.ChannelUnread{dmChannelUnread("D1", []goslack.Message{dmMsg("OTHER", "200.0", "x")}, nil)}
	if got := dmActivityToHits(cus, ""); got != nil {
		t.Fatalf("empty self id must yield nil (can't exclude own messages); got %+v", got)
	}
}

func TestMergeSearchHits_DedupAndSort(t *testing.T) {
	realHit := goslack.SearchMessage{User: "OTHER", Timestamp: "300.0", Permalink: "https://real/link"}
	realHit.Channel.ID = "D1"
	base := []goslack.SearchMessage{realHit}

	// One duplicate (same channel+ts) that must be dropped in favour of
	// the real hit, one fresh history hit that must be added.
	dupHit := goslack.SearchMessage{User: "OTHER", Timestamp: "300.0", Permalink: "https://synthetic"}
	dupHit.Channel.ID = "D1"
	freshHit := goslack.SearchMessage{User: "OTHER", Timestamp: "500.0", Permalink: "https://synthetic2"}
	freshHit.Channel.ID = "D1"

	out := mergeSearchHits(base, []goslack.SearchMessage{dupHit, freshHit})
	if len(out) != 2 {
		t.Fatalf("want 2 after dedup, got %d: %+v", len(out), out)
	}
	// Newest first: 500.0 before 300.0.
	if out[0].Timestamp != "500.0" || out[1].Timestamp != "300.0" {
		t.Fatalf("want newest-first order [500,300]; got [%s,%s]", out[0].Timestamp, out[1].Timestamp)
	}
	// The surviving 300.0 must be the real hit (canonical permalink), not
	// the synthetic duplicate.
	if out[1].Permalink != "https://real/link" {
		t.Errorf("real search hit should win the dedup; got permalink %q", out[1].Permalink)
	}
}
