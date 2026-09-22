package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// dcListTools drives the MCP server's own tools/list endpoint and
// returns the registered tool names. Going through the protocol rather
// than reading the server's internals keeps the assertion on what a
// client would actually be offered.
func dcListTools(t *testing.T, s *server.MCPServer) []string {
	t.Helper()
	raw := s.HandleMessage(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal tools/list response: %v", err)
	}
	var env struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("decode tools/list response: %v (%s)", err, b)
	}
	if env.Error != nil {
		t.Fatalf("tools/list failed: %s", env.Error.Message)
	}
	names := make([]string, 0, len(env.Result.Tools))
	for _, tool := range env.Result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// dcContent is one content block of a tools/call result, flattened so a
// test can assert on what a client would actually receive.
type dcContent struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Data     string `json:"data"`
	MIMEType string `json:"mimeType"`
}

// dcToolResult is a decoded tools/call result.
type dcToolResult struct {
	IsError bool        `json:"isError"`
	Content []dcContent `json:"content"`
}

// Text joins every text block, which is how the rendered answer reads.
func (r dcToolResult) Text() string {
	var parts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// dcCallTool registers h's whole catalogue on a fresh MCP server and
// invokes one tool through the protocol, so the test exercises the
// registration closure (argument parsing, workspace scoping, error
// mapping) and not just the inner run* function.
func dcCallTool(t *testing.T, h *Hub, name string, args map[string]any) dcToolResult {
	t.Helper()
	s := server.NewMCPServer("t", "0")
	h.RegisterAll(s)

	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}
	b, err := json.Marshal(s.HandleMessage(context.Background(), req))
	if err != nil {
		t.Fatalf("marshal tools/call response: %v", err)
	}
	var env struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result dcToolResult `json:"result"`
	}
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("decode tools/call response: %v (%s)", err, b)
	}
	if env.Error != nil {
		t.Fatalf("tools/call %s failed at the protocol level: %s", name, env.Error.Message)
	}
	return env.Result
}

// dcHas reports whether name is in names.
func dcHas(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// dcRegisterAll builds a Hub from opts, registers everything, and
// returns the tool names on offer.
func dcRegisterAll(t *testing.T, opts ...func(*config.Config)) []string {
	t.Helper()
	f := newFakeSlack(t)
	h := newFakeHub(t, f, opts...)
	s := server.NewMCPServer("t", "0")
	h.RegisterAll(s)
	names := dcListTools(t, s)
	if f.Called("auth.test") {
		t.Fatalf("registration must not call Slack; calls=%v", f.Calls())
	}
	return names
}

func TestRegisterAll_OffersTheWholeCatalogue(t *testing.T) {
	names := dcRegisterAll(t)

	// A sample spanning every register* method RegisterAll fans out to —
	// one silently dropped category is the failure this pins.
	for _, want := range []string{
		"download_audio", "view_image", "read_canvas", "list_channels",
		"get_channel_digest", "read_document", "export_conversations",
		"get_list_items", "post_message", "search_messages", "set_status",
		"set_dnd", "list_scheduled_messages", "get_thread",
		"analyze_audio_tone", "transcribe_audio", "get_unread_summary",
		"list_users",
	} {
		if !dcHas(names, want) {
			t.Errorf("tool %q not registered; got %v", want, names)
		}
	}
	if len(names) < 25 {
		t.Errorf("expected the full catalogue, got %d tools: %v", len(names), names)
	}
}

func TestRegisterAll_BotOnlyHidesUserTokenTools(t *testing.T) {
	names := dcRegisterAll(t, func(c *config.Config) { c.UserToken = "" })

	// get_list_items is gated on RequiresUserToken: Slack denies
	// lists:read to bot tokens, so offering it would promise a read the
	// server cannot perform.
	if dcHas(names, "get_list_items") {
		t.Errorf("get_list_items must not be offered without a user token; got %v", names)
	}
	// The rest of the catalogue is unaffected.
	if !dcHas(names, "read_canvas") || !dcHas(names, "search_messages") {
		t.Errorf("bot-only mode dropped too much: %v", names)
	}
}

func TestRegisterAll_DisabledToolsAreNotOffered(t *testing.T) {
	names := dcRegisterAll(t, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{
			"read_canvas":    {},
			"read_document":  {},
			"view_image":     {},
			"get_list_items": {},
		}
	})

	for _, gone := range []string{"read_canvas", "read_document", "view_image", "get_list_items"} {
		if dcHas(names, gone) {
			t.Errorf("disabled tool %q is still offered; got %v", gone, names)
		}
	}
	if !dcHas(names, "search_messages") {
		t.Errorf("disabling four tools must not disable the rest: %v", names)
	}
}

func TestRegister_ReadOnlySkipsWriteTools(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f, func(c *config.Config) { c.ReadOnly = true })
	s := server.NewMCPServer("t", "0")

	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	h.register(s,
		toolDef{Name: "dc_write_tool", Description: "mutates", Writes: true, Handle: handler},
		toolDef{Name: "dc_read_tool", Description: "reads", Handle: handler},
	)

	names := dcListTools(t, s)
	if dcHas(names, "dc_write_tool") {
		t.Errorf("a writing tool must be skipped in read-only mode; got %v", names)
	}
	if !dcHas(names, "dc_read_tool") {
		t.Errorf("a reading tool must survive read-only mode; got %v", names)
	}
}

func TestRegister_DescriptionlessToolStillRegisters(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)
	s := server.NewMCPServer("t", "0")

	h.register(s, toolDef{
		Name: "dc_bare_tool",
		Handle: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("bare"), nil
		},
	})

	if names := dcListTools(t, s); !dcHas(names, "dc_bare_tool") {
		t.Errorf("a toolDef without a Description must still register; got %v", names)
	}
}

func TestWrap_PassesThroughToTheHandler(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	called := false
	wrapped := h.wrap("dc_named", func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("from " + req.Params.Name), nil
	})

	res, err := wrapped(context.Background(), callToolRequest("dc_named", nil))
	if err != nil {
		t.Fatalf("wrapped handler: %v", err)
	}
	if !called {
		t.Fatal("wrap did not invoke the wrapped handler")
	}
	if got := resultText(res); got != "from dc_named" {
		t.Errorf("wrap altered the result: %q", got)
	}
}

func TestHubAccessors_ExposeTheLiveClient(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	if h.Client() == nil {
		t.Error("Client() is nil")
	}
	if h.Config() == nil || h.Config().BotToken == "" {
		t.Error("Config() does not carry the configured tokens")
	}
	if h.Log() == nil {
		t.Error("Log() is nil")
	}

	// Every service seam must be wired; a nil one panics at the first
	// handler call rather than here, which is a much worse place to find it.
	if h.Users() == nil || h.Channels() == nil || h.Messages() == nil ||
		h.Search() == nil || h.Unread() == nil || h.Lists() == nil ||
		h.Status() == nil || h.DND() == nil || h.Scheduled() == nil ||
		h.Canvas() == nil {
		t.Error("a service accessor returned nil")
	}
}

func TestWorkspaceHelpers_SingleWorkspaceDefaults(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	if got := h.workspaceNames(); len(got) != 1 || got[0] != "primary" {
		t.Errorf("workspaceNames() = %v, want [primary]", got)
	}
	if got := h.wsLabel("primary"); got != "" {
		t.Errorf("a single workspace must stay unlabelled, got %q", got)
	}
	if got := h.workspaceTargets(""); len(got) != 1 {
		t.Errorf("empty target must fan out to every workspace, got %d", len(got))
	}
	if got := h.workspaceTargets("  primary  "); len(got) != 1 {
		t.Errorf("a padded label must still match, got %d", len(got))
	}
	if got := h.workspaceTargets("nope"); got != nil {
		t.Errorf("an unknown label must resolve to nothing, got %v", got)
	}

	scoped, name, errRes := h.scopedWorkspace("")
	if errRes != nil {
		t.Fatalf("scoping to the default workspace failed: %s", resultText(errRes))
	}
	if name != "primary" || scoped == nil {
		t.Errorf("scopedWorkspace(\"\") = %q/%v", name, scoped)
	}

	_, _, errRes = h.scopedWorkspace("beta")
	if errRes == nil {
		t.Fatal("an unknown workspace label must be refused")
	}
	if txt := resultText(errRes); txt == "" {
		t.Error("the refusal must explain itself")
	}
}
