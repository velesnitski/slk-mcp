package tools

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	goslack "github.com/slack-go/slack"
)

// ADR 110: every tool that accepts a permalink routes by the link's host.
// These tests assert on WHICH fake Slack received the call — a routing bug
// is invisible in the result text when both workspaces answer the same
// error, so the call log is the only honest witness.

const (
	prBetaHost = "https://beta.example.com"
	prBetaLink = prBetaHost + "/archives/C0BETA0001/p1700000000000100"
)

// prTwoHub is a two-workspace hub (alpha primary, beta secondary) whose
// workspaces self-identify by host, which is what routing keys on.
func prTwoHub(t *testing.T) (h *Hub, alpha, beta *fakeSlack) {
	t.Helper()
	alpha, beta = newFakeSlack(t), newFakeSlack(t)
	tmAuthTest(alpha, "U1", tmHost+"/")
	tmAuthTest(beta, "U2", prBetaHost+"/")
	return tmTwoFakeHub(t, alpha, beta), alpha, beta
}

// calledBeyondAuth reports whether f served anything other than auth.test,
// which routing itself calls on every workspace to learn its host.
func calledBeyondAuth(f *fakeSlack) bool {
	for _, c := range f.Calls() {
		if c != "auth.test" {
			return true
		}
	}
	return false
}

func TestFetchFiles_PermalinkFromSecondWorkspaceRoutesThere(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	beta.On("conversations.history", `{"ok":true,"messages":[]}`)

	_, _, _, errRes := h.fetchFiles(context.Background(), "", "", "", prBetaLink, "",
		os.TempDir(), "slk-test", isImageFile)

	if !beta.Called("conversations.history") {
		t.Fatalf("the message lookup must go to beta; beta calls=%v", beta.Calls())
	}
	if calledBeyondAuth(alpha) {
		t.Fatalf("the primary must not be asked about a beta link; alpha calls=%v", alpha.Calls())
	}
	// Empty history is a failure, and a failure after auto-routing says
	// where it was tried.
	if errRes == nil || !errRes.IsError {
		t.Fatal("an empty lookup should be an error")
	}
	tmWant(t, resultText(errRes), "workspace auto-detected from permalink")
}

func TestFetchFiles_ExplicitWorkspaceStillWins(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	alpha.On("conversations.history", `{"ok":true,"messages":[]}`)

	_, _, _, _ = h.fetchFiles(context.Background(), "alpha", "", "", prBetaLink, "",
		os.TempDir(), "slk-test", isImageFile)

	if !alpha.Called("conversations.history") {
		t.Fatalf("explicit workspace=alpha must be honoured; alpha calls=%v", alpha.Calls())
	}
	if beta.Called("conversations.history") {
		t.Fatal("the link's host must not override an explicit workspace")
	}
}

func TestFetchFiles_UnknownHostFallbackIsAnnouncedOnFailure(t *testing.T) {
	// No workspace owns the host: the call falls back to the primary. If
	// that then fails, the caller must be told it was a fallback — a bare
	// "not found" from the wrong workspace is the silent miss this ADR
	// exists to remove.
	h, alpha, _ := prTwoHub(t)
	alpha.On("conversations.history", `{"ok":true,"messages":[]}`)

	_, _, _, errRes := h.fetchFiles(context.Background(), "", "", "",
		"https://gamma.example.com/archives/C0GAMMA001/p1700000000000100", "",
		os.TempDir(), "slk-test", isImageFile)

	if errRes == nil || !errRes.IsError {
		t.Fatal("expected an error result")
	}
	tmWant(t, resultText(errRes), "no configured workspace matches host", "gamma.example.com")
}

func TestViewImage_PermalinkFromSecondWorkspaceRoutesThere(t *testing.T) {
	// The reported case, end to end through the registered tool.
	h, alpha, beta := prTwoHub(t)
	beta.On("conversations.history", `{"ok":true,"messages":[]}`)
	s := tmServer(t, h.registerImageTools)

	_ = tmCall(t, s, "view_image", map[string]any{"permalink": prBetaLink})

	if !beta.Called("conversations.history") || calledBeyondAuth(alpha) {
		t.Fatalf("view_image must route to beta; alpha=%v beta=%v", alpha.Calls(), beta.Calls())
	}
}

func TestDeleteMessage_PermalinkFromSecondWorkspaceDeletesThere(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	beta.On("chat.delete", `{"ok":true,"channel":"C0BETA0001","ts":"1700000000.000100"}`)

	res := h.runDeleteMessage(context.Background(), "", "", "", prBetaLink)

	if !beta.Called("chat.delete") {
		t.Fatalf("delete must reach beta; beta calls=%v result=%q", beta.Calls(), resultText(res))
	}
	if alpha.Called("chat.delete") {
		t.Fatal("an irreversible delete must never be sent to a workspace the link does not belong to")
	}
	tmWant(t, resultText(res), "[beta]")
}

func TestMarkRead_PermalinkFromSecondWorkspaceMarksThere(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	beta.On("conversations.mark", `{"ok":true}`)
	s := tmServer(t, h.registerUnreadTools)

	res := tmCall(t, s, "mark_read", map[string]any{"permalink": prBetaLink})

	if !beta.Called("conversations.mark") {
		t.Fatalf("mark_read must reach beta; beta calls=%v result=%q", beta.Calls(), resultText(res))
	}
	if alpha.Called("conversations.mark") {
		t.Fatal("mark_read must not advance the primary's cursor for a beta link")
	}
}

func TestMarkRead_ChannelNameIsNotDoublePrefixed(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("conversations.mark", `{"ok":true}`)
	s := tmServer(t, newFakeHub(t, f).registerUnreadTools)

	out := resultText(tmCall(t, s, "mark_read", map[string]any{
		"channel": "#alpha", "timestamp": tmRootTS,
	}))

	if strings.Contains(out, "##") {
		t.Fatalf("a channel passed with its sigil must not gain a second one: %q", out)
	}
	tmWant(t, out, "marked #alpha read up to")
}

func TestWithRouteNote(t *testing.T) {
	if withRouteNote(nil, "note") != nil {
		t.Fatal("nil in, nil out")
	}

	ok := mcp.NewToolResultText("done")
	if got := resultText(withRouteNote(ok, "note")); got != "done" {
		t.Fatalf("a success must be left alone, got %q", got)
	}

	failed := mcp.NewToolResultError("channel_not_found")
	if got := resultText(withRouteNote(failed, "")); got != "channel_not_found" {
		t.Fatalf("an empty note must change nothing, got %q", got)
	}
	if got := resultText(withRouteNote(failed, "tried the primary")); got != "channel_not_found (tried the primary)" {
		t.Fatalf("an error must carry the note, got %q", got)
	}
}

// ADR 114: Slack Connect. A file or channel shared from another
// organisation keeps that organisation's host, so no configured workspace
// matches it — yet one of them can read it. The primary is tried first,
// then the rest.

const (
	scForeignHost = "https://other-org.example.com"
	scFileLink    = scForeignHost + "/files/U0OWNER01/F0SHARED01/notes.md"
	scMsgLink     = scForeignHost + "/archives/C0SHARED01/p1700000000000100"
)

func TestFetchFiles_SlackConnectFileFoundInSecondWorkspace(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	alpha.OnError("files.info", "file_not_found")
	beta.On("files.info", `{"ok":true,"file":{"id":"F0SHARED01","name":"notes.md","mimetype":"image/png"}}`)

	_, _, wsName, errRes := h.fetchFiles(context.Background(), "", "", "", scFileLink, "",
		os.TempDir(), "slk-test", func(goslack.File) bool { return false })

	if !alpha.Called("files.info") || !beta.Called("files.info") {
		t.Fatalf("primary first, then the other workspace; alpha=%v beta=%v", alpha.Calls(), beta.Calls())
	}
	if errRes != nil && strings.Contains(resultText(errRes), "file_not_found") {
		t.Fatalf("the file beta can see must not end as not-found: %q", resultText(errRes))
	}
	_ = wsName
}

func TestFetchFiles_SlackConnectFailureNamesEveryWorkspaceTried(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	alpha.OnError("files.info", "file_not_found")
	beta.OnError("files.info", "file_not_found")

	_, _, _, errRes := h.fetchFiles(context.Background(), "", "", "", scFileLink, "",
		os.TempDir(), "slk-test", isImageFile)

	if errRes == nil || !errRes.IsError {
		t.Fatal("a file nobody can see is an error")
	}
	tmWant(t, resultText(errRes), "other-org.example.com", "tried alpha, beta")
}

func TestFetchFiles_MatchedHostIsNotRetriedElsewhere(t *testing.T) {
	// A host that belongs to beta is beta's answer — a not-found there
	// must not leak the lookup into alpha.
	h, alpha, beta := prTwoHub(t)
	beta.OnError("files.info", "file_not_found")

	_, _, _, _ = h.fetchFiles(context.Background(), "", "", "",
		prBetaHost+"/files/U0OWNER01/F0BETAFILE/x.png", "", os.TempDir(), "slk-test", isImageFile)

	if alpha.Called("files.info") {
		t.Fatal("a routed host must not fall back to other workspaces")
	}
}

func TestFetchFiles_ExplicitWorkspaceIsNotRetriedElsewhere(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	alpha.OnError("files.info", "file_not_found")

	_, _, _, _ = h.fetchFiles(context.Background(), "alpha", "", "", scFileLink, "",
		os.TempDir(), "slk-test", isImageFile)

	if beta.Called("files.info") {
		t.Fatal("an explicit workspace is the caller's answer — no fallback")
	}
}

func TestGetMessage_SlackConnectChannelFoundInSecondWorkspace(t *testing.T) {
	h, alpha, beta := prTwoHub(t)
	alpha.OnError("conversations.history", "channel_not_found")
	alpha.OnError("conversations.replies", "channel_not_found")
	beta.On("conversations.history", `{"ok":true,"messages":[{"type":"message","user":"U2","ts":"1700000000.000100","text":"shared hello"}]}`)
	beta.On("conversations.replies", `{"ok":true,"messages":[{"type":"message","user":"U2","ts":"1700000000.000100","text":"shared hello"}]}`)
	s := tmServer(t, h.registerMessageTools)

	out := resultText(tmCall(t, s, "get_message", map[string]any{"permalink": scMsgLink}))

	tmWant(t, out, "shared hello", "found via beta")
}
