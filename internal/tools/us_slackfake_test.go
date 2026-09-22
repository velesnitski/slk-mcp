package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// usFakeSlack is a programmable stand-in for the Slack Web API. It is
// installed as http.DefaultTransport for the duration of one test, which
// is the seam that reaches slack-go: goslack.New builds its client as
// &http.Client{} with a nil Transport, so every request it makes — Web
// API calls and file downloads alike — goes through whatever
// http.DefaultTransport is at that moment. No socket is opened and no
// packet leaves the process.
//
// Prefer it over a hand-built interface fake when the behaviour under
// test spans layers. A fake satisfying UserClient proves the handler
// called the method; this proves the request slack-go actually builds is
// one Slack would accept, and that the response is parsed back into the
// rendering the caller sees.
//
// Unrouted API methods answer {"ok":false,"error":"not_mocked"} and are
// still recorded, so a handler reaching for an endpoint the test did not
// anticipate surfaces as a readable assertion on Calls() rather than a
// hang or a live request.
//
// Tests using it must not call t.Parallel: the transport is process-wide.
type usFakeSlack struct {
	mu     sync.Mutex
	routes map[string]func(*http.Request) string
	assets map[string]usAsset
	calls  []string
}

// usAsset is a non-API response (a file download body).
type usAsset struct {
	status int
	body   string
}

// usNewFakeSlack installs a fresh fake as the process HTTP transport and
// restores the previous one when the test ends.
func usNewFakeSlack(t *testing.T) *usFakeSlack {
	t.Helper()
	f := &usFakeSlack{
		routes: map[string]func(*http.Request) string{},
		assets: map[string]usAsset{},
	}
	orig := http.DefaultTransport
	http.DefaultTransport = f
	t.Cleanup(func() { http.DefaultTransport = orig })
	return f
}

// RoundTrip answers a Slack Web API call from the routing table, or a
// file download from the asset table.
func (f *usFakeSlack) RoundTrip(req *http.Request) (*http.Response, error) {
	_ = req.ParseForm()

	apiMethod := ""
	if i := strings.Index(req.URL.Path, "/api/"); i >= 0 {
		apiMethod = req.URL.Path[i+len("/api/"):]
	}

	f.mu.Lock()
	if apiMethod != "" {
		f.calls = append(f.calls, apiMethod)
		fn := f.routes[apiMethod]
		f.mu.Unlock()
		if fn == nil {
			return usResponse(req, http.StatusOK, `{"ok":false,"error":"not_mocked"}`), nil
		}
		return usResponse(req, http.StatusOK, fn(req)), nil
	}

	url := req.URL.String()
	f.calls = append(f.calls, url)
	asset, ok := f.assets[url]
	f.mu.Unlock()
	if !ok {
		return usResponse(req, http.StatusNotFound, "no such asset"), nil
	}
	return usResponse(req, asset.status, asset.body), nil
}

func usResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

// On routes a Slack API method (e.g. "users.info") to a fixed JSON body.
func (f *usFakeSlack) On(method, body string) *usFakeSlack {
	return f.OnFunc(method, func(*http.Request) string { return body })
}

// OnFunc routes a method to a handler that may inspect the form the
// caller posted — use it to assert on the arguments a service builds.
func (f *usFakeSlack) OnFunc(method string, fn func(*http.Request) string) *usFakeSlack {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method] = fn
	return f
}

// OnError routes a method to a Slack-shaped error response.
func (f *usFakeSlack) OnError(method, code string) *usFakeSlack {
	return f.On(method, `{"ok":false,"error":"`+code+`"}`)
}

// OnAsset serves body for a download URL (a file's url_private_download).
func (f *usFakeSlack) OnAsset(url, body string) *usFakeSlack {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assets[url] = usAsset{status: http.StatusOK, body: body}
	return f
}

// Calls returns the API methods (and download URLs) requested so far.
func (f *usFakeSlack) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// Called reports whether method was requested at least once.
func (f *usFakeSlack) Called(method string) bool {
	for _, c := range f.Calls() {
		if c == method {
			return true
		}
	}
	return false
}

// usConfig returns a Config with both tokens set, so bot-only and
// user-only branches are both reachable; pass opts to narrow that or to
// set any other field.
func usConfig(opts ...func(*config.Config)) *config.Config {
	cfg := &config.Config{
		BotToken:              "xoxb-test",
		UserToken:             "xoxp-test",
		DigestHours:           24,
		MaxMessagesPerChannel: 50,
		AutodiscoverLimit:     50,
		DisabledTools:         map[string]struct{}{},
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// hub builds a single-workspace Hub whose Slack client talks to f.
func (f *usFakeSlack) hub(t *testing.T, opts ...func(*config.Config)) *Hub {
	t.Helper()
	cfg := usConfig(opts...)
	log := testLog()
	return NewHub(slack.New(cfg, log), cfg, log)
}

// usWS names one workspace of a multi-workspace Hub and the config
// tweaks that distinguish it (e.g. dropping the user token).
type usWS struct {
	Name string
	Opts []func(*config.Config)
}

// multiHub builds a Hub serving every named workspace, all talking to f.
func (f *usFakeSlack) multiHub(t *testing.T, specs ...usWS) *Hub {
	t.Helper()
	log := testLog()
	reg := make([]slack.Workspace, 0, len(specs))
	for _, spec := range specs {
		reg = append(reg, slack.Workspace{
			Name:   spec.Name,
			Client: slack.New(usConfig(spec.Opts...), log),
		})
	}
	return NewHubWithRegistry(reg, usConfig(), log)
}

// usBotOnly strips the user token, turning a workspace into one that
// cannot carry a personal status, DND or scheduled-message read.
func usBotOnly(c *config.Config) { c.UserToken = "" }

// usToolNames runs a register* method against a throwaway MCP server and
// returns the tool names it installed, sorted. This is how the
// registration gates (read-only, disabled-tools, user-token) are
// observed from outside: a tool that is not registered is one the client
// can never call.
func usToolNames(t *testing.T, register func(*server.MCPServer)) []string {
	t.Helper()
	s := server.NewMCPServer("slk-mcp-test", "0.0.0")
	register(s)

	raw := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	msg := s.HandleMessage(context.Background(), raw)
	// mcp-go registers the tools capability lazily on the first AddTool,
	// so a register* method that installed nothing leaves tools/list
	// answering "tools not supported" — which is exactly the observable
	// "this tool is unreachable from a client" we want to assert.
	if errResp, ok := msg.(mcp.JSONRPCError); ok {
		if strings.Contains(errResp.Error.Message, "tools not supported") {
			return nil
		}
		t.Fatalf("tools/list failed: %s", errResp.Error.Message)
	}
	resp, ok := msg.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("tools/list did not return a response: %#v", msg)
	}
	var tools []mcp.Tool
	switch result := resp.Result.(type) {
	case mcp.ListToolsResult:
		tools = result.Tools
	case *mcp.ListToolsResult:
		tools = result.Tools
	default:
		t.Fatalf("tools/list result has unexpected type: %#v", resp.Result)
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

// usCallTool registers tools, then invokes one of them the way an MCP
// client would — through tools/call, so the argument-parsing closure in
// the register* method is exercised too, not just the run* method it
// delegates to.
func usCallTool(t *testing.T, register func(*server.MCPServer), name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	s := server.NewMCPServer("slk-mcp-test", "0.0.0")
	register(s)

	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}

	msg := s.HandleMessage(context.Background(), json.RawMessage(raw))
	resp, ok := msg.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("tools/call %q did not return a response: %#v", name, msg)
	}
	switch result := resp.Result.(type) {
	case mcp.CallToolResult:
		return &result
	case *mcp.CallToolResult:
		return result
	default:
		t.Fatalf("tools/call %q result has unexpected type: %#v", name, resp.Result)
		return nil
	}
}

// usHasTool reports whether name is in the list usToolNames returned.
func usHasTool(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
