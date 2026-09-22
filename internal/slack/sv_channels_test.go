package slack

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestChannelList_ReturnsEveryPageBeforeStopping(t *testing.T) {
	f := svServer(t)
	f.pages("conversations.list",
		svJSON{
			"ok":                true,
			"channels":          []svJSON{svChan("C1", "alpha", nil), svChan("C2", "beta", nil)},
			"response_metadata": svCursor("page2"),
		},
		svJSON{
			"ok":                true,
			"channels":          []svJSON{svChan("C3", "general", nil)},
			"response_metadata": svCursor(""),
		},
	)

	got, err := svChannels(t, f).List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d channels, want 3 — pagination dropped a page", len(got))
	}
	if got[2].ID != "C3" {
		t.Fatalf("last channel = %q, want C3", got[2].ID)
	}
	if n := f.count("conversations.list"); n != 2 {
		t.Fatalf("conversations.list called %d times, want 2", n)
	}
	if cur := f.form(t, "conversations.list", 1).Get("cursor"); cur != "page2" {
		t.Fatalf("second page cursor = %q, want page2", cur)
	}
}

func TestChannelList_SendsTypesLimitAndExcludesArchived(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.list", svJSON{"ok": true, "channels": []svJSON{}})

	if _, err := svChannels(t, f).List(context.Background(), 5); err != nil {
		t.Fatalf("List: %v", err)
	}
	form := f.form(t, "conversations.list", 0)
	if got := form.Get("types"); got != "public_channel,private_channel" {
		t.Fatalf("types = %q", got)
	}
	if got := form.Get("limit"); got != "200" {
		t.Fatalf("limit = %q, want 200 (the page size, not the caller's cap)", got)
	}
	if got := form.Get("exclude_archived"); got != "true" {
		t.Fatalf("exclude_archived = %q, want true", got)
	}
}

func TestChannelList_TruncatesToTheCallersLimit(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.list", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil), svChan("C2", "beta", nil), svChan("C3", "general", nil),
	}})

	got, err := svChannels(t, f).List(context.Background(), 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
}

// A zero limit is NOT "everything": List quietly substitutes 100. The
// behaviour is pinned here because Client.JoinedChannelNames passes 0
// meaning "no cap" — see TestJoinedChannelNames_BotPathSilentlyCapsAtHundred.
func TestChannelList_ZeroLimitSilentlyMeansHundred(t *testing.T) {
	f := svServer(t)
	many := make([]svJSON, 0, 150)
	for i := 0; i < 150; i++ {
		many = append(many, svChan(fmt.Sprintf("C%03d", i), fmt.Sprintf("ch-%03d", i), nil))
	}
	f.reply("conversations.list", svJSON{"ok": true, "channels": many})

	got, err := svChannels(t, f).List(context.Background(), 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("got %d, want 100", len(got))
	}
}

func TestChannelList_SlackErrorIsReturnedNotAnEmptyList(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.list", "invalid_auth")

	got, err := svChannels(t, f).List(context.Background(), 10)
	if err == nil {
		t.Fatal("List returned nil error on invalid_auth")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil on error", got)
	}
}

func TestChannelList_SecondPageFailureDoesNotYieldAShortList(t *testing.T) {
	f := svServer(t)
	f.pages("conversations.list",
		svJSON{
			"ok":                true,
			"channels":          []svJSON{svChan("C1", "alpha", nil)},
			"response_metadata": svCursor("page2"),
		},
		svJSON{"ok": false, "error": "ratelimited"},
	)

	got, err := svChannels(t, f).List(context.Background(), 50)
	if err == nil {
		t.Fatal("List returned nil error although the second page failed")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil — a partial walk must not look complete", got)
	}
}

func TestResolveID_FindsTheChannelOnALaterPage(t *testing.T) {
	f := svServer(t)
	f.pages("conversations.list",
		svJSON{
			"ok":                true,
			"channels":          []svJSON{svChan("C1", "alpha", nil)},
			"response_metadata": svCursor("page2"),
		},
		svJSON{
			"ok":       true,
			"channels": []svJSON{svChan("C2", "beta", nil)},
		},
	)

	id, err := svChannels(t, f).ResolveID(context.Background(), "beta")
	if err != nil {
		t.Fatalf("ResolveID: %v", err)
	}
	if id != "C2" {
		t.Fatalf("id = %q, want C2", id)
	}
}

func TestResolveID_SecondLookupIsServedFromCache(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.list", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil), svChan("C2", "beta", nil),
	}})
	svc := svChannels(t, f)

	if _, err := svc.ResolveID(context.Background(), "#alpha"); err != nil {
		t.Fatalf("first ResolveID: %v", err)
	}
	// The walk caches every channel it saw, not just the hit.
	if _, err := svc.ResolveID(context.Background(), "beta"); err != nil {
		t.Fatalf("second ResolveID: %v", err)
	}
	if n := f.count("conversations.list"); n != 1 {
		t.Fatalf("conversations.list called %d times, want 1 (the cache should serve the second lookup)", n)
	}
}

func TestResolveID_EmptyNameIsRejectedWithoutACall(t *testing.T) {
	f := svServer(t)
	svc := svChannels(t, f)

	if _, err := svc.ResolveID(context.Background(), "#"); err == nil {
		t.Fatal("ResolveID(\"#\") returned nil error")
	}
	if n := f.count("conversations.list"); n != 0 {
		t.Fatalf("conversations.list called %d times, want 0", n)
	}
}

func TestResolveID_UnknownNameSuggestsTheClosestChannels(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.list", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil), svChan("C2", "alpha-notes", nil), svChan("C3", "general", nil),
	}})

	_, err := svChannels(t, f).ResolveID(context.Background(), "alph")
	if err == nil {
		t.Fatal("ResolveID returned nil error for an unknown channel")
	}
	if !svHasSubstr(err.Error(), "did you mean") {
		t.Fatalf("err = %q, want a suggestion", err)
	}
	if !svHasSubstr(err.Error(), "#alpha") {
		t.Fatalf("err = %q, want #alpha suggested", err)
	}
}

func TestResolveID_UnknownNameWithNoNeighbourHasNoSuggestion(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.list", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "general", nil),
	}})

	_, err := svChannels(t, f).ResolveID(context.Background(), "zzzzzzzz")
	if err == nil {
		t.Fatal("ResolveID returned nil error for an unknown channel")
	}
	if svHasSubstr(err.Error(), "did you mean") {
		t.Fatalf("err = %q, should not guess wildly", err)
	}
}

func TestResolveID_ListFailureIsReportedAsSuch(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.list", "missing_scope")

	_, err := svChannels(t, f).ResolveID(context.Background(), "alpha")
	if err == nil {
		t.Fatal("ResolveID returned nil error")
	}
	if !svHasSubstr(err.Error(), "list channels") || !svHasSubstr(err.Error(), "missing_scope") {
		t.Fatalf("err = %q, want the underlying Slack error wrapped", err)
	}
}

func TestChannelInfo_RequestsMemberCountAndPopulatesTheCache(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.info", svJSON{
		"ok":      true,
		"channel": svChan("C1", "alpha", svJSON{"num_members": 12}),
	})
	svc := svChannels(t, f)

	ch, err := svc.Info(context.Background(), "C1")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if ch.Name != "alpha" || ch.NumMembers != 12 {
		t.Fatalf("Info = %+v, want alpha/12", ch)
	}
	if got := f.form(t, "conversations.info", 0).Get("include_num_members"); got != "true" {
		t.Fatalf("include_num_members = %q, want true", got)
	}

	// The cache Info populated must serve NamesForIDs without a call.
	names := svc.NamesForIDs(context.Background(), []string{"C1"})
	if names["C1"] != "alpha" {
		t.Fatalf("NamesForIDs = %v, want C1 -> alpha", names)
	}
	if n := f.count("conversations.info"); n != 1 {
		t.Fatalf("conversations.info called %d times, want 1", n)
	}
}

func TestChannelInfo_SlackErrorPropagates(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.info", "channel_not_found")

	if _, err := svChannels(t, f).Info(context.Background(), "C1"); err == nil {
		t.Fatal("Info returned nil error")
	}
}

func TestNamesForIDs_EmptyInputMakesNoCallAtAll(t *testing.T) {
	f := svServer(t)
	if got := svChannels(t, f).NamesForIDs(context.Background(), nil); got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
	if n := len(f.forms("conversations.info")); n != 0 {
		t.Fatalf("made %d calls, want 0", n)
	}
}

// A channel the token cannot see is dropped from the map rather than
// failing the batch — the digest still renders with a raw #CID.
func TestNamesForIDs_SkipsBlankAndUnresolvableIDs(t *testing.T) {
	f := svServer(t)
	f.on("conversations.info", func(r *http.Request) svJSON {
		if r.FormValue("channel") == "C1" {
			return svJSON{"ok": true, "channel": svChan("C1", "alpha", nil)}
		}
		return svJSON{"ok": false, "error": "channel_not_found"}
	})

	got := svChannels(t, f).NamesForIDs(context.Background(), []string{"C1", "", "C9"})
	if len(got) != 1 || got["C1"] != "alpha" {
		t.Fatalf("got = %v, want only C1 -> alpha", got)
	}
	// The blank id is filtered before the API, so only C9 is looked up
	// alongside C1.
	if n := f.count("conversations.info"); n != 2 {
		t.Fatalf("conversations.info called %d times, want 2", n)
	}
}

func TestNamesForIDs_IgnoresAChannelWithNoName(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svJSON{"id": "C1"}})

	got := svChannels(t, f).NamesForIDs(context.Background(), []string{"C1"})
	if len(got) != 0 {
		t.Fatalf("got = %v, want empty", got)
	}
}

func TestMembers_PaginatesAndSendsTheCursor(t *testing.T) {
	f := svServer(t)
	f.pages("conversations.members",
		svJSON{"ok": true, "members": []string{"U1", "U2"}, "response_metadata": svCursor("next")},
		svJSON{"ok": true, "members": []string{"U3"}, "response_metadata": svCursor("")},
	)

	got, err := svChannels(t, f).Members(context.Background(), "C1", 0)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if strings.Join(got, ",") != "U1,U2,U3" {
		t.Fatalf("got = %v, want U1,U2,U3", got)
	}
	if cur := f.form(t, "conversations.members", 1).Get("cursor"); cur != "next" {
		t.Fatalf("cursor = %q, want next", cur)
	}
	if lim := f.form(t, "conversations.members", 0).Get("limit"); lim != "200" {
		t.Fatalf("limit = %q, want 200", lim)
	}
}

func TestMembers_StopsAndTruncatesAtTheLimit(t *testing.T) {
	f := svServer(t)
	f.pages("conversations.members",
		svJSON{"ok": true, "members": []string{"U1", "U2", "U3"}, "response_metadata": svCursor("next")},
		svJSON{"ok": true, "members": []string{"U4"}},
	)

	got, err := svChannels(t, f).Members(context.Background(), "C1", 2)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if n := f.count("conversations.members"); n != 1 {
		t.Fatalf("conversations.members called %d times, want 1 — the limit was already met", n)
	}
}

func TestMembers_SlackErrorIsReturnedNotAnEmptyRoster(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.members", "channel_not_found")

	got, err := svChannels(t, f).Members(context.Background(), "C1", 0)
	if err == nil {
		t.Fatal("Members returned nil error")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
	if !svHasSubstr(err.Error(), "list members") {
		t.Fatalf("err = %q, want it wrapped as a member listing failure", err)
	}
}

func TestOpenDM_ReturnsTheConversationID(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.open", svJSON{"ok": true, "channel": svJSON{"id": "D1"}})

	got, err := svChannels(t, f).OpenDM(context.Background(), "U1")
	if err != nil {
		t.Fatalf("OpenDM: %v", err)
	}
	if got != "D1" {
		t.Fatalf("got = %q, want D1", got)
	}
	if users := f.form(t, "conversations.open", 0).Get("users"); users != "U1" {
		t.Fatalf("users = %q, want U1", users)
	}
}

func TestOpenDM_SlackErrorPropagates(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.open", "user_not_found")

	if _, err := svChannels(t, f).OpenDM(context.Background(), "U1"); err == nil {
		t.Fatal("OpenDM returned nil error")
	}
}

// ok:true with no channel object is the "looks successful, carries
// nothing" shape — it must not resolve to an empty channel ID.
func TestOpenDM_MissingChannelIsAnErrorNotAnEmptyID(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.open", svJSON{"ok": true})

	got, err := svChannels(t, f).OpenDM(context.Background(), "U1")
	if err == nil {
		t.Fatalf("OpenDM returned %q with a nil error", got)
	}
	if got != "" {
		t.Fatalf("got = %q, want empty", got)
	}
}

func TestIsUserID_AcceptsClassicAndGridIDs(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"U0AAAA1111B", true},
		{"W0AAAA1111B", true},
		{"C0AAAA1111B", false},
		{"D0AAAA1111B", false},
		{"U1", false},                    // too short
		{"U0AAAA1111B0AAAA1111B", false}, // too long
		{"U0aaaa1111b", false},           // lowercase is not a canonical id
		{"", false},
	}
	for _, c := range cases {
		if got := IsUserID(c.in); got != c.want {
			t.Errorf("IsUserID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
