// Package config loads slk-mcp configuration from environment variables.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
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

	// Workspaces is the ordered list of Slack workspaces this server
	// serves. Workspaces[0] is the primary — its tokens mirror the
	// legacy BotToken/UserToken/Channels fields so every existing
	// single-workspace accessor keeps working unchanged.
	//
	// Load populates it from the legacy SLACK_TOKEN/SLACK_USER_TOKEN pair
	// (workspace[0]) plus the optional SLACK_WORKSPACES JSON array. A
	// workspace's human label lives in the JSON *value* ("name"), never
	// in an environment-variable key, so adding a workspace never
	// introduces a product-specific env key.
	Workspaces []WorkspaceConfig

	// workspacesErr records a SLACK_WORKSPACES parse failure. Load stays
	// error-free (its signature is value-only); Validate surfaces it.
	workspacesErr error
}

// WorkspaceConfig is one Slack workspace's credentials and optional
// channel scope. Tokens are workspace-scoped — a token minted in one
// workspace cannot read another — so multi-workspace support is
// fundamentally "one client per token set".
type WorkspaceConfig struct {
	// Name is a human label surfaced in merged digests (e.g. "[primary]").
	// It is cosmetic and never used as a lookup key for credentials.
	Name string

	BotToken  string
	UserToken string
	Channels  []string
}

// WorkspaceView pairs a workspace label with a fully-derived *Config
// whose token/channel fields are scoped to that workspace while every
// global scalar (read-only, disabled tools, digest hours, …) is shared
// with the parent. slack.NewRegistry builds one Client per view.
type WorkspaceView struct {
	Name string
	Cfg  *Config
}

// ErrMissingToken is returned when neither bot nor user token is configured.
var ErrMissingToken = errors.New(
	"at least one of SLACK_TOKEN (xoxb-...) or SLACK_USER_TOKEN (xoxp-...) is required",
)

// Load reads configuration from environment variables.
// Validation is the caller's responsibility (use Validate).
func Load() *Config {
	c := &Config{
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

	extra, err := ParseWorkspaces(os.Getenv("SLACK_WORKSPACES"))
	c.workspacesErr = err
	c.Workspaces = buildWorkspaces(
		os.Getenv("SLACK_WORKSPACE_NAME"),
		c.BotToken, c.UserToken, c.Channels, extra,
	)

	// Mirror the primary workspace back onto the legacy scalar fields so
	// every existing single-workspace accessor (PrimaryToken, HasBotToken,
	// the slack.Client built from this Config) reads workspace[0]. This is
	// what makes a SLACK_WORKSPACES-only config (no legacy SLACK_TOKEN)
	// still drive the primary client.
	if len(c.Workspaces) > 0 {
		c.BotToken = c.Workspaces[0].BotToken
		c.UserToken = c.Workspaces[0].UserToken
		c.Channels = c.Workspaces[0].Channels
	}

	return c
}

// wsJSON is the on-the-wire shape of one SLACK_WORKSPACES entry. The
// label is a value ("name"), keeping product-specific names out of env
// keys. `channels` is an optional comma-separated allow-list.
type wsJSON struct {
	Name      string `json:"name"`
	BotToken  string `json:"bot_token"`
	UserToken string `json:"user_token"`
	Channels  string `json:"channels"`
}

// ParseWorkspaces parses the SLACK_WORKSPACES JSON array. A blank value
// yields (nil, nil) — multi-workspace is purely additive, so the absence
// of the variable is the single-workspace path, not an error.
func ParseWorkspaces(s string) ([]WorkspaceConfig, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var raw []wsJSON
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("SLACK_WORKSPACES must be a JSON array of {name,bot_token,user_token,channels}: %w", err)
	}
	out := make([]WorkspaceConfig, 0, len(raw))
	for _, r := range raw {
		out = append(out, WorkspaceConfig{
			Name:      strings.TrimSpace(r.Name),
			BotToken:  strings.TrimSpace(r.BotToken),
			UserToken: strings.TrimSpace(r.UserToken),
			Channels:  parseCSV(r.Channels),
		})
	}
	return out, nil
}

// buildWorkspaces assembles the ordered workspace list: the legacy
// SLACK_TOKEN/SLACK_USER_TOKEN pair becomes workspace[0] (when either is
// set), followed by the SLACK_WORKSPACES entries. Empty names are filled
// deterministically so a label is always available for digest prefixes.
func buildWorkspaces(primaryName, botToken, userToken string, channels []string, extra []WorkspaceConfig) []WorkspaceConfig {
	var ws []WorkspaceConfig
	if botToken != "" || userToken != "" {
		ws = append(ws, WorkspaceConfig{
			Name:      strings.TrimSpace(primaryName),
			BotToken:  botToken,
			UserToken: userToken,
			Channels:  channels,
		})
	}
	ws = append(ws, extra...)
	for i := range ws {
		if ws[i].Name == "" {
			if i == 0 {
				ws[i].Name = "primary"
			} else {
				ws[i].Name = fmt.Sprintf("workspace-%d", i+1)
			}
		}
	}
	return ws
}

// WorkspaceViews returns one derived *Config per workspace: token and
// channel fields scoped to that workspace, every global scalar shared
// with the parent. A config with no Workspaces (e.g. a hand-built test
// Config) yields a single "primary" view backed by the config itself, so
// the single-workspace path needs no special-casing downstream.
func (c *Config) WorkspaceViews() []WorkspaceView {
	if len(c.Workspaces) == 0 {
		return []WorkspaceView{{Name: "primary", Cfg: c}}
	}
	views := make([]WorkspaceView, 0, len(c.Workspaces))
	for _, ws := range c.Workspaces {
		wc := *c
		wc.BotToken = ws.BotToken
		wc.UserToken = ws.UserToken
		wc.Channels = ws.Channels
		wc.Workspaces = nil
		wc.workspacesErr = nil
		views = append(views, WorkspaceView{Name: ws.Name, Cfg: &wc})
	}
	return views
}

// Validate returns an error if required fields are missing.
func (c *Config) Validate() error {
	if c.workspacesErr != nil {
		return c.workspacesErr
	}
	if c.BotToken == "" && c.UserToken == "" {
		return ErrMissingToken
	}
	// Every additional workspace must carry at least one token; a
	// token-less entry is a config typo we want to fail loudly on rather
	// than silently build a dead client for.
	for i, ws := range c.Workspaces {
		if ws.BotToken == "" && ws.UserToken == "" {
			return fmt.Errorf("workspace %d (%q): %w", i, ws.Name, ErrMissingToken)
		}
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
