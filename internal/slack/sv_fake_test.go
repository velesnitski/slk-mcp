package slack

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	goslack "github.com/slack-go/slack"
)

// svJSON is the shape of a canned Slack response body.
type svJSON map[string]any

// svFake is a stand-in for the Slack Web API, routed by method name.
//
// A method with no registered handler answers
// {"ok":false,"error":"not_mocked"}, so a call the test did not
// anticipate fails with a readable error instead of hanging or
// returning a plausible-looking empty result.
type svFake struct {
	mu       sync.Mutex
	handlers map[string]func(*http.Request) svJSON
	requests []svRequest
	server   *httptest.Server
}

// svRequest is one recorded call: the Slack method and the form the
// client actually put on the wire.
type svRequest struct {
	Method string
	Form   url.Values
}

// svServer starts a fake Slack API bound to the test's lifetime.
func svServer(t *testing.T) *svFake {
	t.Helper()
	f := &svFake{handlers: make(map[string]func(*http.Request) svJSON)}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *svFake) serve(w http.ResponseWriter, r *http.Request) {
	method := strings.TrimPrefix(r.URL.Path, "/")
	_ = r.ParseForm()

	form := url.Values{}
	for k, v := range r.Form {
		form[k] = append([]string(nil), v...)
	}

	f.mu.Lock()
	f.requests = append(f.requests, svRequest{Method: method, Form: form})
	h := f.handlers[method]
	f.mu.Unlock()

	body := svJSON{"ok": false, "error": "not_mocked"}
	if h != nil {
		body = h(r)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// on registers a handler for a Slack method (no "/api/" prefix).
func (f *svFake) on(method string, h func(*http.Request) svJSON) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

// reply registers a fixed response for a Slack method.
func (f *svFake) reply(method string, body svJSON) {
	f.on(method, func(*http.Request) svJSON { return body })
}

// fail registers a Slack-level error ({"ok":false,"error":code}).
func (f *svFake) fail(method, code string) {
	f.reply(method, svJSON{"ok": false, "error": code})
}

// pages replies with one body per call, repeating the last one once the
// script runs out. Models cursor pagination without bookkeeping in the
// test body.
func (f *svFake) pages(method string, bodies ...svJSON) {
	var n int
	f.on(method, func(*http.Request) svJSON {
		f.mu.Lock()
		i := n
		n++
		f.mu.Unlock()
		if i >= len(bodies) {
			i = len(bodies) - 1
		}
		return bodies[i]
	})
}

// count returns how many times a Slack method was called.
func (f *svFake) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int
	for _, req := range f.requests {
		if req.Method == method {
			n++
		}
	}
	return n
}

// forms returns every recorded form for a Slack method, in call order.
func (f *svFake) forms(method string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, req := range f.requests {
		if req.Method == method {
			out = append(out, req.Form)
		}
	}
	return out
}

// form returns the nth recorded form for a method, failing the test when
// that call never happened.
func (f *svFake) form(t *testing.T, method string, n int) url.Values {
	t.Helper()
	got := f.forms(method)
	if len(got) <= n {
		t.Fatalf("%s: wanted call #%d, only %d were made", method, n+1, len(got))
	}
	return got[n]
}

// url is the base the slack-go client is pointed at. slack-go appends
// the method name directly, so the trailing slash matters.
func (f *svFake) url() string { return f.server.URL + "/" }

// svLogger discards log output; the services log warnings liberally.
func svLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// svAPI builds a slack-go client aimed at the fake, carrying token so a
// handler can tell two identities apart.
func svAPI(f *svFake, token string) *goslack.Client {
	return goslack.New(token, goslack.OptionAPIURL(f.url()))
}

// svUserAPI is the conventional single-identity client for these tests.
func svUserAPI(f *svFake) *goslack.Client { return svAPI(f, "xoxp-test") }

// svChannels wires a ChannelService to the fake.
func svChannels(t *testing.T, f *svFake) *ChannelService {
	t.Helper()
	api := svUserAPI(f)
	log := svLogger()
	return newChannelService(api, newUserService(api, log), log)
}

// svMessages wires a MessageService to the fake with no user-token
// fallback (the single-credential shape).
func svMessages(t *testing.T, f *svFake) *MessageService {
	t.Helper()
	api := svUserAPI(f)
	log := svLogger()
	users := newUserService(api, log)
	return newMessageService(api, nil, newChannelService(api, users, log), users, log)
}

// svMessagesWithFallback wires a MessageService whose primary and user
// identities are distinct clients, so the download fallback is live.
func svMessagesWithFallback(t *testing.T, f *svFake, primaryToken, userToken string) *MessageService {
	t.Helper()
	primary := svAPI(f, primaryToken)
	user := svAPI(f, userToken)
	log := svLogger()
	users := newUserService(primary, log)
	return newMessageService(primary, user, newChannelService(primary, users, log), users, log)
}

// svUnread wires an UnreadService to the fake. search may be nil.
func svUnread(t *testing.T, f *svFake, search *SearchService) *UnreadService {
	t.Helper()
	api := svUserAPI(f)
	log := svLogger()
	users := newUserService(api, log)
	return newUnreadService(api, newChannelService(api, users, log), users, search, log)
}

// svSearch wires a SearchService to the fake.
func svSearch(t *testing.T, f *svFake) *SearchService {
	t.Helper()
	return newSearchService(svUserAPI(f), svLogger())
}

// svCanvas wires a CanvasService whose two identities are distinct
// clients, distinguishable by the token they send.
func svCanvas(t *testing.T, f *svFake, primaryToken, userToken string) *CanvasService {
	t.Helper()
	var user *goslack.Client
	if userToken != "" {
		user = svAPI(f, userToken)
	}
	return newCanvasService(svAPI(f, primaryToken), user, userToken, svLogger())
}

// svFreezeClock pins nowUnixFn for the duration of the test.
func svFreezeClock(t *testing.T, now int64) {
	t.Helper()
	prev := nowUnixFn
	nowUnixFn = func() int64 { return now }
	t.Cleanup(func() { nowUnixFn = prev })
}

// svChan is a channel payload for a conversations listing.
func svChan(id, name string, extra svJSON) svJSON {
	out := svJSON{"id": id, "name": name}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// svMsg is a message payload for a history/replies listing.
func svMsg(ts, user, text string, extra svJSON) svJSON {
	out := svJSON{"type": "message", "ts": ts, "user": user, "text": text}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// svCursor builds the response_metadata envelope Slack uses for
// cursor pagination.
func svCursor(next string) svJSON {
	return svJSON{"next_cursor": next}
}

// svHasSubstr reports whether haystack contains needle.
func svHasSubstr(haystack, needle string) bool { return strings.Contains(haystack, needle) }
