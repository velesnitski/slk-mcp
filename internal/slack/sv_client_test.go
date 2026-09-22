package slack

import (
	"context"
	"fmt"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// svClientFor builds a Client whose every service talks to the fake.
// New() itself cannot be pointed at a test server (it mints its clients
// from the config tokens), so the services are rebuilt around one
// fake-bound API handle. withUser mirrors "a user token is configured",
// which is what JoinedChannelNames branches on.
func svClientFor(t *testing.T, f *svFake, cfg *config.Config, withUser bool) *Client {
	t.Helper()
	api := svUserAPI(f)
	log := svLogger()

	c := &Client{cfg: cfg, log: log, bot: api}
	if withUser {
		c.user = api
	}
	c.Users = newUserService(api, log)
	c.Channels = newChannelService(api, c.Users, log)
	c.Unread = newUnreadService(api, c.Channels, c.Users, nil, log)
	return c
}

func TestNew_BotOnlyKeepsTheUserServicesDisabled(t *testing.T) {
	cfg := &config.Config{BotToken: "xoxb-test"}
	c := New(cfg, svLogger())

	if c.HasUserToken() {
		t.Fatal("HasUserToken() = true for a bot-only config")
	}
	if c.Config() != cfg {
		t.Fatal("Config() did not return the config New was built with")
	}
	for name, svc := range map[string]any{
		"Channels": c.Channels, "Messages": c.Messages, "Users": c.Users,
		"Search": c.Search, "Unread": c.Unread, "Lists": c.Lists,
		"Status": c.Status, "DND": c.DND, "Scheduled": c.Scheduled, "Canvas": c.Canvas,
	} {
		if svc == nil {
			t.Fatalf("%s service is nil", name)
		}
	}
	// Without a user token the unread surface is inert rather than absent.
	if c.Unread.Enabled() {
		t.Fatal("Unread.Enabled() = true for a bot-only config")
	}
	if c.Lists.HasToken() {
		t.Fatal("Lists.HasToken() = true for a bot-only config")
	}
}

func TestNew_UserOnlyPromotesTheUserTokenToPrimary(t *testing.T) {
	c := New(&config.Config{UserToken: "xoxp-test"}, svLogger())

	if !c.HasUserToken() {
		t.Fatal("HasUserToken() = false for a user-only config")
	}
	if !c.Unread.Enabled() {
		t.Fatal("Unread.Enabled() = false although a user token is configured")
	}
	// The user token IS the primary, so the message service must not
	// carry a second identity to fall back to.
	if c.Messages.fallback != nil {
		t.Fatal("Messages.fallback is set although both identities are the same client")
	}
}

func TestNew_BothTokensKeepTwoDistinctIdentities(t *testing.T) {
	c := New(&config.Config{BotToken: "xoxb-test", UserToken: "xoxp-test"}, svLogger())

	if !c.HasUserToken() {
		t.Fatal("HasUserToken() = false")
	}
	if c.bot == nil || c.user == nil {
		t.Fatalf("bot = %v, user = %v, want both", c.bot, c.user)
	}
	if c.bot == c.user {
		t.Fatal("bot and user share one client although the tokens differ")
	}
	if c.Messages.fallback == nil {
		t.Fatal("Messages.fallback is nil although a distinct user token exists")
	}
	// Search prefers the user identity — search.messages is user-gated.
	if c.Search.api != c.user {
		t.Fatal("Search does not use the user identity")
	}
}

func TestNew_BotOnlySearchFallsBackToTheBotIdentity(t *testing.T) {
	c := New(&config.Config{BotToken: "xoxb-test"}, svLogger())
	if c.Search.api != c.bot {
		t.Fatal("Search does not fall back to the bot identity")
	}
}

func TestJoinedChannelNames_UserPathSortsByMembersAndDropsArchived(t *testing.T) {
	f := svServer(t)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", svJSON{"num_members": 3}),
		svChan("C2", "beta", svJSON{"num_members": 9}),
		svChan("C3", "general", svJSON{"num_members": 40, "is_archived": true}),
	}})
	c := svClientFor(t, f, &config.Config{UserToken: "xoxp-test"}, true)

	got, err := c.JoinedChannelNames(context.Background(), 0)
	if err != nil {
		t.Fatalf("JoinedChannelNames: %v", err)
	}
	if strings.Join(got, ",") != "beta,alpha" {
		t.Fatalf("got = %v, want [beta alpha]", got)
	}
	if n := f.count("conversations.list"); n != 0 {
		t.Fatalf("conversations.list called %d times on the user path, want 0", n)
	}
}

func TestJoinedChannelNames_HonoursTheLimit(t *testing.T) {
	f := svServer(t)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", svJSON{"num_members": 3}),
		svChan("C2", "beta", svJSON{"num_members": 9}),
	}})
	c := svClientFor(t, f, &config.Config{UserToken: "xoxp-test"}, true)

	got, err := c.JoinedChannelNames(context.Background(), 1)
	if err != nil {
		t.Fatalf("JoinedChannelNames: %v", err)
	}
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("got = %v, want [beta]", got)
	}
}

func TestJoinedChannelNames_BotPathReadsTheWorkspaceListing(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.list", svJSON{"ok": true, "channels": []svJSON{
		svChan("C1", "alpha", svJSON{"num_members": 3}),
		svChan("C2", "beta", svJSON{"num_members": 9}),
	}})
	c := svClientFor(t, f, &config.Config{BotToken: "xoxb-test"}, false)

	got, err := c.JoinedChannelNames(context.Background(), 0)
	if err != nil {
		t.Fatalf("JoinedChannelNames: %v", err)
	}
	if strings.Join(got, ",") != "beta,alpha" {
		t.Fatalf("got = %v", got)
	}
	if n := f.count("users.conversations"); n != 0 {
		t.Fatalf("users.conversations called %d times without a user token, want 0", n)
	}
}

// DEFECT PIN — JoinedChannelNames documents limit 0 as "no cap", and it
// is the only caller that means it. On the bot path it forwards that 0
// to ChannelService.List, where 0 is re-interpreted as 100, so a
// workspace with more visible channels is silently truncated BEFORE the
// member-count sort. The result looks like "the top channels" but is an
// arbitrary first page. This test records the behaviour as it ships; it
// is not an endorsement of it.
func TestJoinedChannelNames_BotPathSilentlyCapsAtHundred(t *testing.T) {
	f := svServer(t)
	many := make([]svJSON, 0, 130)
	for i := 0; i < 130; i++ {
		many = append(many, svChan(fmt.Sprintf("C%03d", i), fmt.Sprintf("ch-%03d", i), svJSON{
			"num_members": i,
		}))
	}
	f.reply("conversations.list", svJSON{"ok": true, "channels": many})
	c := svClientFor(t, f, &config.Config{BotToken: "xoxb-test"}, false)

	got, err := c.JoinedChannelNames(context.Background(), 0)
	if err != nil {
		t.Fatalf("JoinedChannelNames: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("got %d names, want 100 — the current (capped) behaviour", len(got))
	}
	// The truncation happens before the sort, so the busiest channels
	// (ch-100..ch-129) never make it into the answer.
	for _, name := range got {
		if name == "ch-129" {
			t.Fatal("ch-129 survived: the cap no longer precedes the sort — update this pin")
		}
	}
}

func TestJoinedChannelNames_UserPathErrorPropagates(t *testing.T) {
	f := svServer(t)
	f.fail("users.conversations", "invalid_auth")
	c := svClientFor(t, f, &config.Config{UserToken: "xoxp-test"}, true)

	got, err := c.JoinedChannelNames(context.Background(), 0)
	if err == nil {
		t.Fatal("JoinedChannelNames returned nil error")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
}

func TestJoinedChannelNames_BotPathErrorPropagates(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.list", "missing_scope")
	c := svClientFor(t, f, &config.Config{BotToken: "xoxb-test"}, false)

	if _, err := c.JoinedChannelNames(context.Background(), 0); err == nil {
		t.Fatal("JoinedChannelNames returned nil error")
	}
}

func TestJoinedChannelNames_EmptyWorkspaceYieldsAnEmptyList(t *testing.T) {
	f := svServer(t)
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{}})
	c := svClientFor(t, f, &config.Config{UserToken: "xoxp-test"}, true)

	got, err := c.JoinedChannelNames(context.Background(), 0)
	if err != nil {
		t.Fatalf("JoinedChannelNames: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %v, want empty", got)
	}
}

func TestSearchAPI_PrefersTheUserIdentityWhenBothExist(t *testing.T) {
	log := svLogger()
	bot := goslack.New("xoxb-test")
	user := goslack.New("xoxp-test")

	both := &Client{log: log, bot: bot, user: user}
	if both.searchAPI() != user {
		t.Fatal("searchAPI() did not prefer the user identity")
	}
	botOnly := &Client{log: log, bot: bot}
	if botOnly.searchAPI() != bot {
		t.Fatal("searchAPI() did not fall back to the bot identity")
	}
}
