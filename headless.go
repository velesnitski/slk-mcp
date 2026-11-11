package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/logger"
)

// Headless mode (ADR 136): `slk-mcp run <tool>` and `slk-mcp tools` call
// the same MCP server in-process, so a scheduler — cron, launchd, a CI
// job, another bot — gets exactly what an MCP client gets: the same
// tools, the same SLACK_READ_ONLY and DISABLED_TOOLS gating, the same
// output. Nothing is wired per tool, so a new tool is reachable here the
// day it is registered.

// Exit codes a scheduler can branch on.
const (
	exitOK        = 0
	exitToolError = 1 // the tool ran and reported an error
	exitUsage     = 2 // unknown tool, bad arguments, bad configuration
)

// usageError is a mistake in the command line, not in Slack.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// isHeadless reports whether the command line asks for headless mode.
func isHeadless(args []string) bool {
	return len(args) > 0 && (args[0] == "run" || args[0] == "tools")
}

// runHeadless runs one headless command and returns the process exit code.
func runHeadless(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(stderr, "slk-mcp:", err)
		return exitUsage
	}
	// Logs go to stderr; a scheduler wants only the result on stdout, so
	// the default level is quieter than the server's.
	log := logger.SetupWithFile(envOr("SLACK_LOG_LEVEL", "warn"), os.Getenv("SLACK_LOG_FILE"))
	srv, _ := buildServer(cfg, log)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var err error
	code := exitOK
	switch args[0] {
	case "tools":
		err = headlessTools(ctx, srv, args[1:], stdout)
	case "run":
		code, err = headlessRun(ctx, srv, args[1:], stdin, stdout)
	}
	var ue usageError
	switch {
	case errors.As(err, &ue):
		fmt.Fprintln(stderr, "slk-mcp:", err)
		return exitUsage
	case err != nil:
		fmt.Fprintln(stderr, "slk-mcp:", err)
		return exitToolError
	}
	return code
}

// headlessTools lists the registered tools: one per line, or their full
// definitions (with input schemas) under --json.
func headlessTools(ctx context.Context, srv *server.MCPServer, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print full tool definitions as JSON")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	list, err := listTools(ctx, srv)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(list)
	}
	for _, t := range list {
		desc, _, _ := strings.Cut(t.Description, ". ")
		fmt.Fprintf(stdout, "%-26s %s\n", t.Name, desc)
	}
	return nil
}

// headlessRun calls one tool. Arguments come as key=value pairs, typed by
// the tool's input schema, and/or as a JSON object via --args (a literal,
// or - for stdin); pairs override the object.
func headlessRun(ctx context.Context, srv *server.MCPServer, args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return 0, usageError{"usage: slk-mcp run <tool> [key=value ...] [--args JSON|-] [--json] [--timeout 5m]"}
	}
	name := args[0]
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "print the result as one JSON object")
	argsJSON := fs.String("args", "", "tool arguments as a JSON object, or - to read it from stdin")
	timeout := fs.Duration("timeout", 5*time.Minute, "give up after this long")
	pairs, err := parseInterleaved(fs, args[1:])
	if err != nil {
		return 0, usageError{err.Error()}
	}

	list, err := listTools(ctx, srv)
	if err != nil {
		return 0, err
	}
	tool, ok := findTool(list, name)
	if !ok {
		return 0, usageError{fmt.Sprintf("unknown tool %q (it may be hidden by SLACK_READ_ONLY or DISABLED_TOOLS); `slk-mcp tools` lists what is available", name)}
	}
	callArgs, err := buildArgs(tool, *argsJSON, pairs, stdin)
	if err != nil {
		return 0, usageError{err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	res, err := callTool(ctx, srv, name, callArgs)
	if err != nil {
		return 0, err
	}
	text := resultText(res)
	if *asJSON {
		out := headlessResult{Tool: name, Args: callArgs, OK: !res.IsError, Text: text, Cursor: trailingCursor(text)}
		for _, c := range res.Content {
			if img, ok := c.(mcp.ImageContent); ok {
				out.Images = append(out.Images, headlessImage{MIMEType: img.MIMEType, Data: img.Data})
			}
		}
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(out); err != nil {
			return 0, err
		}
	} else {
		fmt.Fprintln(stdout, text)
	}
	if res.IsError {
		return exitToolError, nil
	}
	return exitOK, nil
}

// headlessResult is the --json shape: stable field names a scheduler can
// rely on. Cursor is lifted from a trailing "cursor: …" line (the unread
// summary's delta token) so the next run can pass it as after=.
type headlessResult struct {
	Tool   string          `json:"tool"`
	Args   map[string]any  `json:"args"`
	OK     bool            `json:"ok"`
	Text   string          `json:"text"`
	Cursor string          `json:"cursor,omitempty"`
	Images []headlessImage `json:"images,omitempty"`
}

type headlessImage struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"` // base64, as MCP carries it
}

// parseInterleaved accepts flags anywhere among the key=value pairs, the
// way people type them; the standard flag package stops at the first
// non-flag.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var pairs []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pairs = append(pairs, args[0])
			args = args[1:]
		}
	}
	return pairs, nil
}

// buildArgs merges the --args object and the key=value pairs, typing each
// pair by the tool's input schema and refusing keys the tool does not
// take, so a typo fails loudly instead of being silently ignored.
func buildArgs(tool mcp.Tool, argsJSON string, pairs []string, stdin io.Reader) (map[string]any, error) {
	out := map[string]any{}
	if argsJSON != "" {
		raw := []byte(argsJSON)
		if argsJSON == "-" {
			b, err := io.ReadAll(stdin)
			if err != nil {
				return nil, fmt.Errorf("reading --args from stdin: %v", err)
			}
			raw = b
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("--args is not a JSON object: %v", err)
		}
	}
	props := tool.InputSchema.Properties
	for _, p := range pairs {
		key, val, ok := strings.Cut(p, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("argument %q is not key=value", p)
		}
		schema, known := props[key]
		if !known {
			return nil, fmt.Errorf("%s takes no argument %q; it takes: %s", tool.Name, key, strings.Join(sortedKeys(props), ", "))
		}
		v, err := typed(key, val, schema)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	for _, req := range tool.InputSchema.Required {
		if _, ok := out[req]; !ok {
			return nil, fmt.Errorf("%s needs %s=…", tool.Name, req)
		}
	}
	return out, nil
}

// typed converts a command-line string to the type the schema declares.
func typed(key, val string, schema any) (any, error) {
	typ := ""
	if m, ok := schema.(map[string]any); ok {
		typ, _ = m["type"].(string)
	}
	switch typ {
	case "number", "integer":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil, fmt.Errorf("%s=%q is not a number", key, val)
		}
		return f, nil
	case "boolean":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return nil, fmt.Errorf("%s=%q is not true or false", key, val)
		}
		return b, nil
	case "array", "object":
		var v any
		if err := json.Unmarshal([]byte(val), &v); err != nil {
			return nil, fmt.Errorf("%s takes JSON: %v", key, err)
		}
		return v, nil
	}
	return val, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var cursorLine = regexp.MustCompile(`(?m)^cursor: (\S+)`)

// trailingCursor returns the last "cursor: <token>" line's token, or "".
func trailingCursor(text string) string {
	m := cursorLine.FindAllStringSubmatch(text, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

func findTool(list []mcp.Tool, name string) (mcp.Tool, bool) {
	for _, t := range list {
		if t.Name == name {
			return t, true
		}
	}
	return mcp.Tool{}, false
}

// resultText joins the text content of a tool result.
func resultText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// rpc sends one JSON-RPC request to the in-process server and decodes the
// result into out.
func rpc(ctx context.Context, srv *server.MCPServer, method string, params any, out any) error {
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	b, err := json.Marshal(srv.HandleMessage(ctx, req))
	if err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("%s: %s", method, resp.Error.Message)
	}
	return json.Unmarshal(resp.Result, out)
}

func listTools(ctx context.Context, srv *server.MCPServer) ([]mcp.Tool, error) {
	var res mcp.ListToolsResult
	if err := rpc(ctx, srv, "tools/list", map[string]any{}, &res); err != nil {
		return nil, err
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	return res.Tools, nil
}

func callTool(ctx context.Context, srv *server.MCPServer, name string, args map[string]any) (*mcp.CallToolResult, error) {
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	if err != nil {
		return nil, err
	}
	msg := srv.HandleMessage(ctx, req)
	switch m := msg.(type) {
	case mcp.JSONRPCResponse:
		if res, ok := m.Result.(*mcp.CallToolResult); ok {
			return res, nil
		}
		if res, ok := m.Result.(mcp.CallToolResult); ok {
			return &res, nil
		}
		return nil, fmt.Errorf("tools/call: unexpected result %T", m.Result)
	case mcp.JSONRPCError:
		return nil, fmt.Errorf("tools/call: %s", m.Error.Message)
	}
	return nil, fmt.Errorf("tools/call: unexpected response %T", msg)
}
