// Package slack provides a service-oriented Slack client for slk-mcp.
//
// The Client composes narrow services (Channels, Messages, Users, Search,
// Unread) so tool handlers can depend on just what they need. Each service
// shares a retry-aware transport and a user-name cache.
package slack

import (
	"log/slog"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// Client aggregates all Slack services behind a single handle.
type Client struct {
	cfg *config.Config
	log *slog.Logger

	bot  *goslack.Client
	user *goslack.Client

	Channels *ChannelService
	Messages *MessageService
	Users    *UserService
	Search   *SearchService
	Unread   *UnreadService
}

// New constructs a Client. The bot token is required; the user token is
// optional and enables unread/mentions features.
func New(cfg *config.Config, log *slog.Logger) *Client {
	bot := goslack.New(cfg.BotToken)

	var user *goslack.Client
	if cfg.HasUserToken() {
		user = goslack.New(cfg.UserToken)
	}

	c := &Client{cfg: cfg, log: log, bot: bot, user: user}

	c.Users = newUserService(bot, log)
	c.Channels = newChannelService(bot, c.Users, log)
	c.Messages = newMessageService(bot, c.Channels, c.Users, log)
	c.Search = newSearchService(c.searchAPI(), log)
	c.Unread = newUnreadService(user, c.Channels, c.Users, log)

	return c
}

// Config returns the configuration this client was built with.
func (c *Client) Config() *config.Config { return c.cfg }

// HasUserToken reports whether a user token is available.
func (c *Client) HasUserToken() bool { return c.user != nil }

// searchAPI returns the API handle to use for search: user token if
// available (search.messages is gated on user tokens in newer Slack apps),
// falling back to the bot token otherwise.
func (c *Client) searchAPI() *goslack.Client {
	if c.user != nil {
		return c.user
	}
	return c.bot
}
