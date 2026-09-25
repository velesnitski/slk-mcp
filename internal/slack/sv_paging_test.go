package slack

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"testing"
)

// ADR 113. The service-layer fakes answered conversations.history and
// conversations.replies with a fixed body, ignoring `oldest`, `latest`,
// `limit` and `cursor`. No test could observe which slice of a
// conversation a request returns, so code built on a wrong model of
// Slack's paging passed — and several tests pinned that model by asserting
// the `oldest` parameter. svPager serves both methods the way the live API
// was measured to:
//
//   - history: `latest` set (or neither bound) → the `limit` messages
//     adjacent to `latest`; `oldest` alone → the `limit` messages adjacent
//     to `oldest`. Newest-first either way.
//   - replies: oldest-first, `limit` per page, continued with `cursor`.

type svPager struct {
	history []svJSON            // any order; sorted by ts on serve
	replies map[string][]svJSON // thread_ts → replies, parent included
}

func svTS(i int) string { return fmt.Sprintf("%d.000000", 1700000000+i) }

func svTSf(m svJSON) float64 { f, _ := strconv.ParseFloat(m["ts"].(string), 64); return f }

func (p *svPager) install(f *svFake) {
	f.on("conversations.history", func(r *http.Request) svJSON {
		msgs := append([]svJSON(nil), p.history...)
		sort.Slice(msgs, func(i, j int) bool { return svTSf(msgs[i]) < svTSf(msgs[j]) })
		oldest, latest := r.FormValue("oldest"), r.FormValue("latest")
		limit, _ := strconv.Atoi(r.FormValue("limit"))
		if limit <= 0 {
			limit = 100
		}
		var in []svJSON
		for _, m := range msgs {
			ts := svTSf(m)
			if o, err := strconv.ParseFloat(oldest, 64); err == nil && oldest != "" && ts <= o {
				continue
			}
			if l, err := strconv.ParseFloat(latest, 64); err == nil && latest != "" && ts > l {
				continue
			}
			in = append(in, m)
		}
		more := len(in) > limit
		if more {
			if oldest != "" && latest == "" {
				in = in[:limit] // the stale end
			} else {
				in = in[len(in)-limit:] // the fresh end
			}
		}
		for i, j := 0, len(in)-1; i < j; i, j = i+1, j-1 {
			in[i], in[j] = in[j], in[i]
		}
		return svJSON{"ok": true, "has_more": more, "messages": in}
	})
	f.on("conversations.replies", func(r *http.Request) svJSON {
		all := p.replies[r.FormValue("ts")]
		limit, _ := strconv.Atoi(r.FormValue("limit"))
		if limit <= 0 {
			limit = 100
		}
		start, _ := strconv.Atoi(r.FormValue("cursor"))
		end := start + limit
		if end > len(all) {
			end = len(all)
		}
		out := svJSON{"ok": true, "messages": all[start:end], "has_more": end < len(all)}
		if end < len(all) {
			out["response_metadata"] = svJSON{"next_cursor": strconv.Itoa(end)}
		}
		return out
	})
}

func TestUnread_BusyChannelReturnsItsNewestUnreadMessages(t *testing.T) {
	// 300 messages a minute apart, the last 200 unread. The old request
	// (oldest = last_read, or 12h behind it) returned the page adjacent
	// to that bound — messages from before last_read — and the unread
	// filter emptied it: a channel with 200 unread messages vanished
	// from the sweep.
	f := svServer(t)
	svFreezeClock(t, 1700000000+300*60+60)
	p := &svPager{}
	for i := 1; i <= 300; i++ {
		p.history = append(p.history, svMsg(svTS(i*60), "U2", fmt.Sprintf("m%d", i), nil))
	}
	p.install(f)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{"last_read": svTS(100 * 60)})})

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cu.Messages) == 0 {
		t.Fatal("a channel with 200 unread messages returned none")
	}
	if cu.Messages[0].Text != "m300" {
		t.Fatalf("the newest unread message must be first, got %q", cu.Messages[0].Text)
	}
	for _, m := range cu.Messages {
		if m.Timestamp <= svTS(100*60) {
			t.Fatalf("a message at or before last_read leaked in: %s", m.Timestamp)
		}
	}
}

func TestUnread_ReadThreadParentOnTheNewestPageStillSurfacesItsNewReply(t *testing.T) {
	// The reason the old request reached behind last_read: a thread whose
	// parent is already read. It must still work from the newest page.
	f := svServer(t)
	svFreezeClock(t, 1700000000+10000)
	parent := svMsg(svTS(1000), "U2", "old question", svJSON{
		"thread_ts": svTS(1000), "reply_count": 1, "latest_reply": svTS(9000),
	})
	p := &svPager{
		history: []svJSON{parent, svMsg(svTS(9500), "U2", "new top-level", nil)},
		replies: map[string][]svJSON{svTS(1000): {
			svMsg(svTS(1000), "U2", "old question", svJSON{"thread_ts": svTS(1000)}),
			svMsg(svTS(9000), "U3", "fresh answer", svJSON{"thread_ts": svTS(1000)}),
		}},
	}
	p.install(f)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{"last_read": svTS(5000)})})

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if rs := cu.Replies[svTS(1000)]; len(rs) != 1 || rs[0].Text != "fresh answer" {
		t.Fatalf("replies = %+v, want the fresh answer under the read parent", cu.Replies)
	}
}

func TestThreadReplies_LongThreadIncludesItsNewestReply(t *testing.T) {
	// conversations.replies pages oldest-first. One page of 200 dropped
	// the newest replies of any longer thread.
	f := svServer(t)
	var thread []svJSON
	for i := 0; i <= 450; i++ {
		thread = append(thread, svMsg(svTS(i), "U2", fmt.Sprintf("r%d", i), svJSON{"thread_ts": svTS(0)}))
	}
	(&svPager{replies: map[string][]svJSON{svTS(0): thread}}).install(f)

	got, err := svMessages(t, f).ThreadReplies(context.Background(), "C1", svTS(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 451 || got[len(got)-1].Text != "r450" {
		t.Fatalf("got %d replies ending in %q, want all 451 ending in r450", len(got), got[len(got)-1].Text)
	}
}

func TestUnread_LongThreadStillSurfacesItsNewestReply(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, 1700000000+10000)
	var thread []svJSON
	thread = append(thread, svMsg(svTS(1000), "U2", "parent", svJSON{"thread_ts": svTS(1000)}))
	for i := 1; i <= 150; i++ {
		thread = append(thread, svMsg(svTS(1000+i), "U2", "old reply", svJSON{"thread_ts": svTS(1000)}))
	}
	thread = append(thread, svMsg(svTS(9000), "U3", "the new reply", svJSON{"thread_ts": svTS(1000)}))
	(&svPager{
		history: []svJSON{svMsg(svTS(1000), "U2", "parent", svJSON{
			"thread_ts": svTS(1000), "reply_count": 151, "latest_reply": svTS(9000),
		})},
		replies: map[string][]svJSON{svTS(1000): thread},
	}).install(f)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{"last_read": svTS(5000)})})

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 10)
	if err != nil {
		t.Fatal(err)
	}
	rs := cu.Replies[svTS(1000)]
	if len(rs) != 1 || rs[0].Text != "the new reply" {
		t.Fatalf("replies = %+v, want only the reply newer than last_read — found past the first page", rs)
	}
}
