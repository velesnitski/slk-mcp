package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func newTestUserService(f *fakeSlack) *UserService {
	api := goslack.New("test", goslack.OptionAPIURL(f.apiURL()))
	return newUserService(api, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func member(id string) map[string]any { return map[string]any{"id": id, "name": id} }

// Two pages; the second fails once on Slack's side and is retried on
// its own cursor – the first page is not fetched again.
func TestUserList_RetriesTheFailedPageOnly(t *testing.T) {
	f := newFakeSlack(t)
	var cursors []string
	failed := false
	f.on("users.list", func(r *http.Request) any {
		_ = r.ParseForm()
		c := r.Form.Get("cursor")
		cursors = append(cursors, c)
		if c == "" {
			return map[string]any{"ok": true, "members": []any{member("U1")}, "response_metadata": map[string]any{"next_cursor": "p2"}}
		}
		if !failed {
			failed = true
			return map[string]any{"ok": false, "error": "fatal_error"}
		}
		return map[string]any{"ok": true, "members": []any{member("U2")}, "response_metadata": map[string]any{"next_cursor": ""}}
	})

	users, err := newTestUserService(f).List(context.Background())

	if err != nil || len(users) != 2 {
		t.Fatalf("users=%v err=%v", users, err)
	}
	if strings.Join(cursors, ",") != ",p2,p2" {
		t.Fatalf("cursors requested: %q", cursors)
	}
}

// A page that keeps failing gives up after userPageAttempts and the
// error says where the listing broke.
func TestUserList_ErrorNamesThePage(t *testing.T) {
	f := newFakeSlack(t)
	f.on("users.list", func(r *http.Request) any {
		_ = r.ParseForm()
		if r.Form.Get("cursor") == "" {
			return map[string]any{"ok": true, "members": []any{member("U1"), member("U2")}, "response_metadata": map[string]any{"next_cursor": "p2"}}
		}
		return map[string]any{"ok": false, "error": "fatal_error"}
	})

	_, err := newTestUserService(f).List(context.Background())

	if err == nil || !strings.Contains(err.Error(), "users.list page 2 (after 2 users") || !strings.Contains(err.Error(), "fatal_error") {
		t.Fatalf("err=%v", err)
	}
	if n := f.callCount("users.list"); n != 1+userPageAttempts {
		t.Fatalf("calls=%d, want %d", n, 1+userPageAttempts)
	}
}

// An auth or scope error is not retried.
func TestUserList_OtherErrorsAreNotRetried(t *testing.T) {
	f := newFakeSlack(t)
	f.on("users.list", func(r *http.Request) any {
		return map[string]any{"ok": false, "error": "missing_scope"}
	})

	if _, err := newTestUserService(f).List(context.Background()); err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("want missing_scope, got %v", err)
	}
	if n := f.callCount("users.list"); n != 1 {
		t.Fatalf("calls=%d, want 1", n)
	}
}

// Deleted accounts are dropped, as before.
func TestUserList_SkipsDeleted(t *testing.T) {
	f := newFakeSlack(t)
	f.on("users.list", func(r *http.Request) any {
		return map[string]any{"ok": true, "members": []any{member("U1"), map[string]any{"id": "U2", "deleted": true}}}
	})

	users, err := newTestUserService(f).List(context.Background())

	if err != nil || len(users) != 1 || users[0].ID != "U1" {
		t.Fatalf("users=%v err=%v", users, err)
	}
}
