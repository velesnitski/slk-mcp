package slack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	goslack "github.com/slack-go/slack"
)

// fakeSlack stands in for the Slack Web API. Each handler is a function
// keyed by method name (without "/api/" prefix) that returns the JSON body.
//
// Tests register handlers per-method; the server records every call so
// tests can assert on shape and counts.
type fakeSlack struct {
	t        *testing.T
	mu       sync.Mutex
	handlers map[string]func(r *http.Request) any
	calls    map[string]int32
	server   *httptest.Server
}

func newFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	f := &fakeSlack{
		t:        t,
		handlers: make(map[string]func(r *http.Request) any),
		calls:    make(map[string]int32),
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeSlack) serve(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/")
	f.mu.Lock()
	f.calls[method]++
	h := f.handlers[method]
	f.mu.Unlock()

	if h == nil {
		f.t.Errorf("unexpected slack call: %s", method)
		http.Error(w, "no handler", http.StatusNotImplemented)
		return
	}
	body := h(r)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		f.t.Errorf("encode response: %v", err)
	}
}

func (f *fakeSlack) on(method string, h func(r *http.Request) any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

func (f *fakeSlack) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return int(f.calls[method])
}

// apiURL returns a URL the slack-go client will accept via OptionAPIURL.
// slack-go expects the URL to end with a slash and concatenates "method"
// directly to it.
func (f *fakeSlack) apiURL() string { return f.server.URL + "/" }

// newTestUnreadService builds an UnreadService wired to the fake server.
func newTestUnreadService(t *testing.T, f *fakeSlack) *UnreadService {
	t.Helper()
	api := goslack.New("xoxp-test", goslack.OptionAPIURL(f.apiURL()))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	users := newUserService(api, log)
	channels := newChannelService(api, users, log)
	return newUnreadService(api, channels, users, log)
}

// channelInfo is a minimal subset of conversations.info we set in tests.
type channelInfo struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	LastRead           string `json:"last_read"`
	UnreadCount        int    `json:"unread_count"`
	UnreadCountDisplay int    `json:"unread_count_display"`
	IsArchived         bool   `json:"is_archived"`
}

func okInfoResponse(ch channelInfo) map[string]any {
	return map[string]any{
		"ok":      true,
		"channel": ch,
	}
}

// ----------------------- Enabled / token gating -----------------------

func TestUnreadService_Enabled(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	disabled := newUnreadService(nil, nil, nil, log)
	if disabled.Enabled() {
		t.Fatalf("Enabled() = true on nil api; want false")
	}

	api := goslack.New("xoxp-test")
	enabled := newUnreadService(api, nil, nil, log)
	if !enabled.Enabled() {
		t.Fatalf("Enabled() = false on configured api; want true")
	}
}

func TestUnread_RequiresUserToken(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newUnreadService(nil, nil, nil, log)

	if _, err := s.Unread(context.Background(), "C1", 10); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("Unread err = %v; want ErrNoUserToken", err)
	}
	if _, err := s.UnreadAll(context.Background(), 10); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("UnreadAll err = %v; want ErrNoUserToken", err)
	}
	if _, err := s.JoinedChannels(context.Background()); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("JoinedChannels err = %v; want ErrNoUserToken", err)
	}
	if err := s.MarkRead(context.Background(), "C1", "1.0"); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("MarkRead err = %v; want ErrNoUserToken", err)
	}
}

// ----------------------- Unread (single channel) -----------------------

func TestUnread_DoesNotTrustZeroUnreadCount(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.info", func(r *http.Request) any {
		return okInfoResponse(channelInfo{
			ID: "C1", Name: "general",
			LastRead:    "1700000000.000000",
			UnreadCount: 0,
		})
	})
	f.on("conversations.history", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"type": "message", "user": "U1", "text": "muted-channel update", "ts": "1700000100.000000"},
			},
		}
	})

	s := newTestUnreadService(t, f)
	cu, err := s.Unread(context.Background(), "C1", 10)
	if err != nil {
		t.Fatalf("Unread err: %v", err)
	}
	if len(cu.Messages) != 1 {
		t.Fatalf("muted channels (unread_count=0 with new messages) must surface, got %d", len(cu.Messages))
	}
	if got := f.callCount("conversations.history"); got != 1 {
		t.Fatalf("history call count = %d; want 1 (last_read drives the fetch)", got)
	}
}

func TestUnread_NoLastReadShortCircuits(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.info", func(r *http.Request) any {
		return okInfoResponse(channelInfo{
			ID:          "C1",
			Name:        "general",
			LastRead:    "",
			UnreadCount: 5,
		})
	})
	s := newTestUnreadService(t, f)

	cu, err := s.Unread(context.Background(), "C1", 10)
	if err != nil {
		t.Fatalf("Unread err: %v", err)
	}
	if len(cu.Messages) != 0 {
		t.Fatalf("no last_read baseline → expected 0 messages, got %d", len(cu.Messages))
	}
}

func TestUnread_FetchesMessagesNewerThanLastRead(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.info", func(r *http.Request) any {
		return okInfoResponse(channelInfo{
			ID:          "C1",
			Name:        "general",
			LastRead:    "1700000000.000000",
			UnreadCount: 3,
		})
	})

	var gotOldest, gotChannel, gotLimit string
	f.on("conversations.history", func(r *http.Request) any {
		_ = r.ParseForm()
		gotChannel = r.Form.Get("channel")
		gotOldest = r.Form.Get("oldest")
		gotLimit = r.Form.Get("limit")
		return map[string]any{
			"ok": true,
			"messages": []map[string]any{
				{"type": "message", "user": "U1", "text": "hi", "ts": "1700000100.000000"},
				{"type": "message", "user": "U2", "text": "yo", "ts": "1700000050.000000"},
				// boundary message: ts == last_read; must be filtered out by Unread.
				{"type": "message", "user": "U3", "text": "old", "ts": "1700000000.000000"},
			},
		}
	})

	s := newTestUnreadService(t, f)
	cu, err := s.Unread(context.Background(), "C1", 25)
	if err != nil {
		t.Fatalf("Unread err: %v", err)
	}

	if gotChannel != "C1" {
		t.Fatalf("channel param = %q; want C1", gotChannel)
	}
	if gotOldest != "1700000000.000000" {
		t.Fatalf("oldest param = %q; want 1700000000.000000", gotOldest)
	}
	if gotLimit != "25" {
		t.Fatalf("limit param = %q; want 25", gotLimit)
	}
	if len(cu.Messages) != 2 {
		t.Fatalf("expected 2 messages newer than last_read, got %d", len(cu.Messages))
	}
	for _, m := range cu.Messages {
		if m.Timestamp == "1700000000.000000" {
			t.Fatalf("boundary message (ts == last_read) leaked through")
		}
	}
	if cu.LastRead != "1700000000.000000" {
		t.Fatalf("ChannelUnread.LastRead = %q; want 1700000000.000000", cu.LastRead)
	}
	if cu.Channel.ID != "C1" {
		t.Fatalf("ChannelUnread.Channel.ID = %q; want C1", cu.Channel.ID)
	}
}

func TestUnread_DefaultsMaxMessagesWhenNonPositive(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.info", func(r *http.Request) any {
		return okInfoResponse(channelInfo{
			ID:          "C1",
			Name:        "general",
			LastRead:    "1700000000.000000",
			UnreadCount: 1,
		})
	})

	var gotLimit string
	f.on("conversations.history", func(r *http.Request) any {
		_ = r.ParseForm()
		gotLimit = r.Form.Get("limit")
		return map[string]any{"ok": true, "messages": []any{}}
	})

	s := newTestUnreadService(t, f)
	if _, err := s.Unread(context.Background(), "C1", 0); err != nil {
		t.Fatalf("Unread err: %v", err)
	}
	if gotLimit != "50" {
		t.Fatalf("limit when max=0 = %q; want 50 (default)", gotLimit)
	}
}

// ----------------------- UnreadAll (regression) -----------------------

// TestUnreadAll_DoesNotTrustJoinedChannelsUnreadCount is the regression test
// for the bug that returned "0 unread" while Slack's UI showed unreads.
//
// users.conversations does not populate unread_count; the only authoritative
// source is conversations.info. UnreadAll must call info on every joined
// channel and rely on its short-circuit instead of pre-filtering on the
// list response.
func TestUnreadAll_DoesNotTrustJoinedChannelsUnreadCount(t *testing.T) {
	f := newFakeSlack(t)

	f.on("users.conversations", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"channels": []map[string]any{
				// All three look "caught up" in the list response —
				// because users.conversations never populates these fields.
				{"id": "C1", "name": "alpha", "is_archived": false,
					"unread_count": 0, "unread_count_display": 0},
				{"id": "C2", "name": "beta", "is_archived": false,
					"unread_count": 0, "unread_count_display": 0},
				{"id": "C3", "name": "gamma", "is_archived": false,
					"unread_count": 0, "unread_count_display": 0},
			},
			"response_metadata": map[string]any{"next_cursor": ""},
		}
	})

	infoByID := map[string]channelInfo{
		"C1": {ID: "C1", Name: "alpha", LastRead: "1700000000.000000", UnreadCount: 2},
		"C2": {ID: "C2", Name: "beta", LastRead: "1700000000.000000", UnreadCount: 0}, // truly caught up
		"C3": {ID: "C3", Name: "gamma", LastRead: "1700000000.000000", UnreadCount: 1},
	}
	f.on("conversations.info", func(r *http.Request) any {
		_ = r.ParseForm()
		id := r.Form.Get("channel")
		return okInfoResponse(infoByID[id])
	})

	historyByID := map[string][]map[string]any{
		"C1": {
			{"type": "message", "user": "U1", "text": "a1", "ts": "1700000100.000000"},
			{"type": "message", "user": "U2", "text": "a2", "ts": "1700000200.000000"},
		},
		"C3": {
			{"type": "message", "user": "U1", "text": "g1", "ts": "1700000150.000000"},
		},
	}
	f.on("conversations.history", func(r *http.Request) any {
		_ = r.ParseForm()
		id := r.Form.Get("channel")
		return map[string]any{"ok": true, "messages": historyByID[id]}
	})

	s := newTestUnreadService(t, f)
	results, err := s.UnreadAll(context.Background(), 50)
	if err != nil {
		t.Fatalf("UnreadAll err: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 channels with unread, got %d", len(results))
	}

	got := map[string]int{}
	for _, r := range results {
		got[r.Channel.ID] = len(r.Messages)
	}
	if got["C1"] != 2 {
		t.Errorf("C1 unread count = %d; want 2", got["C1"])
	}
	if got["C3"] != 1 {
		t.Errorf("C3 unread count = %d; want 1", got["C3"])
	}
	if _, ok := got["C2"]; ok {
		t.Errorf("C2 was caught up in conversations.info but appeared in results")
	}

	// info must be called for every joined channel — that's what makes
	// the fix work. 3 channels → 3 info calls.
	if got := f.callCount("conversations.info"); got != 3 {
		t.Errorf("conversations.info call count = %d; want 3", got)
	}
}

func TestUnreadAll_OmitsCaughtUpChannels(t *testing.T) {
	f := newFakeSlack(t)
	f.on("users.conversations", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"channels": []map[string]any{
				{"id": "C1", "name": "alpha"},
				{"id": "C2", "name": "beta"},
			},
			"response_metadata": map[string]any{"next_cursor": ""},
		}
	})
	f.on("conversations.info", func(r *http.Request) any {
		_ = r.ParseForm()
		id := r.Form.Get("channel")
		return okInfoResponse(channelInfo{
			ID: id, Name: id, LastRead: "1700000000.000000", UnreadCount: 0,
		})
	})
	f.on("conversations.history", func(r *http.Request) any {
		return map[string]any{"ok": true, "messages": []any{}}
	})

	s := newTestUnreadService(t, f)
	results, err := s.UnreadAll(context.Background(), 10)
	if err != nil {
		t.Fatalf("UnreadAll err: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("all channels caught up → expected 0 results, got %d", len(results))
	}
}

func TestUnreadAll_EmptyJoinedList(t *testing.T) {
	f := newFakeSlack(t)
	f.on("users.conversations", func(r *http.Request) any {
		return map[string]any{
			"ok":                true,
			"channels":          []any{},
			"response_metadata": map[string]any{"next_cursor": ""},
		}
	})

	s := newTestUnreadService(t, f)
	results, err := s.UnreadAll(context.Background(), 10)
	if err != nil {
		t.Fatalf("UnreadAll err: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty result, got %d", len(results))
	}
}

// ----------------------- JoinedChannels pagination -----------------------

func TestJoinedChannels_PaginatesAcrossCursors(t *testing.T) {
	f := newFakeSlack(t)

	page := int32(0)
	f.on("users.conversations", func(r *http.Request) any {
		switch atomic.AddInt32(&page, 1) {
		case 1:
			return map[string]any{
				"ok": true,
				"channels": []map[string]any{
					{"id": "C1", "name": "alpha"},
					{"id": "C2", "name": "beta"},
				},
				"response_metadata": map[string]any{"next_cursor": "cursor-2"},
			}
		case 2:
			_ = r.ParseForm()
			if got := r.Form.Get("cursor"); got != "cursor-2" {
				t.Errorf("page 2 cursor param = %q; want cursor-2", got)
			}
			return map[string]any{
				"ok": true,
				"channels": []map[string]any{
					{"id": "C3", "name": "gamma"},
				},
				"response_metadata": map[string]any{"next_cursor": ""},
			}
		default:
			t.Errorf("unexpected third page request")
			return map[string]any{"ok": true, "channels": []any{}}
		}
	})

	s := newTestUnreadService(t, f)
	channels, err := s.JoinedChannels(context.Background())
	if err != nil {
		t.Fatalf("JoinedChannels err: %v", err)
	}
	if len(channels) != 3 {
		t.Fatalf("expected 3 channels across 2 pages, got %d", len(channels))
	}
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, want := range wantNames {
		if channels[i].Name != want {
			t.Errorf("channels[%d].Name = %q; want %q", i, channels[i].Name, want)
		}
	}
}

// ----------------------- Self -----------------------

func TestSelf_CachesAuthTest(t *testing.T) {
	f := newFakeSlack(t)
	f.on("auth.test", func(r *http.Request) any {
		return map[string]any{
			"ok":      true,
			"user_id": "U_SELF",
			"user":    "tester",
		}
	})

	s := newTestUnreadService(t, f)
	for i := 0; i < 3; i++ {
		uid, err := s.Self(context.Background())
		if err != nil {
			t.Fatalf("Self err: %v", err)
		}
		if uid != "U_SELF" {
			t.Fatalf("Self() = %q; want U_SELF", uid)
		}
	}
	if got := f.callCount("auth.test"); got != 1 {
		t.Fatalf("auth.test called %d times; want 1 (cache miss only on first call)", got)
	}
}

func TestSelf_RequiresUserToken(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := newUnreadService(nil, nil, nil, log)
	if _, err := s.Self(context.Background()); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("Self err = %v; want ErrNoUserToken", err)
	}
}

// ----------------------- Thread replies -----------------------

func TestUnread_FetchesRepliesForThreadParents(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.info", func(r *http.Request) any {
		return okInfoResponse(channelInfo{
			ID:          "C1",
			Name:        "general",
			LastRead:    "1700000000.000000",
			UnreadCount: 2,
		})
	})
	f.on("conversations.history", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"messages": []map[string]any{
				// Thread parent — should trigger conversations.replies.
				{
					"type":         "message",
					"user":         "U2",
					"text":         "thread starts here",
					"ts":           "1700000100.000000",
					"thread_ts":    "1700000100.000000",
					"reply_count":  2,
				},
				// Plain top-level message — must NOT trigger replies fetch.
				{"type": "message", "user": "U2", "text": "plain", "ts": "1700000150.000000"},
			},
		}
	})

	var repliesParams []string
	f.on("conversations.replies", func(r *http.Request) any {
		_ = r.ParseForm()
		repliesParams = append(repliesParams, r.Form.Get("ts"))
		return map[string]any{
			"ok": true,
			"messages": []map[string]any{
				// Slack returns the parent first, replies after.
				{"type": "message", "user": "U2", "text": "thread starts here", "ts": "1700000100.000000"},
				// One reply older than last_read (must be filtered out).
				{"type": "message", "user": "U3", "text": "old reply", "ts": "1699999000.000000"},
				// One reply newer than last_read.
				{"type": "message", "user": "U4", "text": "new reply", "ts": "1700000200.000000"},
			},
			"has_more": false,
		}
	})

	s := newTestUnreadService(t, f)
	cu, err := s.Unread(context.Background(), "C1", 50)
	if err != nil {
		t.Fatalf("Unread err: %v", err)
	}

	if len(repliesParams) != 1 || repliesParams[0] != "1700000100.000000" {
		t.Fatalf("conversations.replies should be called once for the thread parent, got params=%v", repliesParams)
	}

	got := cu.Replies["1700000100.000000"]
	if len(got) != 1 {
		t.Fatalf("expected 1 reply newer than last_read, got %d (%v)", len(got), got)
	}
	if got[0].Text != "new reply" {
		t.Fatalf("expected the post-last_read reply, got %q", got[0].Text)
	}
	if _, ok := cu.Replies["1700000150.000000"]; ok {
		t.Fatalf("plain message must not have a Replies entry")
	}
}

func TestUnread_NoRepliesCallWhenZeroReplyCount(t *testing.T) {
	f := newFakeSlack(t)
	f.on("conversations.info", func(r *http.Request) any {
		return okInfoResponse(channelInfo{
			ID: "C1", Name: "general", LastRead: "1700000000.000000", UnreadCount: 1,
		})
	})
	f.on("conversations.history", func(r *http.Request) any {
		return map[string]any{
			"ok": true,
			"messages": []map[string]any{
				// Thread parent but no replies yet.
				{
					"type": "message", "user": "U1", "text": "x", "ts": "1700000100.000000",
					"thread_ts": "1700000100.000000", "reply_count": 0,
				},
			},
		}
	})
	// No conversations.replies handler — fakeSlack.serve() will t.Errorf
	// if it gets called. That's the assertion.

	s := newTestUnreadService(t, f)
	cu, err := s.Unread(context.Background(), "C1", 50)
	if err != nil {
		t.Fatalf("Unread err: %v", err)
	}
	if len(cu.Replies) != 0 {
		t.Fatalf("expected no Replies entries, got %d", len(cu.Replies))
	}
}

// ----------------------- MarkRead -----------------------

func TestMarkRead_PostsExpectedParams(t *testing.T) {
	f := newFakeSlack(t)

	var gotChannel, gotTS string
	f.on("conversations.mark", func(r *http.Request) any {
		var (
			err error
		)
		// conversations.mark posts form-encoded params.
		body, _ := io.ReadAll(r.Body)
		vals, err := url.ParseQuery(string(body))
		if err != nil {
			t.Errorf("parse mark body: %v", err)
		}
		gotChannel = vals.Get("channel")
		gotTS = vals.Get("ts")
		return map[string]any{"ok": true}
	})

	s := newTestUnreadService(t, f)
	if err := s.MarkRead(context.Background(), "C1", "1700000000.000000"); err != nil {
		t.Fatalf("MarkRead err: %v", err)
	}
	if gotChannel != "C1" {
		t.Errorf("mark channel = %q; want C1", gotChannel)
	}
	if gotTS != "1700000000.000000" {
		t.Errorf("mark ts = %q; want 1700000000.000000", gotTS)
	}
}
