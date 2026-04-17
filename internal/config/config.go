// Package config loads slk-mcp configuration from environment variables.
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// Config is the loaded runtime configuration for slk-mcp.
//
// BotToken is used for channel reads, posts and reactions (works with xoxb-).
// UserToken is optional and required only for unread / mentions tools (xoxp-).
type Config struct {
	BotToken  string
	UserToken string

	Channels      []string
	ReadOnly      bool
	DisabledTools map[string]struct{}
	DigestHours   int

	DecisionKeywords  []string
	DecisionReactions []string

	MaxMessagesPerChannel int
	CompactOutput         bool
}

// ErrMissingBotToken is returned when no bot token is configured.
var ErrMissingBotToken = errors.New("SLACK_TOKEN is required (xoxb-... Bot User OAuth Token)")

// Load reads configuration from environment variables.
// Validation is the caller's responsibility (use Validate).
func Load() *Config {
	return &Config{
		BotToken:              os.Getenv("SLACK_TOKEN"),
		UserToken:             os.Getenv("SLACK_USER_TOKEN"),
		Channels:              parseCSV(os.Getenv("SLACK_CHANNELS")),
		ReadOnly:              parseBool(os.Getenv("SLACK_READ_ONLY")),
		DisabledTools:         parseSet(os.Getenv("DISABLED_TOOLS")),
		DigestHours:           parseIntDefault(os.Getenv("SLACK_DIGEST_HOURS"), 24),
		MaxMessagesPerChannel: parseIntDefault(os.Getenv("SLACK_MAX_MESSAGES"), 200),
		CompactOutput:         parseBoolDefault(os.Getenv("SLACK_COMPACT"), true),

		DecisionKeywords: []string{
			"decided", "approved", "let's go with", "agreed",
			"confirmed", "moving forward", "final answer",
		},
		DecisionReactions: []string{
			"white_check_mark", "heavy_check_mark", "eyes", "thumbsup",
		},
	}
}

// Validate returns an error if required fields are missing.
func (c *Config) Validate() error {
	if c.BotToken == "" {
		return ErrMissingBotToken
	}
	return nil
}

// HasUserToken reports whether a user token is configured.
// User-token-only tools must guard with this.
func (c *Config) HasUserToken() bool {
	return c.UserToken != ""
}

// IsDisabled reports whether a tool is in the DISABLED_TOOLS set.
func (c *Config) IsDisabled(tool string) bool {
	_, ok := c.DisabledTools[normalizeToolName(tool)]
	return ok
}

func parseCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, v := range parseCSV(s) {
		set[normalizeToolName(v)] = struct{}{}
	}
	return set
}

func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "yes"
}

func parseBoolDefault(s string, def bool) bool {
	if s == "" {
		return def
	}
	return parseBool(s)
}

func parseIntDefault(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func normalizeToolName(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "-", "_"))
}
