package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// headlessEnv points the config at a token-shaped value assembled at
// runtime (ADR 082) with every tool enabled unless readOnly.
func headlessEnv(t *testing.T, readOnly bool) {
	t.Helper()
	t.Setenv("SLACK_TOKEN", "")
	t.Setenv("SLACK_USER_TOKEN", "xox"+"p-"+"headless")
	t.Setenv("SLACK_WORKSPACES", "")
	t.Setenv("DISABLED_TOOLS", "")
	t.Setenv("SLACK_LOG_FILE", "")
	t.Setenv("SLACK_READ_ONLY", strconv.FormatBool(readOnly))
}

func registeredTools(t *testing.T) []mcp.Tool {
	t.Helper()
	headlessEnv(t, false)
	srv, _ := buildServer(config.Load(), mnLog())
	list, err := listTools(context.Background(), srv)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// ADR 135: every registered tool is in the README and the badge counts
// them. The list comes from the server itself, not from the source text:
// tools registered through a variable name would slip past a grep.
func TestREADME_ListsEveryTool(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	list := registeredTools(t)
	for _, tool := range list {
		if !bytes.Contains(readme, []byte("`"+tool.Name+"`")) {
			t.Errorf("README does not list tool %s", tool.Name)
		}
	}
	badge := regexp.MustCompile(`tools-(\d+)-purple`).FindSubmatch(readme)
	if badge == nil || string(badge[1]) != strconv.Itoa(len(list)) {
		t.Errorf("README tools badge = %q, want %d", badge, len(list))
	}
}

func runCLI(t *testing.T, readOnly bool, args ...string) (int, string, string) {
	t.Helper()
	headlessEnv(t, readOnly)
	var out, errOut bytes.Buffer
	code := runHeadless(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// ADR 136: the exit code tells a scheduler what went wrong.
func TestHeadless_ExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		readOnly bool
		args     []string
		code     int
		stderr   string
	}{
		{"no tool", false, []string{"run"}, exitUsage, "usage:"},
		{"unknown tool", false, []string{"run", "nope"}, exitUsage, `unknown tool "nope"`},
		{"unknown argument", false, []string{"run", "get_my_actions", "bogus=1"}, exitUsage, "takes: days, due"},
		{"not a number", false, []string{"run", "get_my_actions", "days=abc"}, exitUsage, "not a number"},
		{"required argument", false, []string{"run", "read_canvas"}, exitUsage, "needs channel="},
		{"read-only hides writes", true, []string{"run", "post_message", "channel=x", "text=y"}, exitUsage, "SLACK_READ_ONLY"},
		{"tool error", false, []string{"run", "get_my_actions", "due=someday"}, exitToolError, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, c.readOnly, c.args...)
			if code != c.code || !strings.Contains(stderr, c.stderr) {
				t.Fatalf("exit %d, stderr %q; want %d containing %q", code, stderr, c.code, c.stderr)
			}
		})
	}
}

func TestHeadless_JSONResult(t *testing.T) {
	code, out, _ := runCLI(t, false, "run", "get_my_actions", "--json", "due=someday", "days=3")
	var res headlessResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if code != exitToolError || res.OK || res.Tool != "get_my_actions" || !strings.Contains(res.Text, "due must be") {
		t.Fatalf("got exit %d, %+v", code, res)
	}
	if res.Args["days"] != 3.0 || res.Args["due"] != "someday" {
		t.Fatalf("pairs are typed by the schema and flags may sit between them: %v", res.Args)
	}
}

func TestHeadless_ToolsLists(t *testing.T) {
	code, out, _ := runCLI(t, false, "tools")
	if code != exitOK || !strings.Contains(out, "get_my_actions") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestBuildArgs(t *testing.T) {
	tool := mcp.NewTool("x",
		mcp.WithString("channel", mcp.Required()),
		mcp.WithNumber("hours"),
		mcp.WithBoolean("full_text"),
	)
	got, err := buildArgs(tool, `{"channel":"a","hours":1}`, []string{"hours=2", "full_text=true"}, nil)
	if err != nil || got["channel"] != "a" || got["hours"] != 2.0 || got["full_text"] != true {
		t.Fatalf("pairs override --args and are typed: %v, %v", got, err)
	}
	got, err = buildArgs(tool, "-", nil, strings.NewReader(`{"channel":"from-stdin"}`))
	if err != nil || got["channel"] != "from-stdin" {
		t.Fatalf("--args - reads stdin: %v, %v", got, err)
	}
	if _, err := buildArgs(tool, "", []string{"full_text=maybe", "channel=a"}, nil); err == nil {
		t.Fatal("a non-boolean is refused")
	}
}

func TestTrailingCursor(t *testing.T) {
	text := "## [a]\ncursor: 1.1 (pass as after=)\n...\ncursor: a=1.2;b=3.4 (pass as after= next pull)"
	if got := trailingCursor(text); got != "a=1.2;b=3.4" {
		t.Fatalf("got %q", got)
	}
	if trailingCursor("no cursor here") != "" {
		t.Fatal("no cursor, no field")
	}
}
