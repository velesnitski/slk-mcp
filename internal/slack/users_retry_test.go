package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

// newFailingUserService returns a UserService whose users.info always
// fails, plus a counter of how many times the API was actually reached.
func newFailingUserService(t *testing.T) (*UserService, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":false,"error":"user_not_found"}`)
	}))
	t.Cleanup(srv.Close)

	api := goslack.New("xoxp-test", goslack.OptionAPIURL(srv.URL+"/"))
	s := newUserService(api, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, &calls
}

func TestUserService_FailureIsNotCachedForever(t *testing.T) {
	// The bug this guards: a single transient users.info failure used to
	// be written into the success cache, so that person rendered as a raw
	// ID on every surface for the rest of the process's life.
	s, calls := newFailingUserService(t)
	base := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }

	if got := s.Name(context.Background(), "U0TEST0001"); got != "U0TEST0001" {
		t.Fatalf("failed lookup must fall back to the id; got %q", got)
	}
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("first lookup should hit the API once; got %d", n)
	}

	// Inside the retry window: answered from memory, no second call.
	s.now = func() time.Time { return base.Add(userResolveRetryAfter / 2) }
	_ = s.Name(context.Background(), "U0TEST0001")
	if n := atomic.LoadInt32(calls); n != 1 {
		t.Fatalf("within the retry window the API must not be called again; got %d calls", n)
	}

	// Past the window: try again, so a transient error can heal.
	s.now = func() time.Time { return base.Add(userResolveRetryAfter + time.Second) }
	_ = s.Name(context.Background(), "U0TEST0001")
	if n := atomic.LoadInt32(calls); n != 2 {
		t.Fatalf("after the retry window the API must be tried again; got %d calls", n)
	}
}

func TestUserService_SuccessClearsTheFailure(t *testing.T) {
	var ok atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if ok.Load() {
			_, _ = io.WriteString(w, `{"ok":true,"user":{"id":"U0TEST0002","name":"handle","real_name":"Real Name"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":false,"error":"ratelimited"}`)
	}))
	defer srv.Close()

	api := goslack.New("xoxp-test", goslack.OptionAPIURL(srv.URL+"/"))
	s := newUserService(api, slog.New(slog.NewTextHandler(io.Discard, nil)))
	base := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }

	if got := s.Name(context.Background(), "U0TEST0002"); got != "U0TEST0002" {
		t.Fatalf("expected id fallback while the API is failing; got %q", got)
	}

	ok.Store(true)
	s.now = func() time.Time { return base.Add(userResolveRetryAfter + time.Second) }
	if got := s.Name(context.Background(), "U0TEST0002"); got != "Real Name (handle)" {
		t.Fatalf("recovered lookup should render the name; got %q", got)
	}

	s.mu.RLock()
	_, stillFailed := s.failed["U0TEST0002"]
	s.mu.RUnlock()
	if stillFailed {
		t.Error("a successful resolution must clear the recorded failure")
	}
}

func TestUserService_EmptyIDNeverCallsTheAPI(t *testing.T) {
	s, calls := newFailingUserService(t)
	if got := s.Name(context.Background(), ""); got != "" {
		t.Fatalf("empty id must render empty; got %q", got)
	}
	if n := atomic.LoadInt32(calls); n != 0 {
		t.Fatalf("empty id must not reach the API; got %d calls", n)
	}
}
