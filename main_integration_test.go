package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// ADR 118: end to end through the protocol. Unit tests call handlers
// directly; nothing exercised the path a real host takes — JSON-RPC over
// a transport, tool registration as the binary does it, and Slack over
// HTTP. These tests start the server buildServer returns behind the
// streamable-HTTP transport, talk to it with the mcp-go client, and
// serve Slack from a fake.

// itSlack is a minimal Slack Web API fake: one public channel with two
// messages, one user, and a self identity.
type itSlack struct {
	mu    sync.Mutex
	calls []string
	srv   *httptest.Server
}

func newITSlack(t *testing.T) *itSlack {
	t.Helper()
	f := &itSlack{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := strings.TrimPrefix(r.URL.Path, "/")
		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, itSlackBody(method))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *itSlack) called(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == method {
			return true
		}
	}
	return false
}

func itSlackBody(method string) string {
	now := time.Now().Add(-time.Hour).Unix()
	ts := func(off int64) string { return strconv.FormatInt(now+off, 10) + ".000100" }
	switch method {
	case "auth.test":
		return `{"ok":true,"url":"https://example.invalid/","team":"Example","user":"op","team_id":"T0EXAMPLE","user_id":"U0OPERATOR"}`
	case "users.info":
		return `{"ok":true,"user":{"id":"U0AUTHOR01","name":"sam","real_name":"Sam Example"}}`
	case "users.list":
		return `{"ok":true,"members":[{"id":"U0AUTHOR01","name":"sam","real_name":"Sam Example"}]}`
	case "conversations.list", "users.conversations":
		return `{"ok":true,"channels":[{"id":"C0GENERAL1","name":"general","is_channel":true,"is_member":true}]}`
	case "conversations.info":
		return `{"ok":true,"channel":{"id":"C0GENERAL1","name":"general","is_channel":true,"last_read":"0"}}`
	case "conversations.history":
		return `{"ok":true,"has_more":false,"messages":[` +
			`{"type":"message","user":"U0AUTHOR01","ts":"` + ts(60) + `","text":"release is green, shipping now"},` +
			`{"type":"message","user":"U0AUTHOR01","ts":"` + ts(0) + `","text":"starting the release checklist"}]}`
	case "conversations.replies":
		return `{"ok":true,"messages":[]}`
	}
	return `{"ok":false,"error":"not_mocked"}`
}

// itServe builds the real server against the fake and serves it over
// streamable HTTP, returning an initialized protocol client.
func itServe(t *testing.T, slackURL string, readOnly bool) *client.Client {
	t.Helper()
	// Credential-shaped values are assembled at runtime (ADR 082).
	t.Setenv("SLACK_TOKEN", "xox"+"b-"+"integration")
	t.Setenv("SLACK_USER_TOKEN", "xox"+"p-"+"integration")
	t.Setenv("SLACK_WORKSPACES", "")
	t.Setenv("SLACK_PRIORITY_CHANNELS", "")
	if readOnly {
		t.Setenv("SLACK_READ_ONLY", "true")
	} else {
		t.Setenv("SLACK_READ_ONLY", "false")
	}
	cfg := config.Load()
	cfg.APIURL = slackURL + "/"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}

	s, registry := buildServer(cfg, mnLog())
	if len(registry) != 1 {
		t.Fatalf("registry has %d workspaces, want 1", len(registry))
	}
	httpSrv := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(httpSrv.Close)

	c, err := client.NewStreamableHttpClient(httpSrv.URL + "/mcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "integration-test", Version: "0"}
	res, err := c.Initialize(ctx, init)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if res.ServerInfo.Name != "slack v"+version || res.ServerInfo.Version != version {
		t.Fatalf("server identifies as %q %q, want the build version", res.ServerInfo.Name, res.ServerInfo.Version)
	}
	return c
}

func itToolNames(t *testing.T, c *client.Client) map[string]bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	return names
}

func itCall(t *testing.T, c *client.Client, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	var b strings.Builder
	for _, content := range res.Content {
		if tc, ok := content.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestIntegration_ToolsListedOverTheProtocol(t *testing.T) {
	c := itServe(t, newITSlack(t).srv.URL, false)

	names := itToolNames(t, c)
	for _, want := range []string{"get_unread_summary", "get_channel_digest", "read_document", "search_messages", "post_message", "set_status"} {
		if !names[want] {
			t.Errorf("tools/list is missing %q", want)
		}
	}
}

func TestIntegration_ReadOnlyHidesWriteTools(t *testing.T) {
	c := itServe(t, newITSlack(t).srv.URL, true)

	names := itToolNames(t, c)
	for _, write := range []string{"post_message", "delete_message", "add_reaction", "archive_channel", "set_status"} {
		if names[write] {
			t.Errorf("read-only mode still serves the write tool %q", write)
		}
	}
	if !names["get_channel_digest"] {
		t.Error("read-only mode must keep the read tools")
	}
}

func TestIntegration_ChannelDigestReachesSlackAndRendersMessages(t *testing.T) {
	slack := newITSlack(t)
	c := itServe(t, slack.srv.URL, true)

	out, isErr := itCall(t, c, "get_channel_digest", map[string]any{"channel": "#general", "hours": float64(24)})

	if isErr {
		t.Fatalf("digest returned an error: %s", out)
	}
	for _, want := range []string{"#general", "release is green, shipping now", "starting the release checklist", "Sam Example"} {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q:\n%s", want, out)
		}
	}
	if !slack.called("conversations.history") {
		t.Error("the digest never reached Slack's conversations.history")
	}
}

func TestIntegration_BadArgumentsAreAToolErrorNotAProtocolFailure(t *testing.T) {
	c := itServe(t, newITSlack(t).srv.URL, true)

	out, isErr := itCall(t, c, "get_message", map[string]any{"permalink": "not a link"})

	if !isErr {
		t.Fatalf("an unparseable permalink must be reported as a tool error, got: %s", out)
	}
}
