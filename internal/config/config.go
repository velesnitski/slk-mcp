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
// At least one of BotToken or UserToken is required.
//   - BotToken (xoxb-): acts as the bot identity; posts appear as the bot.
//   - UserToken (xoxp-): acts as the authenticated user; required for
//     unread/mentions/mark_read and for search.messages in modern apps.
//
// When both are set, BotToken is used for general reads and writes while
// UserToken is used for the personal-workflow tools. When only UserToken
// is set, it is used for everything (user tokens are a functional superset
// of bot tokens).
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

	// AutodiscoverLimit caps the number of channels used when no channels
	// are passed and SLACK_CHANNELS is empty (the server falls back to the
	// channels the bot/user has joined).
	AutodiscoverLimit int
}

// ErrMissingToken is returned when neither bot nor user token is configured.
var ErrMissingToken = errors.New(
	"at least one of SLACK_TOKEN (xoxb-...) or SLACK_USER_TOKEN (xoxp-...) is required",
)

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
		AutodiscoverLimit:     parseIntDefault(os.Getenv("SLACK_AUTODISCOVER_LIMIT"), 50),

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
	if c.BotToken == "" && c.UserToken == "" {
		return ErrMissingToken
	}
	return nil
}

// HasBotToken reports whether a bot token is configured.
func (c *Config) HasBotToken() bool { return c.BotToken != "" }

// HasUserToken reports whether a user token is configured.
// User-token-only tools must guard with this.
func (c *Config) HasUserToken() bool { return c.UserToken != "" }

// PrimaryToken returns the best available token for general API calls.
//
// Prefers the bot token (posts appear as the bot, preserves bot identity).
// Falls back to the user token when no bot token is configured, in which
// case API calls act AS the user — posts will appear as the authenticated
// user, not as a bot.
func (c *Config) PrimaryToken() string {
	if c.BotToken != "" {
		return c.BotToken
	}
	return c.UserToken
}

// PostsAsUser reports whether posts/reactions will appear as the
// authenticated user rather than as a bot. True when only a user token is
// configured. Useful for surfacing the current identity mode in logs.
func (c *Config) PostsAsUser() bool {
	return c.BotToken == "" && c.UserToken != ""
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
