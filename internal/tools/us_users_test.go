package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// usRoster is a users.list body built from raw member objects.
func usRoster(members ...string) string {
	return `{"ok":true,"members":[` + strings.Join(members, ",") + `],"response_metadata":{"next_cursor":""}}`
}

const (
	usMemberAlex = `{"id":"U1","name":"alex","real_name":"Alex","updated":1700000000,
		"profile":{"title":"QA Engineer","real_name":"Alex"}}`
	usMemberSam = `{"id":"U2","name":"sam","real_name":"Sam","updated":1700000000,
		"profile":{"title":"Support","real_name":"Sam"}}`
	usMemberBot = `{"id":"B1","name":"buildbot","real_name":"Build Bot","is_bot":true,"updated":1700000000}`
	usMemberSB  = `{"id":"USLACKBOT","name":"slackbot","real_name":"Slackbot","updated":1700000000}`
)

func TestRegisterUserTools_RegistersListUsers(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t)
	if names := usToolNames(t, h.registerUserTools); !usHasTool(names, "list_users") {
		t.Fatalf("list_users should be registered, got %v", names)
	}
}

func TestRegisterUserTools_DisabledRegistersNothing(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t, func(c *config.Config) { c.DisabledTools = map[string]struct{}{"list_users": {}} })
	if names := usToolNames(t, h.registerUserTools); len(names) != 0 {
		t.Fatalf("a disabled tool must be unreachable, got %v", names)
	}
}

func TestRunListUsers_ExcludesBotsAndSlackbotByDefault(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(usMemberSam, usMemberBot, usMemberSB, usMemberAlex))
	h := f.hub(t)

	res := h.runListUsers(context.Background(), "", false, false, "")
	if res.IsError {
		t.Fatalf("roster read failed: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.HasPrefix(text, "2 users\n") {
		t.Fatalf("bots and slackbot should be excluded; got:\n%s", text)
	}
	if strings.Contains(text, "buildbot") || strings.Contains(text, "slackbot") {
		t.Errorf("bot accounts leaked into the default roster:\n%s", text)
	}
	// Sorted by handle, so alex precedes sam.
	if strings.Index(text, "alex") > strings.Index(text, "sam") {
		t.Errorf("roster should be sorted by handle:\n%s", text)
	}
}

func TestRunListUsers_IncludeBotsAddsThemWithAFlag(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(usMemberAlex, usMemberBot, usMemberSB))
	h := f.hub(t)

	text := resultText(h.runListUsers(context.Background(), "", true, false, ""))
	if !strings.HasPrefix(text, "3 users\n") {
		t.Fatalf("include_bots should count every account; got:\n%s", text)
	}
	if !strings.Contains(text, "buildbot") || !strings.Contains(text, " bot ") {
		t.Errorf("a bot account must be marked as one:\n%s", text)
	}
}

func TestRunListUsers_FilterIsCaseInsensitiveAcrossFields(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(usMemberAlex, usMemberSam))
	h := f.hub(t)

	// Job title.
	text := resultText(h.runListUsers(context.Background(), "", false, false, "qa"))
	if !strings.HasPrefix(text, "1 users\n") || !strings.Contains(text, "alex") {
		t.Fatalf("filter should match the job title; got:\n%s", text)
	}
	// Handle.
	text = resultText(h.runListUsers(context.Background(), "", false, false, "sam"))
	if !strings.HasPrefix(text, "1 users\n") || !strings.Contains(text, "sam") {
		t.Fatalf("filter should match the handle; got:\n%s", text)
	}
	// No match is an honest empty roster, not the full list.
	text = resultText(h.runListUsers(context.Background(), "", false, false, "nobody-here"))
	if !strings.HasPrefix(text, "0 users") {
		t.Fatalf("an unmatched filter must yield an empty roster; got:\n%s", text)
	}
}

func TestRunListUsers_APIErrorIsReported(t *testing.T) {
	f := usNewFakeSlack(t)
	f.OnError("users.list", "missing_scope")
	h := f.hub(t)

	res := h.runListUsers(context.Background(), "", false, false, "")
	if res == nil || !res.IsError {
		t.Fatalf("a failed roster read must be an error, not an empty list; got %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "missing_scope") {
		t.Errorf("the Slack reason should survive: %q", resultText(res))
	}
}

func TestRunListUsers_MultiWorkspaceSectionsAndPerWorkspaceError(t *testing.T) {
	f := usNewFakeSlack(t)
	calls := 0
	f.OnFunc("users.list", func(*http.Request) string {
		calls++
		if calls == 1 {
			return usRoster(usMemberAlex)
		}
		return `{"ok":false,"error":"missing_scope"}`
	})
	h := f.multiHub(t, usWS{Name: "alpha"}, usWS{Name: "beta"})

	res := h.runListUsers(context.Background(), "", false, false, "")
	if res.IsError {
		t.Fatalf("one failing workspace must not void the other: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "## [alpha]") || !strings.Contains(text, "alex") {
		t.Errorf("the working workspace should render under its label:\n%s", text)
	}
	// The failure has to be visible: a section that silently disappears
	// reads as "that workspace has no users".
	if !strings.Contains(text, "## [beta]") || !strings.Contains(text, "_error: ") {
		t.Errorf("the failing workspace must report its error:\n%s", text)
	}
}

func TestRunListUsers_WithActivityAddsLastPostPerUser(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(usMemberAlex, usMemberSam))
	f.OnFunc("search.messages", func(r *http.Request) string {
		if strings.Contains(r.Form.Get("query"), "alex") {
			return `{"ok":true,"messages":{"total":1,"matches":[{"ts":"1700000000.000100","text":"hi"}]}}`
		}
		return `{"ok":true,"messages":{"total":0,"matches":[]}}`
	})
	h := f.hub(t)

	text := resultText(h.runListUsers(context.Background(), "", false, true, ""))
	if !strings.Contains(text, "last_post=2023-11-14") {
		t.Errorf("a search hit should become a date:\n%s", text)
	}
	// No hit must read as "unknown", never as a blank column the reader
	// would take for a date that failed to render.
	if !strings.Contains(text, "last_post=(none found)") {
		t.Errorf("a user with no hit should be labelled:\n%s", text)
	}
}

func TestFetchLastPostDates_SearchFailureLeavesTheDateEmpty(t *testing.T) {
	f := usNewFakeSlack(t)
	f.OnError("search.messages", "not_allowed_token_type")
	h := f.hub(t)

	users := []goslack.User{
		{ID: "U1", Name: "alex"},
		{ID: "U2", Name: "sam"},
		{ID: "U3", Name: "pat"},
	}
	got := h.fetchLastPostDates(context.Background(), users)
	if len(got) != 3 {
		t.Fatalf("every user must get an entry, got %v", got)
	}
	for id, date := range got {
		if date != "" {
			t.Errorf("a failed search must not invent a date for %s: %q", id, date)
		}
	}
}

func TestFetchLastPostDates_QueriesEachUserOnce(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("search.messages", `{"ok":true,"messages":{"total":1,"matches":[{"ts":"1700000000.000100"}]}}`)
	h := f.hub(t)

	users := make([]goslack.User, 0, 9)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		users = append(users, goslack.User{ID: "U" + strings.ToUpper(name), Name: name})
	}
	got := h.fetchLastPostDates(context.Background(), users)
	if len(got) != len(users) {
		t.Fatalf("the worker pool dropped results: %d of %d", len(got), len(users))
	}
	if n := len(f.Calls()); n != len(users) {
		t.Errorf("expected one search per user, got %d calls", n)
	}
	for id, date := range got {
		if date != "2023-11-14" {
			t.Errorf("%s: date = %q", id, date)
		}
	}
}

func TestRenderUserRows_FlagsAndNameFallbacks(t *testing.T) {
	owner := goslack.User{ID: "U1", Name: "owner.one", IsOwner: true}
	owner.Profile.RealName = "Pat"

	guest := goslack.User{ID: "U2", Name: "guest.one", IsRestricted: true}
	guest.Profile.DisplayName = "Sam"

	admin := goslack.User{ID: "U3", Name: "admin.one", IsAdmin: true, IsOwner: true, RealName: "Alex"}
	nameless := goslack.User{ID: "U4", Name: "ghost"}

	got := renderUserRows([]goslack.User{owner, guest, admin, nameless}, nil, false)
	for _, want := range []string{
		"U1 | owner.one | Pat |  | owner",  // RealName falls back to Profile.RealName
		"U2 | guest.one | Sam |  | guest",  // then to Profile.DisplayName
		"U3 | admin.one | Alex |  | admin", // admin wins over owner
		"U4 | ghost | (no name) |",         // never an empty name column
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderUserRows_ActivityModeLabelsAMissingDate(t *testing.T) {
	u := goslack.User{ID: "U1", Name: "alex", RealName: "Alex"}
	got := renderUserRows([]goslack.User{u}, map[string]string{}, true)
	if !strings.Contains(got, "last_post=(none found)") {
		t.Fatalf("an absent date must be labelled, not blank:\n%s", got)
	}
}

func TestRenderUserRows_EmptyRosterStillCounts(t *testing.T) {
	if got := renderUserRows(nil, nil, false); got != "0 users\n" {
		t.Fatalf("an empty roster should still carry the count header, got %q", got)
	}
}

func TestUserMatchesFilter_SearchesEveryNameField(t *testing.T) {
	u := goslack.User{ID: "U1", Name: "alex", RealName: "Alex"}
	u.Profile.DisplayName = "al"
	u.Profile.Title = "Support Lead"

	for _, needle := range []string{"alex", "al", "support lead", "lead"} {
		if !userMatchesFilter(u, needle) {
			t.Errorf("%q should match", needle)
		}
	}
	if userMatchesFilter(u, "devops") {
		t.Error("an unrelated needle must not match")
	}
	// The caller lowercases the needle; the haystack is lowercased here,
	// so an already-lowercased needle is all that ever arrives.
	if userMatchesFilter(u, "ALEX") {
		t.Error("the haystack is lowercased, so an uppercase needle cannot match")
	}
}

func TestParseSlackTS_HandlesGarbage(t *testing.T) {
	if got := parseSlackTS(""); !got.IsZero() {
		t.Errorf("empty ts should be the zero time, got %v", got)
	}
	if got := parseSlackTS("not-a-ts"); !got.IsZero() {
		t.Errorf("non-numeric ts should be the zero time, got %v", got)
	}
	if got := parseSlackTS("1700000000.000100"); !got.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("ts should parse to its second, got %v", got)
	}
	// The fractional part is Slack's per-message counter, not sub-second
	// precision, so it is deliberately ignored.
	if got := parseSlackTS("1700000000"); !got.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("a ts without a fraction should still parse, got %v", got)
	}
}

func TestListUsersTool_ParsesArgumentsFromTheClient(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(usMemberAlex, usMemberSam, usMemberBot))
	h := f.hub(t)

	res := usCallTool(t, h.registerUserTools, "list_users", map[string]any{
		"include_bots": true,
		"filter":       "  QA  ", // trimmed and lowercased by the closure
	})
	if res.IsError {
		t.Fatalf("tool call failed: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.HasPrefix(text, "1 users\n") || !strings.Contains(text, "alex") {
		t.Fatalf("the filter argument should reach the handler trimmed and case-folded:\n%s", text)
	}
}

func TestListUsersTool_UnknownWorkspaceNamesTheOnesItHas(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.multiHub(t, usWS{Name: "alpha"}, usWS{Name: "beta"})

	res := h.runListUsers(context.Background(), "ghost", false, false, "")
	if res == nil || !res.IsError {
		t.Fatalf("an unknown workspace must error, got %+v", res)
	}
	text := resultText(res)
	if !strings.Contains(text, "alpha") || !strings.Contains(text, "beta") {
		t.Errorf("the error should list the workspaces that do exist: %q", text)
	}
}
