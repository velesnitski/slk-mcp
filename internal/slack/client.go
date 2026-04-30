// Package slack provides a service-oriented Slack client for slk-mcp.
//
// The Client composes narrow services (Channels, Messages, Users, Search,
// Unread) so tool handlers can depend on just what they need. Each service
// shares a retry-aware transport and a user-name cache.
package slack

import (
	"context"
	"log/slog"
	"sort"

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

// New constructs a Client from a validated config.
//
// Token model:
//   - If a bot token is set, it is the primary API for reads/posts.
//   - If only a user token is set, the user token acts as primary. In that
//     mode posts/reactions appear as the authenticated user, not as a bot.
//   - The user token — when present — is additionally used for the Unread
//     service (unread/mentions/mark_read) and preferred for Search.
//
// The caller must have run cfg.Validate() first.
func New(cfg *config.Config, log *slog.Logger) *Client {
	primary := goslack.New(cfg.PrimaryToken())

	// Reuse the primary client when the user token IS the primary token,
	// so we don't open two HTTP connection pools for the same credential.
	var user *goslack.Client
	switch {
	case cfg.HasBotToken() && cfg.HasUserToken():
		user = goslack.New(cfg.UserToken)
	case cfg.HasUserToken():
		user = primary
	}

	var bot *goslack.Client
	if cfg.HasBotToken() {
		bot = primary
	}

	c := &Client{cfg: cfg, log: log, bot: bot, user: user}

	c.Users = newUserService(primary, log)
	c.Channels = newChannelService(primary, c.Users, log)
	c.Messages = newMessageService(primary, c.Channels, c.Users, log)
	c.Search = newSearchService(c.searchAPI(), log)
	c.Unread = newUnreadService(user, c.Channels, c.Users, log)

	return c
}

// Config returns the configuration this client was built with.
func (c *Client) Config() *config.Config { return c.cfg }

// HasUserToken reports whether a user token is available.
func (c *Client) HasUserToken() bool { return c.user != nil }

// JoinedChannelNames returns the names of channels the active identity is
// a member of. Uses users.conversations when a user token is configured
// (the user's joined channels), otherwise falls back to ChannelService.List
// (channels the bot can see).
//
// Result is sorted by member count desc and capped at limit (0 = no cap).
// Archived channels are filtered out.
func (c *Client) JoinedChannelNames(ctx context.Context, limit int) ([]string, error) {
	var channels []goslack.Channel
	var err error

	if c.HasUserToken() {
		channels, err = c.Unread.JoinedChannels(ctx)
	} else {
		channels, err = c.Channels.List(ctx, 0)
	}
	if err != nil {
		return nil, err
	}

	sort.Slice(channels, func(i, j int) bool {
		return channels[i].NumMembers > channels[j].NumMembers
	})

	names := make([]string, 0, len(channels))
	for _, ch := range channels {
		if ch.IsArchived {
			continue
		}
		names = append(names, ch.Name)
	}
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}
	return names, nil
}

// searchAPI returns the API handle to use for search: user token if
// available (search.messages is gated on user tokens in newer Slack apps),
// falling back to the bot token otherwise.
func (c *Client) searchAPI() *goslack.Client {
	if c.user != nil {
		return c.user
	}
	return c.bot
}
