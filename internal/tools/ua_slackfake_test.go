package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// A programmable Slack Web API for handler-level tests.
//
// The tools package builds its Slack client through slack.New, which has
// no seam for an alternative API base URL, so the fake intercepts at the
// transport layer instead: every request aimed at the real Slack host is
// answered from an in-memory routing table, and everything else is passed
// through to the transport that was installed before. No sockets, no
// ports, no goroutines — a call is a function call.
//
// Unrouted methods answer {"ok":false,"error":"not_mocked"} and are still
// recorded, so an endpoint a test did not anticipate shows up as a
// readable assertion failure instead of a hang.

// uaSlackHost is the host slack-go targets by default.
const uaSlackHost = "slack.com"

type uaFakeSlack struct {
	t      *testing.T
	mu     sync.Mutex
	routes map[string]func(*http.Request) string
	calls  []string
}

type uaTransport struct{}

var (
	uaRealRT   http.RoundTripper
	uaActiveMu sync.Mutex
	uaActive   *uaFakeSlack
)

// init installs the interceptor before any test runs, so the global is
// never written while another goroutine could be reading it.
func init() { uaInstall() }

func uaInstall() {
	if _, already := http.DefaultTransport.(uaTransport); already {
		return
	}
	uaRealRT = http.DefaultTransport
	http.DefaultTransport = uaTransport{}
}

func (uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL == nil || r.URL.Host != uaSlackHost {
		return uaRealRT.RoundTrip(r)
	}
	uaActiveMu.Lock()
	f := uaActive
	uaActiveMu.Unlock()
	if f == nil {
		// No ua fake is active, so this request belongs to another
		// harness further down the chain (this package has three,
		// installed in file-name order). Delegating rather than
		// erroring is what lets them compose — claiming every
		// slack.com request unconditionally would make whichever
		// transport installed last the only one that works.
		return uaRealRT.RoundTrip(r)
	}
	return f.serve(r)
}

// uaNewFakeSlack makes f the fake answering Slack traffic for the
// duration of t, restoring whatever was active before on cleanup.
func uaNewFakeSlack(t *testing.T) *uaFakeSlack {
	t.Helper()
	uaInstall()
	f := &uaFakeSlack{t: t, routes: map[string]func(*http.Request) string{}}
	uaActiveMu.Lock()
	prev := uaActive
	uaActive = f
	uaActiveMu.Unlock()
	t.Cleanup(func() {
		uaActiveMu.Lock()
		uaActive = prev
		uaActiveMu.Unlock()
	})
	return f
}

func (f *uaFakeSlack) serve(r *http.Request) (*http.Response, error) {
	method := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api"), "/")

	form := url.Values{}
	if r.Body != nil {
		raw, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if parsed, err := url.ParseQuery(string(raw)); err == nil {
			form = parsed
		}
	}
	for k, vs := range r.URL.Query() {
		form[k] = append(form[k], vs...)
	}

	f.mu.Lock()
	f.calls = append(f.calls, method)
	fn := f.routes[method]
	f.mu.Unlock()

	body := `{"ok":false,"error":"not_mocked"}`
	if fn != nil {
		shallow := *r
		shallow.Form = form
		shallow.PostForm = form
		body = fn(&shallow)
	}
	return &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

// On routes a Slack method name to a fixed JSON body.
func (f *uaFakeSlack) On(method, body string) {
	f.OnFunc(method, func(*http.Request) string { return body })
}

// OnFunc routes a Slack method name to a body computed from the request;
// the handler sees the decoded form in r.Form.
func (f *uaFakeSlack) OnFunc(method string, fn func(r *http.Request) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method] = fn
}

// OnError routes a method to a Slack-shaped error envelope.
func (f *uaFakeSlack) OnError(method, code string) {
	f.On(method, `{"ok":false,"error":"`+code+`"}`)
}

// Calls returns every Slack method invoked so far, in order.
func (f *uaFakeSlack) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// Called reports whether a Slack method was invoked at least once.
func (f *uaFakeSlack) Called(method string) bool { return f.CountOf(method) > 0 }

// CountOf returns how many times a Slack method was invoked.
func (f *uaFakeSlack) CountOf(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == method {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- hubs

func uaLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// uaHub builds a single-workspace Hub whose Slack client talks to f.
func uaHub(t *testing.T, f *uaFakeSlack, opts ...func(*config.Config)) *Hub {
	t.Helper()
	_ = f // the fake is wired in globally by uaNewFakeSlack
	cfg := &config.Config{UserToken: "xoxp-test"}
	for _, opt := range opts {
		opt(cfg)
	}
	log := uaLog()
	return NewHub(slack.New(cfg, log), cfg, log)
}

// uaMultiHub builds a two-workspace Hub ("primary" + "secondary"), both
// pointed at f. secondaryOpts can strip the secondary's user token to
// exercise the per-workspace skip path.
func uaMultiHub(t *testing.T, f *uaFakeSlack, secondaryOpts ...func(*config.Config)) *Hub {
	t.Helper()
	_ = f
	log := uaLog()
	primaryCfg := &config.Config{UserToken: "xoxp-test"}
	secondaryCfg := &config.Config{UserToken: "xoxp-second"}
	for _, opt := range secondaryOpts {
		opt(secondaryCfg)
	}
	reg := []slack.Workspace{
		{Name: "primary", Client: slack.New(primaryCfg, log)},
		{Name: "secondary", Client: slack.New(secondaryCfg, log)},
	}
	return NewHubWithRegistry(reg, primaryCfg, log)
}

// ------------------------------------------------------- JSON fixtures

func uaJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

// uaChannel builds one channel object; extra merges/overrides fields.
func uaChannel(id, name string, extra map[string]any) map[string]any {
	ch := map[string]any{"id": id, "name": name, "is_archived": false}
	for k, v := range extra {
		ch[k] = v
	}
	return ch
}

// uaMsg builds one conversations.history message; extra merges fields.
func uaMsg(ts, user, text string, extra map[string]any) map[string]any {
	m := map[string]any{"type": "message", "ts": ts, "user": user, "text": text}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// uaHit builds one search.messages match.
func uaHit(channelID, channelName, ts, user, text, permalink string) map[string]any {
	return map[string]any{
		"type":      "message",
		"channel":   map[string]any{"id": channelID, "name": channelName},
		"user":      user,
		"username":  "",
		"ts":        ts,
		"text":      text,
		"permalink": permalink,
	}
}

// uaTS renders a Slack timestamp `offset` seconds away from now, so
// hour-windowed backstops (thread mentions, own threads, DM window)
// see fixtures inside their cutoff without any wall-clock coupling.
func uaTS(offsetSeconds int64, usec int) string {
	return fmt.Sprintf("%d.%06d", time.Now().Unix()+offsetSeconds, usec)
}

// uaNowUnix is the wall clock the fixtures above are anchored to.
func uaNowUnix() int64 { return time.Now().Unix() }

// uaAuthOK is the auth.test body identifying the operator as U1/@alex.
const uaAuthOK = `{"ok":true,"user_id":"U1","user":"alex","team":"T1","team_id":"T1","url":"https://example.slack.com/"}`

// uaNoSearch routes the three search-backed backstops to empty results so
// a test can isolate the plain unread sweep.
func (f *uaFakeSlack) uaNoSearch() {
	f.On("search.messages", `{"ok":true,"messages":{"matches":[],"total":0}}`)
}

// uaNoCanvas routes the canvas delta's raw files.list to an empty page.
func (f *uaFakeSlack) uaNoCanvas() {
	f.On("files.list", `{"ok":true,"files":[]}`)
}

// uaUsers routes users.info from an id→display-name table; unknown ids
// answer user_not_found so the renderer falls back to the raw id.
func (f *uaFakeSlack) uaUsers(names map[string]string) {
	f.OnFunc("users.info", func(r *http.Request) string {
		id := r.Form.Get("user")
		n, ok := names[id]
		if !ok {
			return `{"ok":false,"error":"user_not_found"}`
		}
		return `{"ok":true,"user":{"id":"` + id + `","name":"` + n + `","real_name":"` + n + `","profile":{"display_name":"` + n + `"}}}`
	})
}

// uaConversations routes users.conversations to a fixed channel list.
func (f *uaFakeSlack) uaConversations(t *testing.T, channels ...map[string]any) {
	t.Helper()
	if channels == nil {
		channels = []map[string]any{}
	}
	f.On("users.conversations", uaJSON(t, map[string]any{
		"ok":                true,
		"channels":          channels,
		"response_metadata": map[string]any{"next_cursor": ""},
	}))
}

// uaInfo routes conversations.info from an id→channel table.
func (f *uaFakeSlack) uaInfo(t *testing.T, byID map[string]map[string]any) {
	t.Helper()
	bodies := map[string]string{}
	for id, ch := range byID {
		bodies[id] = uaJSON(t, map[string]any{"ok": true, "channel": ch})
	}
	f.OnFunc("conversations.info", func(r *http.Request) string {
		if b, ok := bodies[r.Form.Get("channel")]; ok {
			return b
		}
		return `{"ok":false,"error":"channel_not_found"}`
	})
}

// uaHistory routes conversations.history from an id→messages table.
func (f *uaFakeSlack) uaHistory(t *testing.T, byID map[string][]map[string]any) {
	t.Helper()
	bodies := map[string]string{}
	for id, msgs := range byID {
		bodies[id] = uaJSON(t, map[string]any{"ok": true, "messages": msgs, "has_more": false})
	}
	f.OnFunc("conversations.history", func(r *http.Request) string {
		if b, ok := bodies[r.Form.Get("channel")]; ok {
			return b
		}
		return `{"ok":true,"messages":[],"has_more":false}`
	})
}

// uaReplies routes conversations.replies from a threadTS→messages table.
func (f *uaFakeSlack) uaReplies(t *testing.T, byTS map[string][]map[string]any) {
	t.Helper()
	bodies := map[string]string{}
	for ts, msgs := range byTS {
		bodies[ts] = uaJSON(t, map[string]any{"ok": true, "messages": msgs, "has_more": false})
	}
	f.OnFunc("conversations.replies", func(r *http.Request) string {
		if b, ok := bodies[r.Form.Get("ts")]; ok {
			return b
		}
		return `{"ok":true,"messages":[],"has_more":false}`
	})
}
