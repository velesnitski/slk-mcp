package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Helpers shared by the tm_* handler tests. Every name here is prefixed
// `tm` so it cannot collide with helpers other test files add to this
// package.
//
// The tools under test register their handlers as anonymous closures on
// an *server.MCPServer, so the only way to reach them the way a client
// does is through the server's own JSON-RPC entry point. tmInvoke does
// exactly that: it builds a `tools/call` request, hands it to
// HandleMessage, and unwraps the CallToolResult — which also means a
// tool that was never registered (read-only, disabled) shows up as a
// JSON-RPC error rather than a nil-pointer panic.

// tmServer returns an MCP server carrying only what `register` wires.
func tmServer(t *testing.T, register func(*server.MCPServer)) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("slk-mcp-test", "0.0.0")
	register(s)
	return s
}

// tmInvoke calls a tool and reports whether it was registered at all.
func tmInvoke(t *testing.T, s *server.MCPServer, name string, args map[string]any) (*mcp.CallToolResult, bool) {
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
		t.Fatalf("marshal tools/call: %v", err)
	}
	switch v := s.HandleMessage(context.Background(), raw).(type) {
	case mcp.JSONRPCResponse:
		res, ok := v.Result.(mcp.CallToolResult)
		if !ok {
			t.Fatalf("tools/call returned %T, want mcp.CallToolResult", v.Result)
		}
		return &res, true
	case mcp.JSONRPCError:
		return nil, false
	default:
		t.Fatalf("tools/call returned unexpected %T", v)
		return nil, false
	}
}

// tmCall is tmInvoke for tools that must be registered.
func tmCall(t *testing.T, s *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, ok := tmInvoke(t, s, name, args)
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	return res
}

// tmToolNames lists the tools a server exposes.
func tmToolNames(t *testing.T, s *server.MCPServer) []string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	switch v := s.HandleMessage(context.Background(), raw).(type) {
	case mcp.JSONRPCResponse:
		res, ok := v.Result.(mcp.ListToolsResult)
		if !ok {
			t.Fatalf("tools/list returned %T, want mcp.ListToolsResult", v.Result)
		}
		names := make([]string, 0, len(res.Tools))
		for _, tool := range res.Tools {
			names = append(names, tool.Name)
		}
		return names
	case mcp.JSONRPCError:
		return nil
	default:
		t.Fatalf("tools/list returned unexpected %T", v)
		return nil
	}
}

// tmHas reports whether a slice carries want.
func tmHas(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// tmCountCalls counts how many times an API method was requested.
func tmCountCalls(f *fakeSlack, method string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == method {
			n++
		}
	}
	return n
}

// tmWant fails the test unless every fragment appears in got.
func tmWant(t *testing.T, got string, fragments ...string) {
	t.Helper()
	for _, frag := range fragments {
		if !strings.Contains(got, frag) {
			t.Fatalf("output missing %q:\n%s", frag, got)
		}
	}
}

// tmNotWant fails the test if any fragment appears in got.
func tmNotWant(t *testing.T, got string, fragments ...string) {
	t.Helper()
	for _, frag := range fragments {
		if strings.Contains(got, frag) {
			t.Fatalf("output should not contain %q:\n%s", frag, got)
		}
	}
}

// tmChannelList routes conversations.list to a single-page listing built
// from a name→id map, so a handler's ResolveID("alpha") lands on "C1".
func tmChannelList(f *fakeSlack, byName map[string]string) *fakeSlack {
	entries := make([]string, 0, len(byName))
	for name, id := range byName {
		entries = append(entries, `{"id":"`+id+`","name":"`+name+`","is_member":true}`)
	}
	return f.On("conversations.list",
		`{"ok":true,"channels":[`+strings.Join(entries, ",")+`],"response_metadata":{"next_cursor":""}}`)
}

// tmUsers routes users.info to a per-id roster, so rendered lines carry
// display names instead of raw ids. Unknown ids answer user_not_found,
// which the service degrades to the raw id.
func tmUsers(f *fakeSlack, byID map[string]string) *fakeSlack {
	return f.OnFunc("users.info", func(r *http.Request) string {
		id := r.Form.Get("user")
		name, ok := byID[id]
		if !ok {
			return `{"ok":false,"error":"user_not_found"}`
		}
		return `{"ok":true,"user":{"id":"` + id + `","name":"` + name + `","real_name":"` + name + `"}}`
	})
}

// tmAuthTest routes auth.test to a self identity on the given host.
func tmAuthTest(f *fakeSlack, userID, teamURL string) *fakeSlack {
	return f.On("auth.test",
		`{"ok":true,"user_id":"`+userID+`","user":"alex","team":"T1","team_id":"T1","url":"`+teamURL+`"}`)
}

// tmSearchBody wraps raw match objects in the search.messages envelope.
func tmSearchBody(matches ...string) string {
	return `{"ok":true,"messages":{"total":` +
		strconv.Itoa(len(matches)) + `,"matches":[` + strings.Join(matches, ",") + `]}}`
}

// tmHistoryBody wraps raw message objects in the conversations.history
// envelope (also the shape conversations.replies answers with).
func tmHistoryBody(msgs ...string) string {
	return `{"ok":true,"has_more":false,"messages":[` + strings.Join(msgs, ",") + `]}`
}
