package slack

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"testing"
)

// svNow is the frozen clock these tests run against.
const svNow int64 = 1700100000

// svAuthOK registers an auth.test response for the operator.
func svAuthOK(f *svFake) {
	f.reply("auth.test", svJSON{
		"ok": true, "user_id": "U1", "user": "alex",
		"url": "https://example.invalid/", "team": "T1", "team_id": "T1",
	})
}

// svDisabledUnread is the service shape you get without a user token.
func svDisabledUnread() *UnreadService {
	return newUnreadService(nil, nil, nil, nil, svLogger())
}

func TestTeamURL_ComesFromTheCachedAuthTest(t *testing.T) {
	f := svServer(t)
	svAuthOK(f)
	svc := svUnread(t, f, nil)

	url, err := svc.TeamURL(context.Background())
	if err != nil {
		t.Fatalf("TeamURL: %v", err)
	}
	if url != "https://example.invalid/" {
		t.Fatalf("url = %q", url)
	}

	// Self, TeamURL and SelfHandle all share one auth.test round trip.
	if _, err := svc.SelfHandle(context.Background()); err != nil {
		t.Fatalf("SelfHandle: %v", err)
	}
	if _, err := svc.TeamURL(context.Background()); err != nil {
		t.Fatalf("TeamURL again: %v", err)
	}
	if n := f.count("auth.test"); n != 1 {
		t.Fatalf("auth.test called %d times, want 1", n)
	}
}

func TestTeamURL_WithoutAUserTokenIsTheSentinel(t *testing.T) {
	url, err := svDisabledUnread().TeamURL(context.Background())
	if !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("err = %v, want ErrNoUserToken", err)
	}
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
}

func TestTeamURL_AuthTestFailurePropagates(t *testing.T) {
	f := svServer(t)
	f.fail("auth.test", "invalid_auth")

	if _, err := svUnread(t, f, nil).TeamURL(context.Background()); err == nil {
		t.Fatal("TeamURL returned nil error")
	}
}

func TestSelfHandle_AuthTestFailurePropagates(t *testing.T) {
	f := svServer(t)
	f.fail("auth.test", "token_revoked")

	handle, err := svUnread(t, f, nil).SelfHandle(context.Background())
	if err == nil {
		t.Fatal("SelfHandle returned nil error")
	}
	if handle != "" {
		t.Fatalf("handle = %q", handle)
	}
}

func TestJoinedChannels_PaginatesAndAsksForDMTypes(t *testing.T) {
	f := svServer(t)
	f.pages("users.conversations",
		svJSON{
			"ok":                true,
			"channels":          []svJSON{svChan("C1", "alpha", nil)},
			"response_metadata": svCursor("page2"),
		},
		svJSON{"ok": true, "channels": []svJSON{svChan("D1", "", svJSON{"is_im": true})}},
	)

	got, err := svUnread(t, f, nil).JoinedChannels(context.Background())
	if err != nil {
		t.Fatalf("JoinedChannels: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	types := f.form(t, "users.conversations", 0).Get("types")
	for _, want := range []string{"public_channel", "private_channel", "mpim", "im"} {
		if !svHasSubstr(types, want) {
			t.Fatalf("types = %q, missing %q", types, want)
		}
	}
	if cur := f.form(t, "users.conversations", 1).Get("cursor"); cur != "page2" {
		t.Fatalf("cursor = %q", cur)
	}
}

func TestJoinedChannels_FailureIsReturnedNotAnEmptyList(t *testing.T) {
	f := svServer(t)
	f.fail("users.conversations", "missing_scope")

	got, err := svUnread(t, f, nil).JoinedChannels(context.Background())
	if err == nil {
		t.Fatal("JoinedChannels returned nil error")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
	if !svHasSubstr(err.Error(), "users.conversations") {
		t.Fatalf("err = %q", err)
	}
}

func TestJoinedChannels_WithoutAUserTokenIsTheSentinel(t *testing.T) {
	if _, err := svDisabledUnread().JoinedChannels(context.Background()); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("err = %v, want ErrNoUserToken", err)
	}
}

func TestUnread_NoLastReadShortCircuitsWithoutFetchingHistory(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", nil)})

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 0)
	if err != nil {
		t.Fatalf("Unread: %v", err)
	}
	if cu.LastRead != "" || len(cu.Messages) != 0 {
		t.Fatalf("cu = %+v", cu)
	}
	if n := f.count("conversations.history"); n != 0 {
		t.Fatalf("conversations.history called %d times, want 0", n)
	}
}

func TestUnread_InfoFailureIsReturned(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.info", "channel_not_found")

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 10)
	if err == nil {
		t.Fatal("Unread returned nil error")
	}
	if cu != nil {
		t.Fatalf("cu = %+v, want nil", cu)
	}
}

func TestUnread_HistoryFailureIsReturnedNotAnEmptyChannel(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
		"last_read": "1700090000.000000",
	})})
	f.fail("conversations.history", "ratelimited")

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 10)
	if err == nil {
		t.Fatal("Unread returned nil error — a failed history must not read as 'caught up'")
	}
	if cu != nil {
		t.Fatalf("cu = %+v, want nil", cu)
	}
}

func TestUnread_ReachesBehindLastReadForActiveThreadParents(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
		"last_read": "1700099000.000000",
	})})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		// New top-level message.
		svMsg("1700099500.000000", "U2", "new", nil),
		// Parent read long ago but still moving — only visible because
		// the lookback window reaches behind last_read.
		svMsg("1700080000.000000", "U2", "old parent", svJSON{
			"thread_ts": "1700080000.000000", "reply_count": 2, "latest_reply": "1700099900.000000",
		}),
	}})
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700080000.000000", "U2", "old parent", svJSON{"thread_ts": "1700080000.000000"}),
		svMsg("1700099900.000000", "U3", "fresh reply", svJSON{"thread_ts": "1700080000.000000"}),
	}})

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 10)
	if err != nil {
		t.Fatalf("Unread: %v", err)
	}
	if len(cu.Messages) != 1 || cu.Messages[0].Text != "new" {
		t.Fatalf("Messages = %+v, want only the post-last_read message", cu.Messages)
	}
	replies := cu.Replies["1700080000.000000"]
	if len(replies) != 1 || replies[0].Text != "fresh reply" {
		t.Fatalf("Replies = %+v", cu.Replies)
	}

	// ADR 113: the page is anchored at now, never at a lower bound — a
	// page bounded by `oldest` is the one adjacent to it. The old parent
	// is reachable because it is on the newest page, within the lookback.
	if oldest := f.form(t, "conversations.history", 0).Get("oldest"); oldest != "" {
		t.Fatalf("oldest = %q, want no lower bound on the request", oldest)
	}
	if lim := f.form(t, "conversations.history", 0).Get("limit"); lim != "40" {
		t.Fatalf("limit = %q, want 10 + the 30 parent headroom", lim)
	}
}

func TestUnread_ReplyFetchFailureStillReturnsTheTopLevelMessages(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
		"last_read": "1700099000.000000",
	})})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700099500.000000", "U2", "new parent", svJSON{
			"thread_ts": "1700099500.000000", "reply_count": 1, "latest_reply": "1700099600.000000",
		}),
	}})
	f.fail("conversations.replies", "ratelimited")

	cu, err := svUnread(t, f, nil).Unread(context.Background(), "C1", 10)
	if err != nil {
		t.Fatalf("Unread: %v", err)
	}
	if len(cu.Messages) != 1 {
		t.Fatalf("Messages = %+v", cu.Messages)
	}
	if len(cu.Replies) != 0 {
		t.Fatalf("Replies = %+v, want none when the fetch failed", cu.Replies)
	}
}

func TestUnreadAll_OmitsCaughtUpChannelsAndKeepsTheRest(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil), svChan("C2", "beta", nil),
	}})
	f.on("conversations.info", func(r *http.Request) svJSON {
		if r.FormValue("channel") == "C1" {
			return svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
				"last_read": "1700099000.000000",
			})}
		}
		// beta has never been read: no last_read, so nothing to report.
		return svJSON{"ok": true, "channel": svChan("C2", "beta", nil)}
	})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700099500.000000", "U2", "hello", nil),
	}})

	got, err := svUnread(t, f, nil).UnreadAll(context.Background(), 10)
	if err != nil {
		t.Fatalf("UnreadAll: %v", err)
	}
	if len(got) != 1 || got[0].Channel.ID != "C1" {
		t.Fatalf("got = %+v, want only alpha", got)
	}
}

// A channel whose fetch fails is logged and dropped: the sweep returns a
// SHORTER list with a nil error. Pinned so a change to that contract is
// a deliberate one.
func TestUnreadAll_ChannelFailureIsDroppedNotReported(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", nil), svChan("C2", "beta", nil),
	}})
	f.on("conversations.info", func(r *http.Request) svJSON {
		if r.FormValue("channel") == "C2" {
			return svJSON{"ok": false, "error": "ratelimited"}
		}
		return svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
			"last_read": "1700099000.000000",
		})}
	})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700099500.000000", "U2", "hello", nil),
	}})

	got, err := svUnread(t, f, nil).UnreadAll(context.Background(), 10)
	if err != nil {
		t.Fatalf("UnreadAll: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d channels, want 1 (beta is silently dropped)", len(got))
	}
}

func TestUnreadAll_JoinedChannelsFailureIsReturned(t *testing.T) {
	f := svServer(t)
	f.fail("users.conversations", "invalid_auth")

	if _, err := svUnread(t, f, nil).UnreadAll(context.Background(), 10); err == nil {
		t.Fatal("UnreadAll returned nil error")
	}
}

func TestUnreadAll_WithoutAUserTokenIsTheSentinel(t *testing.T) {
	if _, err := svDisabledUnread().UnreadAll(context.Background(), 10); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("err = %v, want ErrNoUserToken", err)
	}
}

func TestRecentDMActivity_HistoryFailureDropsOnlyThatConversation(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("D1", "", svJSON{"is_im": true}),
		svChan("D2", "", svJSON{"is_im": true}),
	}})
	f.on("conversations.history", func(r *http.Request) svJSON {
		if r.FormValue("channel") == "D2" {
			return svJSON{"ok": false, "error": "ratelimited"}
		}
		return svJSON{"ok": true, "messages": []svJSON{
			svMsg("1700099500.000000", "U2", "hi", nil),
			svMsg("1700090000.000000", "U2", "older than the window", nil),
		}}
	})

	got, err := svUnread(t, f, nil).RecentDMActivity(context.Background(), 2, 5)
	if err != nil {
		t.Fatalf("RecentDMActivity: %v", err)
	}
	if len(got) != 1 || got[0].Channel.ID != "D1" {
		t.Fatalf("got = %+v, want only D1", got)
	}
	// ADR 111: the page is anchored at now, not at `oldest` — a page
	// anchored at the lower bound holds the window's oldest messages.
	if oldest := f.form(t, "conversations.history", 0).Get("oldest"); oldest != "" {
		t.Fatalf("oldest = %q, want no lower bound on the request", oldest)
	}
	// The window is applied locally instead.
	if n := len(got[0].Messages); n != 1 || got[0].Messages[0].Text != "hi" {
		t.Fatalf("messages = %+v, want only the in-window one", got[0].Messages)
	}
}

func TestRecentDMActivity_DefaultsThePerChannelCap(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("D1", "", svJSON{"is_im": true}),
	}})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{}})

	if _, err := svUnread(t, f, nil).RecentDMActivity(context.Background(), 1, 0); err != nil {
		t.Fatalf("RecentDMActivity: %v", err)
	}
	if lim := f.form(t, "conversations.history", 0).Get("limit"); lim != "20" {
		t.Fatalf("limit = %q, want the 20 default", lim)
	}
}

func TestRecentDMActivity_JoinedChannelsFailureIsReturned(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.fail("users.conversations", "invalid_auth")

	if _, err := svUnread(t, f, nil).RecentDMActivity(context.Background(), 2, 5); err == nil {
		t.Fatal("RecentDMActivity returned nil error")
	}
}

func TestMarkRead_SendsTheChannelAndTimestamp(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.mark", svJSON{"ok": true})

	if err := svUnread(t, f, nil).MarkRead(context.Background(), "C1", "1700099500.000000"); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	form := f.form(t, "conversations.mark", 0)
	if form.Get("channel") != "C1" || form.Get("ts") != "1700099500.000000" {
		t.Fatalf("form = %v", form)
	}
}

func TestMarkRead_SlackErrorPropagates(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.mark", "channel_not_found")

	if err := svUnread(t, f, nil).MarkRead(context.Background(), "C1", "1.0"); err == nil {
		t.Fatal("MarkRead returned nil error")
	}
}

func TestMarkRead_WithoutAUserTokenIsTheSentinel(t *testing.T) {
	if err := svDisabledUnread().MarkRead(context.Background(), "C1", "1.0"); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("err = %v, want ErrNoUserToken", err)
	}
}

func TestUnreadOwnThreads_WithoutAUserTokenIsTheSentinel(t *testing.T) {
	if _, err := svDisabledUnread().UnreadOwnThreads(context.Background(), 24); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("err = %v, want ErrNoUserToken", err)
	}
}

func TestUnreadOwnThreads_NoopWithoutASearchBackendOrAWindow(t *testing.T) {
	f := svServer(t)
	svc := svUnread(t, f, nil)

	got, err := svc.UnreadOwnThreads(context.Background(), 24)
	if err != nil || got != nil {
		t.Fatalf("no search backend: got %v, %v", got, err)
	}

	withSearch := svUnread(t, f, svSearch(t, f))
	if got, err := withSearch.UnreadOwnThreads(context.Background(), 0); err != nil || got != nil {
		t.Fatalf("zero hours: got %v, %v", got, err)
	}
	if got, err := withSearch.UnreadOwnThreads(context.Background(), -1); err != nil || got != nil {
		t.Fatalf("negative hours: got %v, %v", got, err)
	}
	if n := len(f.forms("search.messages")); n != 0 {
		t.Fatalf("search.messages called %d times, want 0", n)
	}
}

func TestUnreadOwnThreads_SurfacesRepliesNewerThanMyLastMessage(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svAuthOK(f)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		{
			"ts": "1700099000.000000", "user": "U1", "text": "my ask",
			"channel":   svJSON{"id": "C1", "name": "alpha"},
			"permalink": "https://example.invalid/archives/C1/p1700099000000000",
		},
	}}})
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700099000.000000", "U1", "my ask", svJSON{"thread_ts": "1700099000.000000"}),
		svMsg("1700099100.000000", "U2", "here you go", svJSON{"thread_ts": "1700099000.000000"}),
	}})

	got, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(context.Background(), 2)
	if err != nil {
		t.Fatalf("UnreadOwnThreads: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d channels, want 1", len(got))
	}
	if got[0].Channel.ID != "C1" || got[0].Channel.Name != "alpha" {
		t.Fatalf("channel = %+v", got[0].Channel)
	}
	replies := got[0].Replies["1700099000.000000"]
	if len(replies) != 1 || replies[0].Text != "here you go" {
		t.Fatalf("replies = %+v", got[0].Replies)
	}
	if q := f.form(t, "search.messages", 0).Get("query"); !svHasSubstr(q, "from:me") {
		t.Fatalf("query = %q, want a from:me search", q)
	}
}

func TestUnreadOwnThreads_ThreadWithNoNewReplyIsOmitted(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svAuthOK(f)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		{
			"ts": "1700099000.000000", "user": "U1", "text": "my last word",
			"channel": svJSON{"id": "C1", "name": "alpha"},
		},
	}}})
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svMsg("1700098000.000000", "U2", "their question", svJSON{"thread_ts": "1700098000.000000"}),
		svMsg("1700099000.000000", "U1", "my last word", svJSON{"thread_ts": "1700098000.000000"}),
	}})

	got, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(context.Background(), 2)
	if err != nil {
		t.Fatalf("UnreadOwnThreads: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want nothing (I spoke last)", got)
	}
}

func TestUnreadOwnThreads_SkipsHitsOlderThanTheExactWindow(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svAuthOK(f)
	// search's after: is day-granular, so Slack returns same-day hits
	// that predate the hour window; they must be re-filtered out.
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		{"ts": "1700090000.000000", "user": "U1", "channel": svJSON{"id": "C1", "name": "alpha"}},
		{"ts": "", "user": "U1", "channel": svJSON{"id": "", "name": ""}},
	}}})

	got, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(context.Background(), 1)
	if err != nil {
		t.Fatalf("UnreadOwnThreads: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want nothing", got)
	}
	if n := f.count("conversations.replies"); n != 0 {
		t.Fatalf("conversations.replies called %d times, want 0", n)
	}
}

func TestUnreadOwnThreads_SearchFailureIsReturned(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svAuthOK(f)
	f.fail("search.messages", "ratelimited")

	got, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(context.Background(), 2)
	if err == nil {
		t.Fatal("UnreadOwnThreads returned nil error")
	}
	if got != nil {
		t.Fatalf("got = %+v, want nil", got)
	}
	if !svHasSubstr(err.Error(), "search from:me") {
		t.Fatalf("err = %q", err)
	}
}

func TestUnreadOwnThreads_AuthFailureIsReturned(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.fail("auth.test", "invalid_auth")

	if _, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(context.Background(), 2); err == nil {
		t.Fatal("UnreadOwnThreads returned nil error")
	}
}

func TestUnreadOwnThreads_UnreadableThreadIsSkippedNotFatal(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	svAuthOK(f)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		{"ts": "1700099000.000000", "user": "U1", "channel": svJSON{"id": "C1", "name": "alpha"}},
		{"ts": "1700099010.000000", "user": "U1", "channel": svJSON{"id": "C2", "name": "beta"}},
	}}})
	f.on("conversations.replies", func(r *http.Request) svJSON {
		if r.FormValue("channel") == "C2" {
			return svJSON{"ok": false, "error": "fetch_failed"}
		}
		return svJSON{"ok": true, "messages": []svJSON{
			svMsg("1700099000.000000", "U1", "mine", svJSON{"thread_ts": "1700099000.000000"}),
			svMsg("1700099200.000000", "U2", "theirs", svJSON{"thread_ts": "1700099000.000000"}),
		}}
	})

	got, err := svUnread(t, f, svSearch(t, f)).UnreadOwnThreads(context.Background(), 2)
	if err != nil {
		t.Fatalf("UnreadOwnThreads: %v", err)
	}
	if len(got) != 1 || got[0].Channel.ID != "C1" {
		t.Fatalf("got = %+v, want only alpha", got)
	}
}

func TestParticipationChannels_WithoutAUserTokenIsTheSentinel(t *testing.T) {
	if _, err := svDisabledUnread().ParticipationChannels(context.Background(), 24); !errors.Is(err, ErrNoUserToken) {
		t.Fatalf("err = %v, want ErrNoUserToken", err)
	}
}

func TestParticipationChannels_NoopWithoutASearchBackendOrAWindow(t *testing.T) {
	f := svServer(t)
	if got, err := svUnread(t, f, nil).ParticipationChannels(context.Background(), 24); err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := svUnread(t, f, svSearch(t, f)).ParticipationChannels(context.Background(), 0); err != nil || got != nil {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestParticipationChannels_DedupesAndRefiltersTheWindow(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		{"ts": "1700099000.000000", "channel": svJSON{"id": "C1", "name": "alpha"}},
		{"ts": "1700099500.000000", "channel": svJSON{"id": "C1", "name": "alpha"}},
		{"ts": "1700099600.000000", "channel": svJSON{"id": "D1", "name": "sam"}},
		// Same day, before the hour window.
		{"ts": "1700080000.000000", "channel": svJSON{"id": "C2", "name": "beta"}},
		// No channel at all.
		{"ts": "1700099700.000000", "channel": svJSON{"id": "", "name": ""}},
	}}})

	got, err := svUnread(t, f, svSearch(t, f)).ParticipationChannels(context.Background(), 1)
	if err != nil {
		t.Fatalf("ParticipationChannels: %v", err)
	}
	var ids []string
	for _, ch := range got {
		ids = append(ids, ch.ID)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "C1" || ids[1] != "D1" {
		t.Fatalf("ids = %v, want [C1 D1]", ids)
	}
}

func TestParticipationChannels_SearchFailureIsReturned(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	f.fail("search.messages", "ratelimited")

	got, err := svUnread(t, f, svSearch(t, f)).ParticipationChannels(context.Background(), 1)
	if err == nil {
		t.Fatal("ParticipationChannels returned nil error")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
}
