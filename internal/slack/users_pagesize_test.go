package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	goslack "github.com/slack-go/slack"
)

func newTestUserService(f *fakeSlack) *UserService {
	api := goslack.New("test", goslack.OptionAPIURL(f.apiURL()))
	return newUserService(api, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// A page Slack can't serve fails with fatal_error; a smaller one works.
func TestUserList_RetriesWithSmallerPages(t *testing.T) {
	f := newFakeSlack(t)
	var limits []string
	f.on("users.list", func(r *http.Request) any {
		_ = r.ParseForm()
		limits = append(limits, r.Form.Get("limit"))
		if r.Form.Get("limit") != "50" {
			return map[string]any{"ok": false, "error": "fatal_error"}
		}
		return map[string]any{"ok": true, "members": []map[string]any{{"id": "U1", "name": "sam"}}}
	})

	users, err := newTestUserService(f).List(context.Background())

	if err != nil || len(users) != 1 || users[0].ID != "U1" {
		t.Fatalf("users=%v err=%v", users, err)
	}
	if got := len(limits); got != 3 || limits[0] != "200" || limits[1] != "100" || limits[2] != "50" {
		t.Fatalf("page sizes tried: %v", limits)
	}
}

func TestUserList_GivesUpAtTheFloor(t *testing.T) {
	f := newFakeSlack(t)
	f.on("users.list", func(r *http.Request) any {
		return map[string]any{"ok": false, "error": "fatal_error"}
	})

	_, err := newTestUserService(f).List(context.Background())

	if err == nil {
		t.Fatal("want the error once the smallest page also fails")
	}
	if n := f.callCount("users.list"); n != 4 { // 200, 100, 50, 25
		t.Fatalf("calls=%d, want 4", n)
	}
}

// An auth or scope error is not about page size: no retries.
func TestUserList_OtherErrorsAreNotRetried(t *testing.T) {
	f := newFakeSlack(t)
	f.on("users.list", func(r *http.Request) any {
		return map[string]any{"ok": false, "error": "missing_scope"}
	})

	if _, err := newTestUserService(f).List(context.Background()); err == nil {
		t.Fatal("want missing_scope")
	}
	if n := f.callCount("users.list"); n != 1 {
		t.Fatalf("calls=%d, want 1", n)
	}
}
