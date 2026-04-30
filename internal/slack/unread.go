package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// ErrNoUserToken is returned by UnreadService methods when the server
// was started without SLACK_USER_TOKEN.
var ErrNoUserToken = errors.New(
	"SLACK_USER_TOKEN is required for this tool. " +
		"Create a Slack App with user scopes and set SLACK_USER_TOKEN=xoxp-...",
)

// UnreadService reports unread messages and mentions for the authenticated user.
// All methods require a user token (xoxp-).
type UnreadService struct {
	api      *goslack.Client
	channels *ChannelService
	users    *UserService
	log      *slog.Logger

	selfMu sync.RWMutex
	selfID string
}

func newUnreadService(api *goslack.Client, channels *ChannelService, users *UserService, log *slog.Logger) *UnreadService {
	return &UnreadService{api: api, channels: channels, users: users, log: log}
}

// Self returns the authenticated user's Slack ID, calling auth.test on
// first use and caching the result for the lifetime of the service.
// Used by tools that highlight messages mentioning the operator.
func (s *UnreadService) Self(ctx context.Context) (string, error) {
	if !s.Enabled() {
		return "", ErrNoUserToken
	}

	s.selfMu.RLock()
	id := s.selfID
	s.selfMu.RUnlock()
	if id != "" {
		return id, nil
	}

	var resp *goslack.AuthTestResponse
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.AuthTestContext(ctx)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("auth.test: %w", err)
	}

	s.selfMu.Lock()
	s.selfID = resp.UserID
	s.selfMu.Unlock()
	return resp.UserID, nil
}

// Enabled reports whether a user token is available.
func (s *UnreadService) Enabled() bool { return s.api != nil }

// ChannelUnread summarizes the unread state of a single channel.
//
// Replies maps a thread parent's timestamp to replies posted after
// LastRead, exclusive of the parent itself. A parent with no new
// replies is absent from the map.
type ChannelUnread struct {
	Channel  goslack.Channel
	LastRead string
	Messages []goslack.Message
	Replies  map[string][]goslack.Message
}

// JoinedChannels returns channels the user is a member of, useful for
// building the full unread sweep without iterating the whole workspace.
func (s *UnreadService) JoinedChannels(ctx context.Context) ([]goslack.Channel, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}

	var all []goslack.Channel
	cursor := ""
	for {
		params := &goslack.GetConversationsForUserParameters{
			Types:           []string{"public_channel", "private_channel"},
			Limit:           200,
			Cursor:          cursor,
			ExcludeArchived: true,
		}
		var channels []goslack.Channel
		var next string
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			ch, cur, err := s.api.GetConversationsForUserContext(ctx, params)
			if err != nil {
				return err
			}
			channels = ch
			next = cur
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("users.conversations: %w", err)
		}
		all = append(all, channels...)
		if next == "" {
			break
		}
		cursor = next
	}
	return all, nil
}

// Unread pulls messages newer than the user's last_read for the given channel.
// Returns a ChannelUnread with Messages empty if fully caught up.
func (s *UnreadService) Unread(ctx context.Context, channelID string, maxMessages int) (*ChannelUnread, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	if maxMessages <= 0 {
		maxMessages = 50
	}

	var info *goslack.Channel
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.GetConversationInfoContext(ctx, &goslack.GetConversationInfoInput{
			ChannelID:     channelID,
			IncludeLocale: false,
		})
		if err != nil {
			return err
		}
		info = r
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.info: %w", err)
	}

	cu := &ChannelUnread{Channel: *info, LastRead: info.LastRead}
	if info.LastRead == "" || info.UnreadCount == 0 {
		return cu, nil
	}

	oldest, _ := strconv.ParseFloat(info.LastRead, 64)
	params := &goslack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Oldest:    info.LastRead,
		Limit:     maxMessages,
		Inclusive: false,
	}

	var resp *goslack.GetConversationHistoryResponse
	err = ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.GetConversationHistoryContext(ctx, params)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.history (unread): %w", err)
	}

	for _, msg := range resp.Messages {
		ts, _ := strconv.ParseFloat(msg.Timestamp, 64)
		if ts <= oldest {
			continue
		}
		cu.Messages = append(cu.Messages, msg)
	}

	if err := s.fetchReplies(ctx, channelID, oldest, cu); err != nil {
		// Replies are best-effort context; a failure here should not
		// block the rest of the digest. Log and continue.
		s.log.Warn("fetch thread replies failed", "channel", channelID, "err", err)
	}
	return cu, nil
}

// fetchReplies populates cu.Replies for every top-level message in
// cu.Messages that is itself a thread parent with reply_count > 0.
// Only replies strictly newer than oldest (the channel's last_read)
// are kept.
func (s *UnreadService) fetchReplies(ctx context.Context, channelID string, oldest float64, cu *ChannelUnread) error {
	for _, m := range cu.Messages {
		// A thread parent has thread_ts == ts and reply_count > 0.
		// A reply has thread_ts != ts; replies do not appear in
		// conversations.history so we never see them at this layer.
		if m.ThreadTimestamp == "" || m.ThreadTimestamp != m.Timestamp || m.ReplyCount == 0 {
			continue
		}

		var replies []goslack.Message
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			params := &goslack.GetConversationRepliesParameters{
				ChannelID: channelID,
				Timestamp: m.Timestamp,
				Oldest:    m.Timestamp,
				Inclusive: false,
				Limit:     100,
			}
			r, _, _, err := s.api.GetConversationRepliesContext(ctx, params)
			if err != nil {
				return err
			}
			replies = r
			return nil
		})
		if err != nil {
			return fmt.Errorf("conversations.replies %s: %w", m.Timestamp, err)
		}

		var newer []goslack.Message
		for _, r := range replies {
			ts, _ := strconv.ParseFloat(r.Timestamp, 64)
			if ts <= oldest || r.Timestamp == m.Timestamp {
				continue
			}
			newer = append(newer, r)
		}
		if len(newer) == 0 {
			continue
		}
		if cu.Replies == nil {
			cu.Replies = make(map[string][]goslack.Message)
		}
		cu.Replies[m.Timestamp] = newer
	}
	return nil
}

// UnreadAll returns unread state for all channels the user joined.
// Channels with zero unread are omitted from the result.
func (s *UnreadService) UnreadAll(ctx context.Context, maxPerChannel int) ([]*ChannelUnread, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	channels, err := s.JoinedChannels(ctx)
	if err != nil {
		return nil, err
	}

	// Fetch in parallel, capped to a modest concurrency to stay polite.
	const workers = 4
	type result struct {
		cu  *ChannelUnread
		err error
	}

	jobs := make(chan goslack.Channel, len(channels))
	results := make(chan result, len(channels))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ch := range jobs {
				// users.conversations does not populate unread_count;
				// Unread() fetches conversations.info and short-circuits
				// when the channel is actually caught up.
				cu, err := s.Unread(ctx, ch.ID, maxPerChannel)
				results <- result{cu, err}
			}
		}()
	}
	for _, ch := range channels {
		jobs <- ch
	}
	close(jobs)
	wg.Wait()
	close(results)

	var out []*ChannelUnread
	for r := range results {
		if r.err != nil {
			s.log.Warn("unread fetch failed", "err", r.err)
			continue
		}
		if r.cu != nil && (len(r.cu.Messages) > 0 || len(r.cu.Replies) > 0) {
			out = append(out, r.cu)
		}
	}
	return out, nil
}

// MarkRead marks the channel as read up to the given timestamp.
func (s *UnreadService) MarkRead(ctx context.Context, channelID, ts string) error {
	if !s.Enabled() {
		return ErrNoUserToken
	}
	return ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.MarkConversationContext(ctx, channelID, ts)
	})
}
