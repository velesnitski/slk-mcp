package slack

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

func TestJoinedCache_ConcurrentCallersShareOneFetch(t *testing.T) {
	var c joinedCache
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func(context.Context) ([]goslack.Channel, error) {
		calls.Add(1)
		<-release
		var ch goslack.Channel
		ch.ID = "C1"
		return []goslack.Channel{ch}, nil
	}

	var wg sync.WaitGroup
	got := make([][]goslack.Channel, 3)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], _ = c.get(context.Background(), fetch)
		}(i)
	}
	time.Sleep(20 * time.Millisecond) // let all three reach the cache
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Fatalf("users.conversations paged %d times, want 1", n)
	}
	for i, g := range got {
		if len(g) != 1 || g[0].ID != "C1" {
			t.Fatalf("caller %d got %v", i, g)
		}
	}
	// Each caller owns its slice: reordering one copy must not reach another.
	got[0][0].ID = "changed"
	if again, _ := c.get(context.Background(), fetch); again[0].ID != "C1" {
		t.Fatal("a caller's copy leaked into the cache")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("a fresh listing must be reused, fetched %d times", n)
	}
}

func TestJoinedCache_ExpiresAndNeverCachesAnError(t *testing.T) {
	old := joinedTTL
	joinedTTL = 0
	t.Cleanup(func() { joinedTTL = old })

	var c joinedCache
	var calls int
	fail := true
	fetch := func(context.Context) ([]goslack.Channel, error) {
		calls++
		if fail {
			return nil, errors.New("boom")
		}
		return []goslack.Channel{{}}, nil
	}
	if _, err := c.get(context.Background(), fetch); err == nil {
		t.Fatal("the error must reach the caller")
	}
	fail = false
	if _, err := c.get(context.Background(), fetch); err != nil {
		t.Fatalf("an error must not be cached: %v", err)
	}
	if _, err := c.get(context.Background(), fetch); err != nil || calls != 3 {
		t.Fatalf("an expired listing must be fetched again: calls=%d err=%v", calls, err)
	}
}

func TestJoinedChannels_OneListingPerSweep(t *testing.T) {
	f := svServer(t)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{{"id": "C1", "name": "alpha"}}})
	svc := svUnread(t, f, nil)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.JoinedChannels(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := f.count("users.conversations"); n != 1 {
		t.Fatalf("users.conversations called %d times, want 1", n)
	}
}

func TestUnreadOwnThreads_FetchesEveryThreadAndMergesInOrder(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svAuthOK(f)
	hit := func(ch, ts string) svJSON {
		return svJSON{
			"ts": ts, "user": "U1", "text": "my ask",
			"channel":   svJSON{"id": ch, "name": ch},
			"permalink": "https://example.invalid/archives/" + ch + "/p" + ts[:10] + ts[11:],
		}
	}
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		hit("C2", "1700099300.000000"), hit("C1", "1700099000.000000"), hit("C1", "1700099200.000000"),
	}}})
	f.on("conversations.replies", func(r *http.Request) svJSON {
		root := r.FormValue("ts")
		return svJSON{"ok": true, "messages": []svJSON{
			svMsg(root, "U1", "my ask", svJSON{"thread_ts": root}),
			svMsg(root[:6]+"9999.000000", "U2", "answer to "+root, svJSON{"thread_ts": root}),
		}}
	})

	got, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if n := f.count("conversations.replies"); n != 3 {
		t.Fatalf("replies fetched %d times, want 3", n)
	}
	by := map[string]*ChannelUnread{}
	for _, cu := range got {
		by[cu.Channel.ID] = cu
	}
	if len(by["C1"].Replies) != 2 || len(by["C2"].Replies) != 1 {
		t.Fatalf("threads lost: C1=%v C2=%v", by["C1"].Replies, by["C2"].Replies)
	}
	for _, root := range []string{"1700099000.000000", "1700099200.000000"} {
		if r := by["C1"].Replies[root]; len(r) != 1 || r[0].Text != "answer to "+root || !by["C1"].OwnThreads[root] {
			t.Fatalf("thread %s: %+v", root, r)
		}
	}
}
