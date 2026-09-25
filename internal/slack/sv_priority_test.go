package slack

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// ADR 112 — priority channels are fetched for their window regardless of
// last_read.

func svPriorityServer(t *testing.T) *svFake {
	t.Helper()
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil),
		svChan("C2", "beta", nil),
	}})
	f.on("conversations.info", func(r *http.Request) svJSON {
		return svJSON{"ok": true, "channel": svChan(r.FormValue("channel"), "", svJSON{"last_read": "1700099900.000000"})}
	})
	f.on("conversations.history", func(r *http.Request) svJSON {
		return svJSON{"ok": true, "messages": []svJSON{
			svMsg("1700099800.000000", "U2", "decision made here", nil),
			svMsg("1700000000.000000", "U2", "last week", nil),
		}}
	})
	return f
}

func TestPriorityActivity_ReturnsTheWindowRegardlessOfLastRead(t *testing.T) {
	f := svPriorityServer(t)

	got, missing, err := svUnread(t, f, nil).PriorityActivity(context.Background(),
		[]string{"#alpha"}, 1700092800, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if len(got) != 1 || got[0].Channel.ID != "C1" || !got[0].Priority {
		t.Fatalf("got = %+v, want C1 marked Priority", got)
	}
	// last_read is AFTER the message — the channel is read — and it is
	// still returned: that is the point of a priority channel.
	if got[0].LastRead != "1700099900.000000" {
		t.Fatalf("last_read = %q, want it filled from conversations.info", got[0].LastRead)
	}
	if n := len(got[0].Messages); n != 1 || got[0].Messages[0].Text != "decision made here" {
		t.Fatalf("messages = %+v, want only the in-window one", got[0].Messages)
	}
	if oldest := f.form(t, "conversations.history", 0).Get("oldest"); oldest != "" {
		t.Fatalf("history must be anchored at now (ADR 111), got oldest=%q", oldest)
	}
}

func TestPriorityActivity_MatchesByNameOrIDAndDeduplicates(t *testing.T) {
	f := svPriorityServer(t)

	got, missing, err := svUnread(t, f, nil).PriorityActivity(context.Background(),
		[]string{"ALPHA", "C1", "beta"}, 1700092800, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want alpha once (by name and by ID) plus beta", len(got))
	}
}

func TestPriorityActivity_ReportsAChannelItCannotFind(t *testing.T) {
	// A typo or a lost membership must be visible, not a priority channel
	// that silently stops appearing.
	f := svPriorityServer(t)

	got, missing, err := svUnread(t, f, nil).PriorityActivity(context.Background(),
		[]string{"alpha", "gamma"}, 1700092800, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("the found channel must still be returned, got %d", len(got))
	}
	if len(missing) != 1 || missing[0] != "gamma" {
		t.Fatalf("missing = %v, want [gamma]", missing)
	}
}

func TestPriorityActivity_EmptyListIsANoOp(t *testing.T) {
	f := svPriorityServer(t)
	got, missing, err := svUnread(t, f, nil).PriorityActivity(context.Background(), nil, 0, 20)
	if err != nil || got != nil || missing != nil {
		t.Fatalf("want nothing, got %v %v %v", got, missing, err)
	}
	if f.count("users.conversations") != 0 {
		t.Fatal("an empty list must not call Slack")
	}
}

func TestPriorityActivity_WithoutAUserTokenIsTheSentinel(t *testing.T) {
	if _, _, err := svDisabledUnread().PriorityActivity(context.Background(), []string{"alpha"}, 0, 20); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("err = %v, want ErrNoUserToken", err)
	}
}
