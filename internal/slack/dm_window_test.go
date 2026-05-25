package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

// TestRecentDMActivity_filtersToDMsOnly verifies that DM-window fetch
// ignores non-DM channels even when users.conversations returns them.
// The whole feature exists for IM/MPIM coverage; including public
// channels would duplicate UnreadAll's work.
func TestRecentDMActivity_filtersToDMsOnly(t *testing.T) {
	f := newFakeSlack(t)

	// Pin time so the test is deterministic — the cutoff `now - hours`
	// must be the same on every run.
	prev := nowUnixFn
	nowUnixFn = func() int64 { return 1700100000 }
	t.Cleanup(func() { nowUnixFn = prev })

	f.on("users.conversations", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C_PUB", "name": "general", "is_archived": false},
				{"id": "D_DM1", "name": "", "is_im": true, "is_archived": false},
				{"id": "G_MPDM", "name": "mpdm-a--b--c-1", "is_mpim": true, "is_archived": false},
			},
			"response_metadata": map[string]any{"next_cursor": ""},
		}
	})

	// Only DM history should be fetched. We return content for both
	// DMs and assert that C_PUB never appears in the result.
	historyByID := map[string][]map[string]any{
		"D_DM1":  {{"type": "message", "user": "U1", "text": "private decision", "ts": "1700099000.000000"}},
		"G_MPDM": {{"type": "message", "user": "U2", "text": "group sync", "ts": "1700099500.000000"}},
	}
	f.on("conversations.history", func(r *http.Request) any {
		_ = r.ParseForm()
		id := r.Form.Get("channel")
		return map[string]any{"ok": true, "messages": historyByID[id]}
	})

	s := newTestUnreadService(t, f)
	results, err := s.RecentDMActivity(context.Background(), 1, 50)
	if err != nil {
		t.Fatalf("RecentDMActivity err: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 DM results; got %d", len(results))
	}
	for _, r := range results {
		if !r.Channel.IsIM && !r.Channel.IsMpIM {
			t.Fatalf("non-DM channel in result: %+v", r.Channel)
		}
	}
}

func TestRecentDMActivity_noopOnZeroHours(t *testing.T) {
	f := newFakeSlack(t)
	s := newTestUnreadService(t, f)

	out, err := s.RecentDMActivity(context.Background(), 0, 50)
	if err != nil {
		t.Fatalf("hours=0 must not error; got %v", err)
	}
	if out != nil {
		t.Fatalf("hours=0 must return nil slice; got %v", out)
	}
}

func TestRecentDMActivity_noopOnNegativeHours(t *testing.T) {
	f := newFakeSlack(t)
	s := newTestUnreadService(t, f)

	out, err := s.RecentDMActivity(context.Background(), -5, 50)
	if err != nil {
		t.Fatalf("hours<=0 must not error; got %v", err)
	}
	if out != nil {
		t.Fatalf("hours<=0 must return nil slice; got %v", out)
	}
}

func TestRecentDMActivity_requiresUserToken(t *testing.T) {
	// A disabled service (no api wired) must short-circuit with
	// ErrNoUserToken, never reach the listing call.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newUnreadService(nil, nil, nil, nil, log)
	_, err := s.RecentDMActivity(context.Background(), 24, 50)
	if err != ErrNoUserToken {
		t.Fatalf("expected ErrNoUserToken; got %v", err)
	}
}
