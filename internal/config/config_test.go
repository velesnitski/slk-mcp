package config

import (
	"errors"
	"testing"
)

func TestValidate_RequiresBotToken(t *testing.T) {
	c := &Config{}
	if err := c.Validate(); !errors.Is(err, ErrMissingBotToken) {
		t.Fatalf("expected ErrMissingBotToken, got %v", err)
	}
	c.BotToken = "xoxb-test"
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHasUserToken(t *testing.T) {
	c := &Config{BotToken: "xoxb-test"}
	if c.HasUserToken() {
		t.Fatal("expected false without user token")
	}
	c.UserToken = "xoxp-test"
	if !c.HasUserToken() {
		t.Fatal("expected true with user token")
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
		t.Fatalf("valid: got %d", v)
	}
}
