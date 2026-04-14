package slack

import (
	"fmt"
	"strings"
	"time"

	"github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
)

type Client struct {
	api           *slack.Client
	cfg           *config.Config
	channelCache  map[string]string // name -> id
	userCache     map[string]string // id -> display name
}

func NewClient(cfg *config.Config) *Client {
	return &Client{
		api:          slack.New(cfg.Token),
		cfg:          cfg,
		channelCache: make(map[string]string),
		userCache:    make(map[string]string),
	}
}

func (c *Client) ResolveChannelID(name string) (string, error) {
	name = strings.TrimPrefix(name, "#")
	if id, ok := c.channelCache[name]; ok {
		return id, nil
	}

	cursor := ""
	for {
		params := &slack.GetConversationsParameters{
			Types:  []string{"public_channel", "private_channel"},
			Limit:  200,
			Cursor: cursor,
		}
		channels, nextCursor, err := c.api.GetConversations(params)
		if err != nil {
			return "", fmt.Errorf("list channels: %w", err)
		}
		for _, ch := range channels {
			c.channelCache[ch.Name] = ch.ID
			if ch.Name == name {
				return ch.ID, nil
			}
		}
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	return "", fmt.Errorf("channel #%s not found", name)
}

func (c *Client) ResolveUserName(userID string) string {
	if name, ok := c.userCache[userID]; ok {
		return name
	}
	user, err := c.api.GetUserInfo(userID)
	if err != nil {
		c.userCache[userID] = userID
		return userID
	}
	name := user.RealName
	if name == "" {
		name = user.Profile.DisplayName
	}
	if name == "" {
		name = user.Name
	}
	c.userCache[userID] = name
	return name
}

func (c *Client) GetChannelHistory(channelID string, oldest time.Time, limit int) ([]slack.Message, error) {
	params := &slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Oldest:    fmt.Sprintf("%d", oldest.Unix()),
		Limit:     limit,
	}
	resp, err := c.api.GetConversationHistory(params)
	if err != nil {
		return nil, fmt.Errorf("channel history: %w", err)
	}
	return resp.Messages, nil
}

func (c *Client) GetThreadReplies(channelID, threadTS string) ([]slack.Message, error) {
	params := &slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     200,
	}
	msgs, _, _, err := c.api.GetConversationReplies(params)
	if err != nil {
		return nil, fmt.Errorf("thread replies: %w", err)
	}
	return msgs, nil
}

func (c *Client) SearchMessages(query string, count int) ([]slack.SearchMessage, error) {
	params := slack.SearchParameters{
		Sort:          "timestamp",
		SortDirection: "desc",
		Count:         count,
	}
	msgs, err := c.api.SearchMessages(query, params)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return msgs.Matches, nil
}

func (c *Client) PostMessage(channelID, text, threadTS string) (string, error) {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, ts, err := c.api.PostMessage(channelID, opts...)
	return ts, err
}

func (c *Client) AddReaction(channelID, timestamp, emoji string) error {
	return c.api.AddReaction(emoji, slack.ItemRef{
		Channel:   channelID,
		Timestamp: timestamp,
	})
}

func (c *Client) ListChannels(limit int) ([]slack.Channel, error) {
	var all []slack.Channel
	cursor := ""
	for {
		params := &slack.GetConversationsParameters{
			Types:  []string{"public_channel", "private_channel"},
			Limit:  200,
			Cursor: cursor,
		}
		channels, nextCursor, err := c.api.GetConversations(params)
		if err != nil {
			return nil, err
		}
		all = append(all, channels...)
		if nextCursor == "" || len(all) >= limit {
			break
		}
		cursor = nextCursor
	}
	return all, nil
}

func (c *Client) GetChannelInfo(channelID string) (*slack.Channel, error) {
	return c.api.GetConversationInfo(&slack.GetConversationInfoInput{
		ChannelID: channelID,
	})
}

func (c *Client) Config() *config.Config {
	return c.cfg
}
