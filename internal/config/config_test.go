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
