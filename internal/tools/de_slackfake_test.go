package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// A programmable Slack Web API, wired in underneath the real client so a
// test drives handler → service layer → slack-go → HTTP for real.
//
// slack-go pins its endpoint to https://slack.com/api/ at construction
// and internal/slack.New exposes no seam to change it, so the redirect
// happens one layer lower: a RoundTripper installed once (never mutated
// afterwards, so it is race-free) diverts requests for that host to
// whichever fake registered the token the request carries, and passes
// every other host straight through to the real transport. Routing by
// token rather than by a global "current server" keeps two workspaces —
// two clients, two tokens — independently addressable in one test.

const deAPIHost = "slack.com"

var (
	deRouteMu  sync.RWMutex
	deRoutes   = map[string]string{} // token → fake server base URL
	deTokenSeq atomic.Int64
)

func init() { http.DefaultTransport = &deTransport{base: http.DefaultTransport} }

type deTransport struct{ base http.RoundTripper }

func (t *deTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL == nil || r.URL.Host != deAPIHost {
		return t.base.RoundTrip(r)
	}
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	target, ok := deLookupRoute(deRequestToken(r, body))
	if !ok {
		return nil, fmt.Errorf("de: no fake Slack registered for this token (%s)", r.URL.Path)
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	u.Path = r.URL.Path
	u.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = r.Header.Clone()
	return t.base.RoundTrip(req)
}

func deLookupRoute(token string) (string, bool) {
	deRouteMu.RLock()
	defer deRouteMu.RUnlock()
	u, ok := deRoutes[token]
	return u, ok
}

// deRequestToken digs the credential out of wherever slack-go put it:
// form body for the POSTed methods, Bearer header for the JSON ones.
func deRequestToken(r *http.Request, body []byte) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if vals, err := url.ParseQuery(string(body)); err == nil {
		if tok := vals.Get("token"); tok != "" {
			return tok
		}
	}
	return r.URL.Query().Get("token")
}

// deFake is a programmable Slack Web API over httptest. Unrouted methods
// answer `not_mocked` and are still recorded, so an endpoint the test did
// not anticipate shows up as a readable assertion instead of a hang.
type deFake struct {
	t      *testing.T
	token  string
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]func(*http.Request) string
	calls  []string
}

func deNewFake(t *testing.T) *deFake {
	t.Helper()
	f := &deFake{t: t, routes: make(map[string]func(*http.Request) string)}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.token = f.newToken()
	t.Cleanup(f.srv.Close)
	return f
}

// newToken registers one more credential pointing at this same fake, so
// a multi-workspace Hub (one client per token) still lands here.
func (f *deFake) newToken() string {
	tok := "xoxp-" + "de" + strconv.FormatInt(deTokenSeq.Add(1), 10)
	deRouteMu.Lock()
	deRoutes[tok] = f.srv.URL
	deRouteMu.Unlock()
	f.t.Cleanup(func() {
		deRouteMu.Lock()
		delete(deRoutes, tok)
		deRouteMu.Unlock()
	})
	return tok
}

func (f *deFake) serve(w http.ResponseWriter, r *http.Request) {
	method := path.Base(r.URL.Path)
	_ = r.ParseForm()
	f.mu.Lock()
	f.calls = append(f.calls, method)
	h := f.routes[method]
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if h == nil {
		_, _ = io.WriteString(w, `{"ok":false,"error":"not_mocked"}`)
		return
	}
	_, _ = io.WriteString(w, h(r))
}

// on routes a Slack method to a fixed JSON body.
func (f *deFake) on(method, body string) {
	f.onFunc(method, func(*http.Request) string { return body })
}

// onFunc routes a Slack method to a handler that may inspect r.Form.
func (f *deFake) onFunc(method string, h func(*http.Request) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method] = h
}

// onError routes a Slack method to a Slack-shaped error.
func (f *deFake) onError(method, code string) {
	f.on(method, `{"ok":false,"error":"`+code+`"}`)
}

func (f *deFake) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *deFake) called(method string) bool { return f.callCount(method) > 0 }

func (f *deFake) callCount(method string) int {
	n := 0
	for _, c := range f.callList() {
		if c == method {
			n++
		}
	}
	return n
}

// deHub builds a single-workspace Hub whose Slack client talks to f.
func deHub(t *testing.T, f *deFake, mods ...func(*config.Config)) *Hub {
	t.Helper()
	cfg := &config.Config{
		UserToken:             f.token,
		DigestHours:           24,
		MaxMessagesPerChannel: 50,
		AutodiscoverLimit:     10,
	}
	for _, m := range mods {
		m(cfg)
	}
	log := testLog()
	return NewHub(slack.New(cfg, log), cfg, log)
}

// deMultiHub builds a Hub serving len(names) workspaces, every one of
// them backed by f (each with its own token).
func deMultiHub(t *testing.T, f *deFake, names []string, mods ...func(*config.Config)) *Hub {
	t.Helper()
	log := testLog()
	reg := make([]slack.Workspace, 0, len(names))
	for _, n := range names {
		cfg := &config.Config{
			UserToken:             f.newToken(),
			DigestHours:           24,
			MaxMessagesPerChannel: 50,
			AutodiscoverLimit:     10,
		}
		for _, m := range mods {
			m(cfg)
		}
		reg = append(reg, slack.Workspace{Name: n, Client: slack.New(cfg, log)})
	}
	return NewHubWithRegistry(reg, reg[0].Client.Config(), log)
}

// deServer registers exactly the tool groups under test — nothing else,
// so coverage credit stays where the assertions are.
func deServer(hub *Hub) *server.MCPServer {
	s := server.NewMCPServer("de-test", "0.0.0")
	hub.registerChannelTools(s)
	hub.registerDigestTools(s)
	hub.registerExportTools(s)
	return s
}

// deCallRaw dispatches a tools/call through the real MCP request path
// and returns the JSON-RPC message, so a test can assert on a protocol
// error (an unregistered tool) as well as on a result.
func deCallRaw(t *testing.T, hub *Hub, name string, args map[string]any) mcp.JSONRPCMessage {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("de: marshal request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return deServer(hub).HandleMessage(ctx, raw)
}

// deCall dispatches a tools/call and returns the tool result.
func deCall(t *testing.T, hub *Hub, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	msg := deCallRaw(t, hub, name, args)
	resp, ok := msg.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("de: %s did not return a result: %+v", name, msg)
	}
	res, ok := resp.Result.(mcp.CallToolResult)
	if !ok {
		t.Fatalf("de: %s returned %T, want mcp.CallToolResult", name, resp.Result)
	}
	return &res
}

// deTS renders a Slack timestamp d before now.
func deTS(d time.Duration) string {
	return strconv.FormatFloat(float64(time.Now().Add(-d).Unix()), 'f', 6, 64)
}

// deMsgJSON builds one conversations.history message object.
func deMsgJSON(user, ts, text string) string {
	return fmt.Sprintf(`{"type":"message","user":%q,"ts":%q,"text":%q}`, user, ts, text)
}

// deHistory wraps message objects in a conversations.history envelope.
func deHistory(msgs ...string) string {
	return `{"ok":true,"has_more":false,"messages":[` + strings.Join(msgs, ",") + `]}`
}

// deChannelJSON builds one conversations.list entry.
func deChannelJSON(id, name string, members int, isMember, isPrivate bool, topic, purpose string) string {
	return fmt.Sprintf(
		`{"id":%q,"name":%q,"num_members":%d,"is_member":%v,"is_private":%v,"is_channel":true,`+
			`"topic":{"value":%q},"purpose":{"value":%q}}`,
		id, name, members, isMember, isPrivate, topic, purpose)
}

// deConversations wraps channel objects in a conversations.list /
// users.conversations envelope.
func deConversations(chans ...string) string {
	return `{"ok":true,"channels":[` + strings.Join(chans, ",") +
		`],"response_metadata":{"next_cursor":""}}`
}

// deAuthTest is the auth.test body the export path needs for permalinks.
func deAuthTest(teamURL string) string {
	return fmt.Sprintf(`{"ok":true,"url":%q,"team":"T","user":"alex","team_id":"T1","user_id":"U1"}`, teamURL)
}

// deUserInfo is a users.info body for one id.
func deUserInfo(id, handle, real string) string {
	return fmt.Sprintf(`{"ok":true,"user":{"id":%q,"name":%q,"real_name":%q}}`, id, handle, real)
}

// TestDeHarness_RoutesAndRecords pins the harness's own contract: a
// routed method answers, an unrouted one comes back as `not_mocked`
// rather than reaching the network, and both are recorded.
func TestDeHarness_RoutesAndRecords_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations(deChannelJSON("C1", "alpha", 3, true, false, "", "")))
	hub := deHub(t, f)

	res := deCall(t, hub, "list_channels", map[string]any{})
	if res.IsError {
		t.Fatalf("routed method should succeed: %q", resultText(res))
	}
	if !f.called("conversations.list") {
		t.Fatalf("expected conversations.list, got %v", f.callList())
	}

	// An unrouted method surfaces as a Slack error, never as a hang or a
	// real request.
	res = deCall(t, hub, "get_channel_info", map[string]any{"channel": "C0AAAAAAAAA"})
	if !res.IsError || !strings.Contains(resultText(res), "not_mocked") {
		t.Fatalf("unrouted method should report not_mocked, got %q", resultText(res))
	}
}
