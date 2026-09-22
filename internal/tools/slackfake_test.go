package tools

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// fakeSlack is a programmable stand-in for the Slack Web API, wired in
// through config.APIURL so a test drives the real code path end to end:
// tool handler → service layer → slack-go → HTTP.
//
// Prefer it over hand-built interface fakes when the behaviour under
// test spans layers. A fake that satisfies UserClient proves the
// handler calls the method; this proves the request slack-go actually
// sends is one Slack would accept, which is where the bugs have been.
//
// Unrouted methods answer `{"ok":false,"error":"not_mocked"}` and are
// still recorded, so a handler that reaches for an endpoint the test
// did not anticipate surfaces as an assertion on Calls() rather than a
// hang or a live request.
type fakeSlack struct {
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]func(*http.Request) string
	calls  []string
}

func newFakeSlack(t *testing.T) *fakeSlack {
	t.Helper()
	f := &fakeSlack{routes: map[string]func(*http.Request) string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/")
		_ = r.ParseForm()

		f.mu.Lock()
		f.calls = append(f.calls, method)
		fn := f.routes[method]
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if fn == nil {
			_, _ = io.WriteString(w, `{"ok":false,"error":"not_mocked"}`)
			return
		}
		_, _ = io.WriteString(w, fn(r))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// On routes a Slack API method (e.g. "users.info") to a fixed JSON body.
func (f *fakeSlack) On(method, body string) *fakeSlack {
	return f.OnFunc(method, func(*http.Request) string { return body })
}

// OnFunc routes a method to a handler that may inspect the form the
// caller posted — use it to assert on the arguments a service builds.
func (f *fakeSlack) OnFunc(method string, fn func(*http.Request) string) *fakeSlack {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method] = fn
	return f
}

// OnError routes a method to a Slack-shaped error response.
func (f *fakeSlack) OnError(method, code string) *fakeSlack {
	return f.On(method, `{"ok":false,"error":"`+code+`"}`)
}

// Calls returns the API methods requested so far, in order.
func (f *fakeSlack) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Called reports whether method was requested at least once.
func (f *fakeSlack) Called(method string) bool {
	for _, c := range f.Calls() {
		if c == method {
			return true
		}
	}
	return false
}

// URL is the API base, trailing slash included as slack-go expects.
func (f *fakeSlack) URL() string { return f.srv.URL + "/" }

// newFakeConfig returns a validated Config pointed at f. Both tokens are
// set so bot-only and user-only branches are both reachable; pass opts to
// narrow that or to set any other field.
func newFakeConfig(t *testing.T, f *fakeSlack, opts ...func(*config.Config)) *config.Config {
	t.Helper()
	cfg := &config.Config{
		BotToken:              "xoxb-test",
		UserToken:             "xoxp-test",
		APIURL:                f.URL(),
		DigestHours:           24,
		MaxMessagesPerChannel: 50,
		AutodiscoverLimit:     50,
		DisabledTools:         map[string]struct{}{},
	}
	for _, opt := range opts {
		opt(cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config validate: %v", err)
	}
	return cfg
}

// newFakeHub builds a Hub whose Slack client talks to f.
func newFakeHub(t *testing.T, f *fakeSlack, opts ...func(*config.Config)) *Hub {
	t.Helper()
	cfg := newFakeConfig(t, f, opts...)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewHub(slack.New(cfg, log), cfg, log)
}

// callToolRequest builds an mcp.CallToolRequest carrying args — the shape
// handlers receive from the MCP server.
func callToolRequest(name string, args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	return req
}

// jsonBody marshals v to a JSON string, for building fake responses from
// Go values instead of hand-written literals.
func jsonBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fake response: %v", err)
	}
	return string(b)
}
