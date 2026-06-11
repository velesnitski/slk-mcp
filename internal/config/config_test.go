package config

import (
	"errors"
	"testing"
)

func TestValidate_RequiresAtLeastOneToken(t *testing.T) {
	c := &Config{}
	if err := c.Validate(); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("expected ErrMissingToken when no tokens set, got %v", err)
	}

	c.BotToken = "xoxb-test"
	if err := c.Validate(); err != nil {
		t.Fatalf("bot-only should be valid: %v", err)
	}

	c.BotToken = ""
	c.UserToken = "xoxp-test"
	if err := c.Validate(); err != nil {
		t.Fatalf("user-only should be valid: %v", err)
	}

	c.BotToken = "xoxb-test"
	c.UserToken = "xoxp-test"
	if err := c.Validate(); err != nil {
		t.Fatalf("both tokens should be valid: %v", err)
	}
}

func TestHasBotAndUserToken(t *testing.T) {
	c := &Config{}
	if c.HasBotToken() || c.HasUserToken() {
		t.Fatal("expected false for both on empty config")
	}
	c.BotToken = "xoxb-test"
	if !c.HasBotToken() || c.HasUserToken() {
		t.Fatal("expected bot-only")
	}
	c.UserToken = "xoxp-test"
	if !c.HasBotToken() || !c.HasUserToken() {
		t.Fatal("expected both")
	}
}

func TestPrimaryToken_PrefersBot(t *testing.T) {
	c := &Config{BotToken: "xoxb-test", UserToken: "xoxp-test"}
	if got := c.PrimaryToken(); got != "xoxb-test" {
		t.Fatalf("expected bot token, got %q", got)
	}

	c.BotToken = ""
	if got := c.PrimaryToken(); got != "xoxp-test" {
		t.Fatalf("expected user token as fallback, got %q", got)
	}
}

func TestPostsAsUser(t *testing.T) {
	cases := []struct {
		bot, user string
		want      bool
	}{
		{"xoxb", "", false},
		{"xoxb", "xoxp", false},
		{"", "xoxp", true},
		{"", "", false},
	}
	for _, tc := range cases {
		c := &Config{BotToken: tc.bot, UserToken: tc.user}
		if got := c.PostsAsUser(); got != tc.want {
			t.Fatalf("bot=%q user=%q: PostsAsUser()=%v want %v", tc.bot, tc.user, got, tc.want)
		}
	}
}

func TestIsDisabled_Normalisation(t *testing.T) {
	c := &Config{DisabledTools: parseSet("Post-Message, ADD_REACTION")}
	if !c.IsDisabled("post_message") {
		t.Fatal("expected post_message to be disabled (hyphen/case normalisation)")
	}
	if !c.IsDisabled("add_reaction") {
		t.Fatal("expected add_reaction to be disabled")
	}
	if c.IsDisabled("get_channel_digest") {
		t.Fatal("expected unrelated tool to be enabled")
	}
}

func TestParseWorkspaces_BlankYieldsNil(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\t"} {
		ws, err := ParseWorkspaces(in)
		if err != nil {
			t.Fatalf("blank %q: unexpected error %v", in, err)
		}
		if ws != nil {
			t.Fatalf("blank %q: expected nil, got %v", in, ws)
		}
	}
}

func TestParseWorkspaces_InvalidJSON(t *testing.T) {
	if _, err := ParseWorkspaces(`{not an array}`); err == nil {
		t.Fatal("expected error on non-array JSON")
	}
}

func TestParseWorkspaces_ValuesNotKeys(t *testing.T) {
	// The label lives in the JSON value ("name"), proving a workspace can
	// be added without any product-specific environment-variable key.
	in := `[{"name":"secondary","user_token":"xoxp-b","bot_token":"xoxb-b","channels":"alpha, beta"}]`
	ws, err := ParseWorkspaces(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(ws) != 1 {
		t.Fatalf("want 1 workspace, got %d", len(ws))
	}
	got := ws[0]
	if got.Name != "secondary" || got.UserToken != "xoxp-b" || got.BotToken != "xoxb-b" {
		t.Fatalf("unexpected workspace: %+v", got)
	}
	if len(got.Channels) != 2 || got.Channels[0] != "alpha" || got.Channels[1] != "beta" {
		t.Fatalf("channels CSV not parsed/trimmed: %v", got.Channels)
	}
}

func TestLoad_SingleWorkspaceBackCompat(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "xoxp-primary")
	t.Setenv("SLACK_TOKEN", "")
	t.Setenv("SLACK_WORKSPACES", "")

	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(c.Workspaces) != 1 {
		t.Fatalf("want 1 workspace, got %d", len(c.Workspaces))
	}
	if c.Workspaces[0].Name != "primary" {
		t.Fatalf("default primary name, got %q", c.Workspaces[0].Name)
	}
	if c.UserToken != "xoxp-primary" {
		t.Fatalf("legacy UserToken should mirror workspace[0], got %q", c.UserToken)
	}
}

func TestLoad_MultiWorkspace_LegacyPlusJSON(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "xoxp-primary")
	t.Setenv("SLACK_TOKEN", "xoxb-primary")
	t.Setenv("SLACK_WORKSPACE_NAME", "main")
	t.Setenv("SLACK_WORKSPACES", `[{"name":"secondary","user_token":"xoxp-secondary"}]`)

	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(c.Workspaces) != 2 {
		t.Fatalf("want 2 workspaces, got %d", len(c.Workspaces))
	}
	if c.Workspaces[0].Name != "main" || c.Workspaces[0].BotToken != "xoxb-primary" {
		t.Fatalf("primary not from legacy env: %+v", c.Workspaces[0])
	}
	if c.Workspaces[1].Name != "secondary" || c.Workspaces[1].UserToken != "xoxp-secondary" {
		t.Fatalf("secondary not from JSON: %+v", c.Workspaces[1])
	}
}

func TestLoad_WorkspacesOnly_NoLegacyToken(t *testing.T) {
	t.Setenv("SLACK_TOKEN", "")
	t.Setenv("SLACK_USER_TOKEN", "")
	t.Setenv("SLACK_WORKSPACES", `[{"name":"only","user_token":"xoxp-only"}]`)

	c := Load()
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(c.Workspaces) != 1 || c.Workspaces[0].Name != "only" {
		t.Fatalf("want single JSON workspace as primary, got %+v", c.Workspaces)
	}
	// Primary mirrored onto legacy fields so the primary client is built.
	if c.UserToken != "xoxp-only" {
		t.Fatalf("primary mirror failed: UserToken=%q", c.UserToken)
	}
}

func TestLoad_InvalidWorkspacesJSON_FailsValidate(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "xoxp-primary")
	t.Setenv("SLACK_WORKSPACES", `not-json`)

	c := Load()
	if err := c.Validate(); err == nil {
		t.Fatal("expected Validate to surface the JSON parse error")
	}
}

func TestValidate_WorkspaceMissingToken(t *testing.T) {
	c := &Config{
		BotToken: "xoxb-primary",
		Workspaces: []WorkspaceConfig{
			{Name: "primary", BotToken: "xoxb-primary"},
			{Name: "secondary"}, // no token
		},
	}
	if err := c.Validate(); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("expected ErrMissingToken for token-less workspace, got %v", err)
	}
}

func TestWorkspaceViews_SharesGlobalsScopesTokens(t *testing.T) {
	c := &Config{
		DigestHours:           12,
		MaxMessagesPerChannel: 99,
		Workspaces: []WorkspaceConfig{
			{Name: "primary", UserToken: "xoxp-a", Channels: []string{"a1"}},
			{Name: "secondary", UserToken: "xoxp-b", Channels: []string{"b1", "b2"}},
		},
	}
	views := c.WorkspaceViews()
	if len(views) != 2 {
		t.Fatalf("want 2 views, got %d", len(views))
	}
	for i, v := range views {
		if v.Cfg.DigestHours != 12 || v.Cfg.MaxMessagesPerChannel != 99 {
			t.Fatalf("view %d should share global scalars, got %+v", i, v.Cfg)
		}
	}
	if views[0].Cfg.UserToken != "xoxp-a" || views[1].Cfg.UserToken != "xoxp-b" {
		t.Fatalf("views should scope tokens: %q / %q", views[0].Cfg.UserToken, views[1].Cfg.UserToken)
	}
	if len(views[1].Cfg.Channels) != 2 {
		t.Fatalf("view should scope channels, got %v", views[1].Cfg.Channels)
	}
}

func TestWorkspaceViews_EmptyFallsBackToSelf(t *testing.T) {
	c := &Config{UserToken: "xoxp-test"}
	views := c.WorkspaceViews()
	if len(views) != 1 || views[0].Name != "primary" || views[0].Cfg != c {
		t.Fatalf("empty Workspaces should yield single self-backed primary view, got %+v", views)
	}
}

func TestParseIntDefault(t *testing.T) {
	if v := parseIntDefault("", 24); v != 24 {
		t.Fatalf("empty -> default: got %d", v)
	}
	if v := parseIntDefault("not-a-number", 24); v != 24 {
		t.Fatalf("invalid -> default: got %d", v)
	}
	if v := parseIntDefault("0", 24); v != 24 {
		t.Fatalf("non-positive -> default: got %d", v)
	}
	if v := parseIntDefault("42", 24); v != 42 {
		t.Fatalf("valid: got %d, want 42", v)
	}
}
