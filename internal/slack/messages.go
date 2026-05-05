package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// MessageService reads channel history, thread replies, and posts messages.
type MessageService struct {
	api      *goslack.Client
	channels *ChannelService
	users    *UserService
	log      *slog.Logger
}

func newMessageService(api *goslack.Client, channels *ChannelService, users *UserService, log *slog.Logger) *MessageService {
	return &MessageService{api: api, channels: channels, users: users, log: log}
}

// HistoryParams controls a conversations.history fetch.
type HistoryParams struct {
	ChannelID string
	OldestTS  float64 // unix seconds; 0 means no lower bound
	LatestTS  float64 // unix seconds; 0 means no upper bound
	Limit     int
}

// History returns messages between OldestTS and LatestTS, up to Limit,
// newest first.
func (s *MessageService) History(ctx context.Context, p HistoryParams) ([]goslack.Message, error) {
	if p.Limit <= 0 {
		p.Limit = 200
	}
	params := &goslack.GetConversationHistoryParameters{
		ChannelID: p.ChannelID,
		Limit:     p.Limit,
	}
	if p.OldestTS > 0 {
		params.Oldest = strconv.FormatFloat(p.OldestTS, 'f', 6, 64)
	}
	if p.LatestTS > 0 {
		params.Latest = strconv.FormatFloat(p.LatestTS, 'f', 6, 64)
		params.Inclusive = true
	}

	var resp *goslack.GetConversationHistoryResponse
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.GetConversationHistoryContext(ctx, params)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.history: %w", err)
	}
	return resp.Messages, nil
}

// ThreadReplies returns all messages in a thread rooted at threadTS.
func (s *MessageService) ThreadReplies(ctx context.Context, channelID, threadTS string) ([]goslack.Message, error) {
	params := &goslack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     200,
	}
	var msgs []goslack.Message
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		m, _, _, err := s.api.GetConversationRepliesContext(ctx, params)
		if err != nil {
			return err
		}
		msgs = m
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.replies: %w", err)
	}
	return msgs, nil
}

// Post sends a message to channelID. Pass threadTS to reply in a thread.
// Returns the posted message timestamp.
func (s *MessageService) Post(ctx context.Context, channelID, text, threadTS string) (string, error) {
	opts := []goslack.MsgOption{goslack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, goslack.MsgOptionTS(threadTS))
	}

	var ts string
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		_, t, err := s.api.PostMessageContext(ctx, channelID, opts...)
		if err != nil {
			return err
		}
		ts = t
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("chat.postMessage: %w", err)
	}
	return ts, nil
}

// AddReaction attaches an emoji reaction to a message.
func (s *MessageService) AddReaction(ctx context.Context, channelID, timestamp, emoji string) error {
	return ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.AddReactionContext(ctx, emoji, goslack.ItemRef{
			Channel:   channelID,
			Timestamp: timestamp,
		})
	})
}
