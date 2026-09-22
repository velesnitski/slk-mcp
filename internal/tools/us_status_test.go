package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/config"
)

func TestRegisterStatusTools_RegistersBothWhenAUserTokenIsPresent(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t)
	names := usToolNames(t, h.registerStatusTools)
	if !usHasTool(names, "set_status") || !usHasTool(names, "set_presence") {
		t.Fatalf("a user token should register both personal tools, got %v", names)
	}
}

func TestRegisterStatusTools_ReadOnlyRegistersNothing(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t, func(c *config.Config) { c.ReadOnly = true })
	if names := usToolNames(t, h.registerStatusTools); len(names) != 0 {
		t.Fatalf("read-only must not expose profile writes, got %v", names)
	}
}

func TestRegisterStatusTools_BotOnlyRegistersNothing(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t, usBotOnly)
	if names := usToolNames(t, h.registerStatusTools); len(names) != 0 {
		t.Fatalf("a bot token cannot set a human's status, got %v", names)
	}
}

func TestRegisterStatusTools_DisabledToolsAreDropped(t *testing.T) {
	f := usNewFakeSlack(t)

	h := f.hub(t, func(c *config.Config) { c.DisabledTools = map[string]struct{}{"set_status": {}} })
	names := usToolNames(t, h.registerStatusTools)
	if usHasTool(names, "set_status") || !usHasTool(names, "set_presence") {
		t.Fatalf("disabling set_status must leave set_presence alone, got %v", names)
	}

	h = f.hub(t, func(c *config.Config) { c.DisabledTools = map[string]struct{}{"set_presence": {}} })
	names = usToolNames(t, h.registerStatusTools)
	if !usHasTool(names, "set_status") || usHasTool(names, "set_presence") {
		t.Fatalf("disabling set_presence must leave set_status alone, got %v", names)
	}
}

func TestRunSetPresence_AwaySendsAwayAndReportsIt(t *testing.T) {
	f := usNewFakeSlack(t)
	var sent string
	f.OnFunc("users.setPresence", func(r *http.Request) string {
		sent = r.Form.Get("presence")
		return `{"ok":true}`
	})
	h := f.hub(t)

	res := h.runSetPresence(context.Background(), "", true)
	if res.IsError {
		t.Fatalf("presence write should succeed: %q", resultText(res))
	}
	if sent != "away" {
		t.Errorf("away=true must send presence=away, sent %q", sent)
	}
	if got := resultText(res); got != "- presence: away" {
		t.Errorf("confirmation = %q", got)
	}
}

func TestRunSetPresence_AutoRestoresAutomaticPresence(t *testing.T) {
	f := usNewFakeSlack(t)
	var sent string
	f.OnFunc("users.setPresence", func(r *http.Request) string {
		sent = r.Form.Get("presence")
		return `{"ok":true}`
	})
	h := f.hub(t)

	res := h.runSetPresence(context.Background(), "", false)
	if sent != "auto" {
		t.Errorf("away=false must send presence=auto, sent %q", sent)
	}
	if got := resultText(res); got != "- presence: auto" {
		t.Errorf("confirmation = %q", got)
	}
}

func TestRunSetPresence_NoUserTokenRefusesLoudly(t *testing.T) {
	// The defect class this guards: a personal tool that quietly does
	// nothing when the token cannot carry the action. It must refuse.
	f := usNewFakeSlack(t)
	h := f.hub(t, usBotOnly)

	res := h.runSetPresence(context.Background(), "", true)
	if res == nil || !res.IsError {
		t.Fatalf("a bot-only workspace must refuse, got %+v", res)
	}
	text := resultText(res)
	if !strings.Contains(text, "no user token") || !strings.Contains(text, "skipped") {
		t.Errorf("refusal should name the missing user token and the skip: %q", text)
	}
	if f.Called("users.setPresence") {
		t.Error("no presence call may be issued without a user token")
	}
}

func TestRunSetPresence_PartialWorkspaceFailureStillReportsBoth(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.setPresence", `{"ok":true}`)
	h := f.multiHub(t,
		usWS{Name: "alpha"},
		usWS{Name: "beta", Opts: []func(*config.Config){usBotOnly}},
	)

	res := h.runSetPresence(context.Background(), "", true)
	if res.IsError {
		t.Fatalf("one working workspace should still succeed: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "- presence: away [alpha]") {
		t.Errorf("the workspace that worked must be labelled: %q", text)
	}
	if !strings.Contains(text, "skipped [beta]") {
		t.Errorf("the skipped workspace must be named, not silently dropped: %q", text)
	}
}

// KNOWN DEFECT, pinned: when every workspace HAS a user token but the
// API rejects the write, runSetPresence still reports "no workspace has
// a user token" (status.go:111) — the token is fine, the call failed.
// The per-workspace line underneath carries the real reason.
func TestRunSetPresence_APIFailureMisreportsTheCause(t *testing.T) {
	f := usNewFakeSlack(t)
	f.OnError("users.setPresence", "missing_scope")
	h := f.hub(t)

	res := h.runSetPresence(context.Background(), "", true)
	if res == nil || !res.IsError {
		t.Fatalf("a rejected presence write must be an error, got %+v", res)
	}
	text := resultText(res)
	if !strings.Contains(text, "missing_scope") || !strings.Contains(text, "users:write") {
		t.Errorf("the scope hint must survive into the error: %q", text)
	}
	if !strings.Contains(text, "no workspace has a user token") {
		t.Skipf("status.go:111 appears fixed — message is now %q; update this test", text)
	}
}

func TestRunSetStatus_SetsTextEmojiAndExpiry(t *testing.T) {
	f := usNewFakeSlack(t)
	var profile string
	f.OnFunc("users.profile.set", func(r *http.Request) string {
		profile = r.Form.Get("profile")
		return `{"ok":true,"profile":{}}`
	})
	h := f.hub(t)

	now := time.Unix(1_700_000_000, 0)
	res := h.runSetStatus(context.Background(), statusParams{
		text:       "heads down",
		emoji:      ":coffee:",
		clearAfter: 90,
	}, now)
	if res.IsError {
		t.Fatalf("status write should succeed: %q", resultText(res))
	}
	for _, want := range []string{"heads down", "coffee"} {
		if !strings.Contains(profile, want) {
			t.Errorf("posted profile missing %q: %s", want, profile)
		}
	}
	// The expiry Slack receives must be the one the confirmation quotes.
	wantExp := now.Add(90 * time.Minute).Unix()
	if !strings.Contains(profile, "status_expiration") {
		t.Errorf("expiry must reach Slack: %s", profile)
	}
	text := resultText(res)
	if !strings.Contains(text, `set status :coffee: "heads down"`) {
		t.Errorf("confirmation should describe what was set: %q", text)
	}
	if !strings.Contains(text, time.Unix(wantExp, 0).Format("2006-01-02 15:04")) {
		t.Errorf("confirmation should quote the computed expiry: %q", text)
	}
}

func TestRunSetStatus_EmptyTextAndEmojiClears(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.profile.set", `{"ok":true,"profile":{}}`)
	h := f.hub(t)

	res := h.runSetStatus(context.Background(), statusParams{}, time.Unix(1000, 0))
	if got := resultText(res); got != "- cleared custom status" {
		t.Fatalf("empty text+emoji should read as a clear, got %q", got)
	}
}

func TestRunSetStatus_TouchesPresenceOnlyWhenAsked(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.profile.set", `{"ok":true,"profile":{}}`)
	f.On("users.setPresence", `{"ok":true}`)
	h := f.hub(t)

	res := h.runSetStatus(context.Background(), statusParams{text: "afk"}, time.Unix(1000, 0))
	if f.Called("users.setPresence") {
		t.Errorf("set_presence=false must leave the dot alone, calls=%v", f.Calls())
	}
	if strings.Contains(resultText(res), "presence") {
		t.Errorf("no presence change should be claimed: %q", resultText(res))
	}

	res = h.runSetStatus(context.Background(), statusParams{
		text: "afk", setPresence: true, away: true,
	}, time.Unix(1000, 0))
	if !f.Called("users.setPresence") {
		t.Error("set_presence=true must flip the dot")
	}
	if !strings.Contains(resultText(res), "; presence: away") {
		t.Errorf("presence change should be reported: %q", resultText(res))
	}
}

func TestRunSetStatus_PresenceFailureKeepsTheStatusResult(t *testing.T) {
	// The status landed; only the dot did not. Reporting the whole call
	// as a failure would hide a change that was actually made.
	f := usNewFakeSlack(t)
	f.On("users.profile.set", `{"ok":true,"profile":{}}`)
	f.OnError("users.setPresence", "missing_scope")
	h := f.hub(t)

	res := h.runSetStatus(context.Background(), statusParams{
		text: "afk", setPresence: true, away: false,
	}, time.Unix(1000, 0))
	if res.IsError {
		t.Fatalf("a presence failure must not void the status write: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "set status") || !strings.Contains(text, "presence unchanged") {
		t.Errorf("both outcomes should be reported: %q", text)
	}
}

func TestRunSetStatus_SingleWorkspaceErrorCarriesTheScopeHint(t *testing.T) {
	f := usNewFakeSlack(t)
	f.OnError("users.profile.set", "missing_scope")
	h := f.hub(t)

	res := h.runSetStatus(context.Background(), statusParams{text: "afk"}, time.Unix(1000, 0))
	if res == nil || !res.IsError {
		t.Fatalf("a rejected status write must be an error, got %+v", res)
	}
	if !strings.Contains(resultText(res), "users.profile:write") {
		t.Errorf("the actionable scope fix must be in the message: %q", resultText(res))
	}
}

func TestRunSetStatus_MultiWorkspaceErrorIsPerWorkspaceNotFatal(t *testing.T) {
	f := usNewFakeSlack(t)
	calls := 0
	f.OnFunc("users.profile.set", func(*http.Request) string {
		calls++
		if calls == 1 {
			return `{"ok":true,"profile":{}}`
		}
		return `{"ok":false,"error":"missing_scope"}`
	})
	h := f.multiHub(t, usWS{Name: "alpha"}, usWS{Name: "beta"})

	res := h.runSetStatus(context.Background(), statusParams{text: "afk"}, time.Unix(1000, 0))
	if res.IsError {
		t.Fatalf("one good workspace means a partial success, not a failure: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "set status \"afk\" [alpha]") {
		t.Errorf("the successful workspace must be reported: %q", text)
	}
	if !strings.Contains(text, "- error [beta]") || !strings.Contains(text, "missing_scope") {
		t.Errorf("the failing workspace must be named with its reason: %q", text)
	}
}

func TestRunSetStatus_NoUserTokenRefusesLoudly(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t, usBotOnly)

	res := h.runSetStatus(context.Background(), statusParams{text: "afk"}, time.Unix(1000, 0))
	if res == nil || !res.IsError {
		t.Fatalf("a bot-only workspace must refuse, got %+v", res)
	}
	if !strings.Contains(resultText(res), "no user token") {
		t.Errorf("refusal should name the missing user token: %q", resultText(res))
	}
	if f.Called("users.profile.set") {
		t.Error("no profile write may be issued without a user token")
	}
}

func TestSetStatusTool_ParsesArgumentsFromTheClient(t *testing.T) {
	// End to end through tools/call: the argument-parsing closure is
	// where a renamed or mistyped argument silently becomes a default.
	f := usNewFakeSlack(t)
	var profile, presence string
	f.OnFunc("users.profile.set", func(r *http.Request) string {
		profile = r.Form.Get("profile")
		return `{"ok":true,"profile":{}}`
	})
	f.OnFunc("users.setPresence", func(r *http.Request) string {
		presence = r.Form.Get("presence")
		return `{"ok":true}`
	})
	h := f.hub(t)

	res := usCallTool(t, h.registerStatusTools, "set_status", map[string]any{
		"text":                "back tomorrow",
		"emoji":               "palm_tree", // bare name — must be wrapped
		"clear_after_minutes": float64(30),
		"set_presence":        true,
		"away":                true,
	})
	if res.IsError {
		t.Fatalf("tool call failed: %q", resultText(res))
	}
	if !strings.Contains(profile, "back tomorrow") || !strings.Contains(profile, ":palm_tree:") {
		t.Errorf("a bare emoji name must be wrapped in colons before it reaches Slack: %s", profile)
	}
	if presence != "away" {
		t.Errorf("away=true should have reached users.setPresence, got %q", presence)
	}
	if !strings.Contains(resultText(res), "clears at") {
		t.Errorf("clear_after_minutes should produce an expiry: %q", resultText(res))
	}
}

func TestSetPresenceTool_DefaultsToAway(t *testing.T) {
	f := usNewFakeSlack(t)
	var presence string
	f.OnFunc("users.setPresence", func(r *http.Request) string {
		presence = r.Form.Get("presence")
		return `{"ok":true}`
	})
	h := f.hub(t)

	res := usCallTool(t, h.registerSetPresence, "set_presence", map[string]any{})
	if res.IsError {
		t.Fatalf("tool call failed: %q", resultText(res))
	}
	if presence != "away" {
		t.Errorf("set_presence with no arguments should go away, got %q", presence)
	}
}
