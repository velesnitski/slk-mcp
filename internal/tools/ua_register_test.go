package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// uaMCP registers only the unread/mentions/mark_read tools onto a fresh
// MCP server, so a test drives the real handler closures (argument
// parsing included) rather than calling runX directly.
func uaMCP(t *testing.T, h *Hub) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("slk-mcp-test", "0.0.0")
	h.registerUnreadTools(s)
	return s
}

// uaRPC sends one JSON-RPC request and returns the marshalled response.
func uaRPC(t *testing.T, s *server.MCPServer, method string, params map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method, "params": params,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	out, err := json.Marshal(s.HandleMessage(context.Background(), raw))
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	return string(out)
}

// uaCallTool invokes a registered tool and returns the response JSON.
func uaCallTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) string {
	t.Helper()
	return uaRPC(t, s, "tools/call", map[string]any{"name": name, "arguments": args})
}

func TestRegisterUnreadTools_NoUserTokenAnywhereRegistersNothing(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaHub(t, f, func(c *config.Config) {
		c.UserToken = ""
		c.BotToken = "xoxb-test"
	})

	list := uaRPC(t, uaMCP(t, h), "tools/list", map[string]any{})
	for _, name := range []string{"get_unread_summary", "get_mentions", "mark_read"} {
		if strings.Contains(list, `"`+name+`"`) {
			t.Fatalf("%s must not be offered without a user token:\n%s", name, list)
		}
	}
}

func TestRegisterUnreadTools_SecondaryUserTokenIsEnoughToRegister(t *testing.T) {
	f := uaNewFakeSlack(t)
	// primary is bot-only; the secondary workspace carries the user token.
	h := uaMultiHub(t, f)
	h.registry[0].Client = h.registry[1].Client

	list := uaRPC(t, uaMCP(t, h), "tools/list", map[string]any{})
	if !strings.Contains(list, `"get_unread_summary"`) {
		t.Fatalf("a user token on any workspace should expose the tools:\n%s", list)
	}
}

func TestRegisterUnreadTools_RegistersTheWholeTrio(t *testing.T) {
	f := uaNewFakeSlack(t)
	list := uaRPC(t, uaMCP(t, uaHub(t, f)), "tools/list", map[string]any{})
	for _, name := range []string{"get_unread_summary", "get_mentions", "mark_read"} {
		if !strings.Contains(list, `"`+name+`"`) {
			t.Fatalf("%s should be registered:\n%s", name, list)
		}
	}
}

func TestRegisterUnreadTools_ReadOnlyOmitsMarkRead(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaHub(t, f, func(c *config.Config) { c.ReadOnly = true })

	list := uaRPC(t, uaMCP(t, h), "tools/list", map[string]any{})
	if strings.Contains(list, `"mark_read"`) {
		t.Fatalf("read-only mode must not offer a write tool:\n%s", list)
	}
	if !strings.Contains(list, `"get_unread_summary"`) {
		t.Fatalf("the read tools must survive read-only mode:\n%s", list)
	}
}

func TestRegisterUnreadTools_DisabledToolsAreSkippedIndividually(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaHub(t, f, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{
			"get_unread_summary": {},
			"mark_read":          {},
		}
	})

	list := uaRPC(t, uaMCP(t, h), "tools/list", map[string]any{})
	if strings.Contains(list, `"get_unread_summary"`) || strings.Contains(list, `"mark_read"`) {
		t.Fatalf("disabled tools must not register:\n%s", list)
	}
	if !strings.Contains(list, `"get_mentions"`) {
		t.Fatalf("an untouched tool must still register:\n%s", list)
	}
}

func TestGetUnreadSummaryTool_ArgumentsReachTheSweep(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "alpha", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "alpha", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg(uaTS(-60, 100), "U2", "first line", nil),
			uaMsg(uaTS(-70, 100), "U2", "second line", nil),
		},
	})
	s := uaMCP(t, uaHub(t, f))

	out := uaCallTool(t, s, "get_unread_summary", map[string]any{
		"max_per_channel":      1,
		"thread_mention_hours": 0,
		"own_thread_hours":     0,
		"canvas_hours":         0,
		"max_chars":            0,
	})

	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("the sweep should succeed:\n%s", out)
	}
	if !strings.Contains(out, "first line") {
		t.Fatalf("the newest message should be inlined:\n%s", out)
	}
	if !strings.Contains(out, `+1 more messages`) {
		t.Fatalf("max_per_channel=1 must collapse the rest:\n%s", out)
	}
	// The three windows were switched off, so no search / files call ran.
	if f.Called("search.messages") || f.Called("files.list") {
		t.Fatalf("zeroed windows must skip their backstops, calls=%v", f.Calls())
	}
}

func TestGetUnreadSummaryTool_UnknownWorkspaceArgumentIsAnError(t *testing.T) {
	f := uaNewFakeSlack(t)
	s := uaMCP(t, uaHub(t, f))

	out := uaCallTool(t, s, "get_unread_summary", map[string]any{"workspace": "ghost"})
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "unknown workspace") {
		t.Fatalf("an unknown workspace must come back as a tool error:\n%s", out)
	}
}

func TestGetMentionsTool_HoursArgumentShapesTheEmptyMessage(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaNoSearch()
	s := uaMCP(t, uaHub(t, f))

	out := uaCallTool(t, s, "get_mentions", map[string]any{"hours": 4, "dm_history": false})
	if !strings.Contains(out, "no mentions in last 4h") {
		t.Fatalf("the hours argument should reach the empty message:\n%s", out)
	}
	if f.Called("users.conversations") {
		t.Fatalf("dm_history=false must skip the DM backstop, calls=%v", f.Calls())
	}
}

func TestGetMentionsTool_SummaryArgumentSwitchesTheRenderer(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"@alex": {uaHit("C1", "alpha", uaTS(-300, 100), "U2", "<@U1> an ask", "")},
	})
	s := uaMCP(t, uaHub(t, f))

	out := uaCallTool(t, s, "get_mentions", map[string]any{
		"summary": true, "hours": 8, "dm_history": false,
	})
	if !strings.Contains(out, "1 mentions (last 8h) — summary") {
		t.Fatalf("summary mode not reached:\n%s", out)
	}
}

func TestMarkReadTool_MarksTheResolvedChannel(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("conversations.list", `{"ok":true,"channels":[{"id":"C1","name":"alpha"}],"response_metadata":{"next_cursor":""}}`)
	f.On("conversations.mark", `{"ok":true}`)
	s := uaMCP(t, uaHub(t, f))

	out := uaCallTool(t, s, "mark_read", map[string]any{
		"channel": "alpha", "timestamp": "1700000000.000100",
	})
	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("mark_read should succeed:\n%s", out)
	}
	if !strings.Contains(out, "marked #alpha read up to 1700000000.000100") {
		t.Fatalf("confirmation text wrong:\n%s", out)
	}
	if !f.Called("conversations.mark") {
		t.Fatalf("conversations.mark must actually be called, calls=%v", f.Calls())
	}
}

func TestMarkReadTool_PermalinkSuppliesChannelAndMessageTS(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("conversations.mark", `{"ok":true}`)
	s := uaMCP(t, uaHub(t, f))

	// A reply permalink: mark_read must advance to the MESSAGE ts, not the
	// thread root.
	out := uaCallTool(t, s, "mark_read", map[string]any{
		"permalink": "https://example.slack.com/archives/C0ABC1234DE/p1700000000000123?thread_ts=1699999999.000111",
	})
	if !strings.Contains(out, "read up to 1700000000.000123") {
		t.Fatalf("mark_read should use the message ts, not the thread root:\n%s", out)
	}
	if f.Called("conversations.list") {
		t.Fatalf("a channel id from the permalink needs no name lookup, calls=%v", f.Calls())
	}
}

func TestMarkReadTool_MissingTargetAsksForOne(t *testing.T) {
	f := uaNewFakeSlack(t)
	s := uaMCP(t, uaHub(t, f))

	out := uaCallTool(t, s, "mark_read", map[string]any{})
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "channel is required") {
		t.Fatalf("a target-less call must ask for one:\n%s", out)
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("validation must precede any Slack call, got %v", f.Calls())
	}

	out = uaCallTool(t, s, "mark_read", map[string]any{"channel": "alpha"})
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "timestamp is required") {
		t.Fatalf("a channel without a timestamp must be rejected:\n%s", out)
	}
}

func TestMarkReadTool_UpstreamFailuresSurfaceAsErrors(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.OnError("conversations.list", "invalid_auth")
	s := uaMCP(t, uaHub(t, f))

	out := uaCallTool(t, s, "mark_read", map[string]any{
		"channel": "ghost-channel", "timestamp": "1700000000.000100",
	})
	if !strings.Contains(out, `"isError":true`) {
		t.Fatalf("an unresolvable channel must be an error:\n%s", out)
	}

	f2 := uaNewFakeSlack(t)
	f2.OnError("conversations.mark", "not_in_channel")
	s2 := uaMCP(t, uaHub(t, f2))
	out = uaCallTool(t, s2, "mark_read", map[string]any{
		"channel": "C0ABC1234DE", "timestamp": "1700000000.000100",
	})
	if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, "not_in_channel") {
		t.Fatalf("a failed mark must surface the Slack error:\n%s", out)
	}
}
