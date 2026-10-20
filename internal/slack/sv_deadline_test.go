package slack

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

// ADR 125: a fan-out that tolerates one item failing must still report
// that the deadline cut it, or the sweep advances its cursor past what
// it never read. Each test cancels the context from inside the first
// Slack call it makes, the way an expiring deadline lands mid-loop.

func svCancelOn(f *svFake, method string, cancel context.CancelFunc, body svJSON) {
	f.on(method, func(*http.Request) svJSON {
		cancel()
		return body
	})
}

func TestUnreadAll_ReportsTheDeadline(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil), svChan("C2", "beta", nil),
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svCancelOn(f, "conversations.info", cancel, svJSON{"ok": true, "channel": svChan("C1", "alpha", nil)})

	_, err := svUnread(t, f, nil).UnreadAll(ctx, 5)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context error", err)
	}
}

func TestRecentDMActivity_ReportsTheDeadline(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("D1", "", svJSON{"is_im": true}), svChan("D2", "", svJSON{"is_im": true}),
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svCancelOn(f, "conversations.history", cancel, svJSON{"ok": true, "messages": []svJSON{}})

	_, err := svUnread(t, f, nil).RecentDMActivity(ctx, 2, 5)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context error", err)
	}
}

func TestPriorityActivity_ADeadlineIsNotAMissingChannel(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", svJSON{"last_read": "1700000000.000000"}),
		svChan("C2", "beta", svJSON{"last_read": "1700000000.000000"}),
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.on("conversations.history", func(*http.Request) svJSON {
		cancel()
		return svJSON{"ok": false, "error": "request_timeout"}
	})

	_, missing, err := svUnread(t, f, nil).PriorityActivity(ctx, []string{"alpha", "beta"}, float64(svNow-3600), 5)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context error", err)
	}
	for _, m := range missing {
		if strings.Contains(m, "fetch failed") {
			t.Fatalf("a deadline cut was reported as a missing channel: %v", missing)
		}
	}
}

func TestUnreadOwnThreads_ReportsTheDeadline(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svAuthOK(f)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		{"ts": "1700099000.000100", "user": "U1", "text": "mine", "channel": svJSON{"id": "C1", "name": "alpha"}},
		{"ts": "1700099100.000100", "user": "U1", "text": "mine too", "channel": svJSON{"id": "C2", "name": "beta"}},
	}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.on("conversations.replies", func(*http.Request) svJSON {
		cancel()
		return svJSON{"ok": false, "error": "request_timeout"}
	})

	_, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(ctx, 24)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context error", err)
	}
	if n := f.count("conversations.replies"); n != 1 {
		t.Fatalf("conversations.replies called %d times after the deadline, want 1", n)
	}
}

// ------------------------------------------------------------ page reuse

func svDMSweep(f *svFake) {
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("D1", "", svJSON{"is_im": true}),
	}})
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("D1", "", svJSON{
		"is_im": true, "last_read": "1700099500.000000",
	})})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700099500.000000", "U2", "already read", nil),
	}})
}

func TestRecentDMActivityAfterUnread_ReusesThePage(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svDMSweep(f)
	svc := svUnread(t, f, nil)

	if _, err := svc.UnreadAll(context.Background(), 5); err != nil {
		t.Fatalf("UnreadAll: %v", err)
	}
	got, err := svc.RecentDMActivityAfterUnread(context.Background(), 2, 5)
	if err != nil {
		t.Fatalf("RecentDMActivityAfterUnread: %v", err)
	}
	if len(got) != 1 || got[0].Messages[0].Text != "already read" {
		t.Fatalf("got = %+v", got)
	}
	if n := f.count("conversations.history"); n != 1 {
		t.Fatalf("conversations.history called %d times, want 1", n)
	}
}

func TestRecentDMActivity_NeverReusesAPage(t *testing.T) {
	// get_mentions has no cursor capped at a sweep start, so its DM
	// backstop must always read fresh.
	f := svServer(t)
	svFreezeClock(t, svNow)
	svDMSweep(f)
	svc := svUnread(t, f, nil)

	if _, err := svc.UnreadAll(context.Background(), 5); err != nil {
		t.Fatalf("UnreadAll: %v", err)
	}
	if _, err := svc.RecentDMActivity(context.Background(), 2, 5); err != nil {
		t.Fatalf("RecentDMActivity: %v", err)
	}
	if n := f.count("conversations.history"); n != 2 {
		t.Fatalf("conversations.history called %d times, want 2", n)
	}
}

func TestHistoryPages(t *testing.T) {
	now := time.Unix(1700000000, 0)
	p := historyPages{now: func() time.Time { return now }}
	msgs := []goslack.Message{threadMsg("A", "3.0"), threadMsg("A", "2.0"), threadMsg("A", "1.0")}

	p.put("D1", msgs, 3)
	if got, ok := p.get("D1", 2); !ok || len(got) != 2 || got[0].Timestamp != "3.0" {
		t.Fatalf("a smaller limit takes the newest messages: %v %v", got, ok)
	}
	if _, ok := p.get("D1", 2); ok {
		t.Fatal("a page is lent once")
	}

	p.put("D1", msgs, 3)
	if _, ok := p.get("D1", 4); ok {
		t.Fatal("a page read with a smaller limit cannot stand in for a larger one")
	}

	p.put("D1", msgs, 3)
	now = now.Add(historyPageTTL + time.Second)
	if _, ok := p.get("D1", 3); ok {
		t.Fatal("a stale page must be read again")
	}

	p.put("D1", msgs, 3)
	p.reset()
	if _, ok := p.get("D1", 3); ok {
		t.Fatal("reset forgets every page")
	}
}
