package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	goslack "github.com/slack-go/slack"
)

func newTestChannelServiceWithAPI(t *testing.T, f *fakeSlack) *ChannelService {
	t.Helper()
	api := goslack.New("xoxp-test", goslack.OptionAPIURL(f.apiURL()))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	users := newUserService(api, log)
	return newChannelService(api, users, log)
}

func TestArchive_callsConversationsArchive(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.archive", func(r *http.Request) any {
		_ = r.ParseForm()
		if got := r.Form.Get("channel"); got != "C0ABC1234DE" {
			t.Errorf("archive must pass channel ID; got %q", got)
		}
		return map[string]any{"ok": true}
	})

	s := newTestChannelServiceWithAPI(t, f)
	if err := s.Archive(context.Background(), "C0ABC1234DE"); err != nil {
		t.Fatalf("Archive err: %v", err)
	}
	if f.callCount("conversations.archive") != 1 {
		t.Fatalf("expected 1 archive call; got %d", f.callCount("conversations.archive"))
	}
}

func TestArchive_propagatesSlackError(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.archive", func(r *http.Request) any {
		// Common Slack error for archive on an already-archived channel.
		return map[string]any{"ok": false, "error": "already_archived"}
	})

	s := newTestChannelServiceWithAPI(t, f)
	err := s.Archive(context.Background(), "C_ARCHIVED")
	if err == nil {
		t.Fatal("expected Slack error to propagate")
	}
	// slack-go wraps the API error verbatim; the substring is enough.
	if !contains(err.Error(), "already_archived") {
		t.Fatalf("expected already_archived in error; got %v", err)
	}
}

func TestUnarchive_callsConversationsUnarchive(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.unarchive", func(r *http.Request) any {
		_ = r.ParseForm()
		if got := r.Form.Get("channel"); got != "C0DEAD0000" {
			t.Errorf("unarchive must pass channel ID; got %q", got)
		}
		return map[string]any{"ok": true}
	})

	s := newTestChannelServiceWithAPI(t, f)
	if err := s.Unarchive(context.Background(), "C0DEAD0000"); err != nil {
		t.Fatalf("Unarchive err: %v", err)
	}
	if f.callCount("conversations.unarchive") != 1 {
		t.Fatalf("expected 1 unarchive call; got %d", f.callCount("conversations.unarchive"))
	}
}

func TestUnarchive_propagatesSlackError(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.unarchive", func(r *http.Request) any {
		return map[string]any{"ok": false, "error": "not_archived"}
	})

	s := newTestChannelServiceWithAPI(t, f)
	err := s.Unarchive(context.Background(), "C_LIVE")
	if err == nil {
		t.Fatal("expected Slack error to propagate")
	}
	if !contains(err.Error(), "not_archived") {
		t.Fatalf("expected not_archived in error; got %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || stringContains(haystack, needle))
}

func stringContains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
