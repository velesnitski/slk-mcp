package tools

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// deThreeChannels is the roster used by the list_channels tests: two
// joined (one private), one the operator has never joined.
func deThreeChannels() string {
	return deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 30, true, false, "topic for alpha", "purpose for alpha"),
		deChannelJSON("C0BBBBBBBBB", "beta", 120, false, false, "", "purpose only"),
		deChannelJSON("G0CCCCCCCCC", "gamma", 5, true, true, "", ""),
	)
}

func TestListChannels_OrdersAndMarksMembership_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deThreeChannels())
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "list_channels", map[string]any{}))

	lines := strings.Split(out, "\n")
	if lines[0] != "3 channels" {
		t.Fatalf("header should count the rendered channels, got %q", lines[0])
	}
	// Ordered by member count descending, regardless of API order.
	if !strings.HasPrefix(lines[1], "- #beta ") || !strings.HasPrefix(lines[2], "- #alpha ") ||
		!strings.HasPrefix(lines[3], "- #gamma ") {
		t.Fatalf("channels must sort by member count desc, got:\n%s", out)
	}
	// [NOT JOINED] is loud on the anomaly and silent on the common case.
	if !strings.Contains(lines[1], "[NOT JOINED]") {
		t.Fatalf("unjoined channel must be marked: %q", lines[1])
	}
	if strings.Contains(lines[2], "[NOT JOINED]") || strings.Contains(lines[3], "[NOT JOINED]") {
		t.Fatalf("joined channels must stay quiet:\n%s", out)
	}
	if !strings.Contains(lines[3], "🔒") || strings.Contains(lines[2], "🔒") {
		t.Fatalf("the lock must mark exactly the private channel:\n%s", out)
	}
	// Context falls back topic → purpose.
	if !strings.HasSuffix(lines[2], "topic for alpha") {
		t.Fatalf("topic should be the context: %q", lines[2])
	}
	if !strings.HasSuffix(lines[1], "purpose only") {
		t.Fatalf("empty topic must fall back to purpose: %q", lines[1])
	}
	if !strings.HasSuffix(lines[3], "(5)") {
		t.Fatalf("no topic and no purpose must leave the line bare: %q", lines[3])
	}
}

func TestListChannels_LongContextIsTruncated_Behaviour(t *testing.T) {
	f := deNewFake(t)
	long := strings.Repeat("x", 200)
	f.on("conversations.list", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 1, true, false, long, "")))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "list_channels", map[string]any{}))
	if !strings.Contains(out, strings.Repeat("x", 80)+"...") {
		t.Fatalf("an 80-char cap with an ellipsis is what keeps one channel from eating the list:\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("x", 81)) {
		t.Fatalf("context was not truncated:\n%s", out)
	}
}

func TestListChannels_UnjoinedOnlyFilters_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deThreeChannels())
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "list_channels", map[string]any{"unjoined_only": true}))
	if !strings.HasPrefix(out, "1 channels (operator is not a member)") {
		t.Fatalf("filtered header must say so, got:\n%s", out)
	}
	if !strings.Contains(out, "#beta") || strings.Contains(out, "#alpha") || strings.Contains(out, "#gamma") {
		t.Fatalf("only unjoined channels should survive the filter:\n%s", out)
	}
}

func TestListChannels_EmptyWorkspaceStillRenders_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations())
	hub := deHub(t, f)

	res := deCall(t, hub, "list_channels", map[string]any{})
	if res.IsError {
		t.Fatalf("an empty workspace is not an error: %q", resultText(res))
	}
	if got := resultText(res); got != "0 channels" {
		t.Fatalf("empty list must still name itself, got %q", got)
	}
}

func TestListChannels_SingleWorkspaceErrorIsAnError_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.onError("conversations.list", "invalid_auth")
	hub := deHub(t, f)

	res := deCall(t, hub, "list_channels", map[string]any{})
	if !res.IsError {
		t.Fatalf("a failed listing must not look like an empty workspace: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "list channels:") ||
		!strings.Contains(resultText(res), "invalid_auth") {
		t.Fatalf("error should name the operation and the cause, got %q", resultText(res))
	}
}

func TestListChannels_MultiWorkspaceSections_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", "")))
	hub := deMultiHub(t, f, []string{"primary", "secondary"})

	out := resultText(deCall(t, hub, "list_channels", map[string]any{}))
	if !strings.Contains(out, "## [primary]") || !strings.Contains(out, "## [secondary]") {
		t.Fatalf("each workspace needs its own labelled section:\n%s", out)
	}
	if strings.Count(out, "- #alpha") != 2 {
		t.Fatalf("every workspace should be listed once:\n%s", out)
	}
	if f.callCount("conversations.list") != 2 {
		t.Fatalf("one listing per workspace expected, got %v", f.callList())
	}
}

func TestListChannels_MultiWorkspaceErrorStaysInItsSection_Behaviour(t *testing.T) {
	f := deNewFake(t)
	var n atomic.Int64
	f.onFunc("conversations.list", func(*http.Request) string {
		if n.Add(1) == 1 {
			return deConversations(deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", ""))
		}
		return `{"ok":false,"error":"invalid_auth"}`
	})
	hub := deMultiHub(t, f, []string{"primary", "secondary"})

	res := deCall(t, hub, "list_channels", map[string]any{})
	if res.IsError {
		t.Fatalf("one dead workspace must not blank out the healthy one: %q", resultText(res))
	}
	out := resultText(res)
	if !strings.Contains(out, "- #alpha") {
		t.Fatalf("the healthy workspace's channels must survive:\n%s", out)
	}
	if !strings.Contains(out, "## [secondary]\n_error: ") || !strings.Contains(out, "invalid_auth") {
		t.Fatalf("the failure belongs inline under its own label:\n%s", out)
	}
}

func TestListChannels_UnknownWorkspaceNamesTheKnownOnes_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deMultiHub(t, f, []string{"primary", "secondary"})

	res := deCall(t, hub, "list_channels", map[string]any{"workspace": "ghost"})
	if !res.IsError {
		t.Fatal("an unknown label must not silently fall back to the primary")
	}
	if !strings.Contains(resultText(res), "primary") || !strings.Contains(resultText(res), "secondary") {
		t.Fatalf("error should list the configured labels, got %q", resultText(res))
	}
	if len(f.callList()) != 0 {
		t.Fatalf("routing must fail before any API call, got %v", f.callList())
	}
}

// ----------------------------- channel info -----------------------------

func deChannelInfo(id, name string, members int, topic, purpose string, archived bool, created int64) string {
	return fmt.Sprintf(
		`{"ok":true,"channel":{"id":%q,"name":%q,"num_members":%d,"created":%d,`+
			`"is_archived":%v,"topic":{"value":%q},"purpose":{"value":%q}}}`,
		id, name, members, created, archived, topic, purpose)
}

func TestGetChannelInfo_RendersMetadata_Behaviour(t *testing.T) {
	f := deNewFake(t)
	const created = int64(1700000000)
	f.on("conversations.list", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 12, true, false, "", "")))
	f.on("conversations.info", deChannelInfo("C0AAAAAAAAA", "alpha", 12,
		"first topic line\nsecond line", "", false, created))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_info", map[string]any{"channel": "#alpha"}))

	want := "#alpha\nmembers: 12\ncreated: " + time.Unix(created, 0).Format("2006-01-02") +
		"\ntopic: first topic line\npurpose: (none)\narchived: false"
	if out != want {
		t.Fatalf("info body drifted.\n got: %q\nwant: %q", out, want)
	}
}

func TestGetChannelInfo_IDSkipsNameLookup_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.info", deChannelInfo("C0AAAAAAAAA", "alpha", 1, "", "", true, 1700000000))
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_info", map[string]any{"channel": "C0AAAAAAAAA"}))
	if !strings.Contains(out, "archived: true") {
		t.Fatalf("archived flag must be reported: %q", out)
	}
	// Chasing a <#CID> from a digest must not pay for a workspace walk.
	if f.called("conversations.list") {
		t.Fatalf("a canonical id should skip the name→id listing, calls: %v", f.callList())
	}
}

func TestGetChannelInfo_RosterAndOverflow_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.info", deChannelInfo("C0AAAAAAAAA", "alpha", 7, "", "", false, 1700000000))
	f.on("conversations.members", `{"ok":true,"members":["U0AAAAAAAAA","U0BBBBBBBBB"],"response_metadata":{"next_cursor":""}}`)
	f.onFunc("users.info", func(r *http.Request) string {
		id := r.Form.Get("user")
		if id == "U0AAAAAAAAA" {
			return deUserInfo(id, "alex", "Alex")
		}
		return deUserInfo(id, "sam", "Sam")
	})
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "get_channel_info", map[string]any{
		"channel": "C0AAAAAAAAA", "include_members": true, "members_limit": 2,
	}))
	if !strings.Contains(out, "roster:\n- Alex\n- Sam") {
		t.Fatalf("roster should list resolved names:\n%s", out)
	}
	// The gap between num_members and what was listed must be stated, or
	// a partial roster reads as the whole membership.
	if !strings.Contains(out, "(+5 more, raise members_limit to see all)") {
		t.Fatalf("truncated roster must say how much is missing:\n%s", out)
	}
}

func TestGetChannelInfo_MemberErrorDoesNotLoseMetadata_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.info", deChannelInfo("C0AAAAAAAAA", "alpha", 7, "t", "p", false, 1700000000))
	f.onError("conversations.members", "missing_scope")
	hub := deHub(t, f)

	res := deCall(t, hub, "get_channel_info", map[string]any{
		"channel": "C0AAAAAAAAA", "include_members": true,
	})
	if res.IsError {
		t.Fatalf("a roster failure must not discard the metadata we did get: %q", resultText(res))
	}
	out := resultText(res)
	if !strings.Contains(out, "topic: t") || !strings.Contains(out, "members_error: ") ||
		!strings.Contains(out, "missing_scope") {
		t.Fatalf("both the metadata and the roster failure should be reported:\n%s", out)
	}
}

func TestGetChannelInfo_UnknownNameSuggests_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha-releases", 3, true, false, "", "")))
	hub := deHub(t, f)

	res := deCall(t, hub, "get_channel_info", map[string]any{"channel": "alpha"})
	if !res.IsError {
		t.Fatal("an unresolvable channel must be an error, not an empty body")
	}
	if !strings.Contains(resultText(res), "#alpha not found") ||
		!strings.Contains(resultText(res), "#alpha-releases") {
		t.Fatalf("a near miss should be suggested, got %q", resultText(res))
	}
	if f.called("conversations.info") {
		t.Fatalf("info must not be attempted after resolution failed: %v", f.callList())
	}
}

func TestGetChannelInfo_MissingArgument_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deHub(t, f)

	res := deCall(t, hub, "get_channel_info", map[string]any{})
	if !res.IsError || resultText(res) != "channel is required" {
		t.Fatalf("missing channel should be named, got %q", resultText(res))
	}
}

func TestGetChannelInfo_InfoErrorSurfaces_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.onError("conversations.info", "channel_not_found")
	hub := deHub(t, f)

	res := deCall(t, hub, "get_channel_info", map[string]any{"channel": "C0AAAAAAAAA"})
	if !res.IsError || !strings.Contains(resultText(res), "channel_not_found") {
		t.Fatalf("info failure should surface, got %q", resultText(res))
	}
}

func TestGetChannelInfo_UnknownWorkspace_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deMultiHub(t, f, []string{"primary", "secondary"})

	res := deCall(t, hub, "get_channel_info", map[string]any{
		"channel": "C0AAAAAAAAA", "workspace": "ghost",
	})
	if !res.IsError || !strings.Contains(resultText(res), "ghost") {
		t.Fatalf("unknown workspace should be reported, got %q", resultText(res))
	}
}

func TestGetChannelInfo_MultiWorkspaceLabelsTheHit_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.info", deChannelInfo("C0AAAAAAAAA", "alpha", 1, "", "", false, 1700000000))
	hub := deMultiHub(t, f, []string{"primary", "secondary"})

	out := resultText(deCall(t, hub, "get_channel_info", map[string]any{
		"channel": "C0AAAAAAAAA", "workspace": "secondary",
	}))
	if !strings.HasPrefix(out, "#alpha [secondary]") {
		t.Fatalf("with several workspaces the answer must name which one it came from: %q", out)
	}
}

// ----------------------------- archive / unarchive -----------------------------

func TestArchiveChannel_ConfirmsAndNamesTheID_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", "")))
	f.on("conversations.archive", `{"ok":true}`)
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "archive_channel", map[string]any{"channel": "#alpha"}))
	if out != "archived #alpha (C0AAAAAAAAA) — reversible via unarchive_channel" {
		t.Fatalf("confirmation drifted: %q", out)
	}
	if !f.called("conversations.archive") {
		t.Fatalf("expected the archive call, got %v", f.callList())
	}
}

func TestArchiveChannel_FailureIsReported_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations(
		deChannelJSON("C0AAAAAAAAA", "alpha", 3, true, false, "", "")))
	f.onError("conversations.archive", "not_allowed")
	hub := deHub(t, f)

	res := deCall(t, hub, "archive_channel", map[string]any{"channel": "alpha"})
	if !res.IsError || !strings.Contains(resultText(res), "not_allowed") {
		t.Fatalf("a refused archive must not read as success, got %q", resultText(res))
	}
}

func TestArchiveChannel_ResolveFailureStopsBeforeWriting_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.list", deConversations())
	hub := deHub(t, f)

	res := deCall(t, hub, "archive_channel", map[string]any{"channel": "ghost"})
	if !res.IsError {
		t.Fatal("an unresolvable channel must not be archived")
	}
	if f.called("conversations.archive") {
		t.Fatalf("nothing should be archived after a failed lookup: %v", f.callList())
	}
}

func TestArchiveChannel_MissingArgument_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deHub(t, f)
	for _, tool := range []string{"archive_channel", "unarchive_channel"} {
		res := deCall(t, hub, tool, map[string]any{})
		if !res.IsError || resultText(res) != "channel is required" {
			t.Fatalf("%s: missing channel should be named, got %q", tool, resultText(res))
		}
	}
}

func TestUnarchiveChannel_Behaviour(t *testing.T) {
	f := deNewFake(t)
	f.on("conversations.unarchive", `{"ok":true}`)
	hub := deHub(t, f)

	out := resultText(deCall(t, hub, "unarchive_channel", map[string]any{"channel": "C0AAAAAAAAA"}))
	if out != "unarchived #C0AAAAAAAAA (C0AAAAAAAAA)" {
		t.Fatalf("confirmation drifted: %q", out)
	}

	f2 := deNewFake(t)
	f2.onError("conversations.unarchive", "not_archived")
	res := deCall(t, deHub(t, f2), "unarchive_channel", map[string]any{"channel": "C0AAAAAAAAA"})
	if !res.IsError || !strings.Contains(resultText(res), "not_archived") {
		t.Fatalf("a refused unarchive must surface, got %q", resultText(res))
	}
}

func TestArchiveTools_HiddenWhenReadOnly_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deHub(t, f, func(c *config.Config) { c.ReadOnly = true })

	for _, tool := range []string{"archive_channel", "unarchive_channel"} {
		msg := deCallRaw(t, hub, tool, map[string]any{"channel": "C0AAAAAAAAA"})
		if _, ok := msg.(mcp.JSONRPCError); !ok {
			t.Fatalf("%s must not be registered in read-only mode, got %+v", tool, msg)
		}
	}
	// Reads stay available.
	if _, ok := deCallRaw(t, hub, "list_channels", nil).(mcp.JSONRPCResponse); !ok {
		t.Fatal("read-only must not withdraw the read tools")
	}
}

func TestChannelTools_DisabledByConfig_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deHub(t, f, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{
			"list_channels": {}, "get_channel_info": {},
			"archive_channel": {}, "unarchive_channel": {},
		}
	})
	for _, tool := range []string{"list_channels", "get_channel_info", "archive_channel", "unarchive_channel"} {
		if _, ok := deCallRaw(t, hub, tool, map[string]any{"channel": "C0AAAAAAAAA"}).(mcp.JSONRPCError); !ok {
			t.Fatalf("%s should not be registered when disabled", tool)
		}
	}
}

func TestArchiveTools_RoutingAndResolutionFailures_Behaviour(t *testing.T) {
	f := deNewFake(t)
	hub := deMultiHub(t, f, []string{"primary", "secondary"})

	// An unknown label must not silently archive in the primary.
	for _, tool := range []string{"archive_channel", "unarchive_channel"} {
		res := deCall(t, hub, tool, map[string]any{"channel": "alpha", "workspace": "ghost"})
		if !res.IsError || !strings.Contains(resultText(res), "ghost") {
			t.Fatalf("%s: unknown workspace should be reported, got %q", tool, resultText(res))
		}
	}
	if len(f.callList()) != 0 {
		t.Fatalf("routing must fail before any API call, got %v", f.callList())
	}

	// A name that resolves to nothing must stop before the write.
	f2 := deNewFake(t)
	f2.on("conversations.list", deConversations())
	res := deCall(t, deHub(t, f2), "unarchive_channel", map[string]any{"channel": "ghost"})
	if !res.IsError || !strings.Contains(resultText(res), "#ghost not found") {
		t.Fatalf("unresolvable channel: got %q", resultText(res))
	}
	if f2.called("conversations.unarchive") {
		t.Fatalf("nothing should be unarchived after a failed lookup: %v", f2.callList())
	}
}

// ----------------------------- firstLine -----------------------------

func TestFirstLine_Behaviour(t *testing.T) {
	cases := map[string]string{
		"":              "(none)",
		"   ":           "(none)",
		"one line":      "one line",
		"first\nsecond": "first",
		// TrimSpace runs before the newline cut, so trailing blanks on the
		// first line survive — pinned so the shape is not changed by accident.
		"  padded  \nnext ": "padded  ",
	}
	for in, want := range cases {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}
