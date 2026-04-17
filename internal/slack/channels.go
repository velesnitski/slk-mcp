package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// ChannelService lists channels and resolves channel names to IDs.
type ChannelService struct {
	api   *goslack.Client
	users *UserService
	log   *slog.Logger

	mu    sync.RWMutex
	cache map[string]string // name -> id
}

func newChannelService(api *goslack.Client, users *UserService, log *slog.Logger) *ChannelService {
	return &ChannelService{api: api, users: users, log: log, cache: make(map[string]string)}
}

// ResolveID converts a channel name (with or without leading #) to a channel ID.
// Caches all channels encountered during the lookup.
func (s *ChannelService) ResolveID(ctx context.Context, name string) (string, error) {
	name = strings.TrimPrefix(name, "#")
	if name == "" {
		return "", fmt.Errorf("empty channel name")
	}

	s.mu.RLock()
	if id, ok := s.cache[name]; ok {
		s.mu.RUnlock()
		return id, nil
	}
	s.mu.RUnlock()

	cursor := ""
	for {
		params := &goslack.GetConversationsParameters{
			Types:           []string{"public_channel", "private_channel"},
			Limit:           200,
			Cursor:          cursor,
			ExcludeArchived: true,
		}
		var channels []goslack.Channel
		var next string
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			ch, cur, err := s.api.GetConversationsContext(ctx, params)
			if err != nil {
				return err
			}
			channels = ch
			next = cur
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("list channels: %w", err)
		}

		s.mu.Lock()
		for _, ch := range channels {
			s.cache[ch.Name] = ch.ID
		}
		s.mu.Unlock()

		for _, ch := range channels {
			if ch.Name == name {
				return ch.ID, nil
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	return "", fmt.Errorf("channel #%s not found", name)
}

// List returns channels the bot has access to, up to limit.
func (s *ChannelService) List(ctx context.Context, limit int) ([]goslack.Channel, error) {
	if limit <= 0 {
		limit = 100
	}
	var all []goslack.Channel
	cursor := ""
	for {
		params := &goslack.GetConversationsParameters{
			Types:           []string{"public_channel", "private_channel"},
			Limit:           200,
			Cursor:          cursor,
			ExcludeArchived: true,
		}
		var channels []goslack.Channel
		var next string
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			ch, cur, err := s.api.GetConversationsContext(ctx, params)
			if err != nil {
				return err
			}
			channels = ch
			next = cur
			return nil
		})
		if err != nil {
			return nil, err
		}

		s.mu.Lock()
		for _, ch := range channels {
			s.cache[ch.Name] = ch.ID
		}
		s.mu.Unlock()

		all = append(all, channels...)
		if next == "" || len(all) >= limit {
			break
		}
		cursor = next
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// Info returns full channel metadata for a given channel ID.
func (s *ChannelService) Info(ctx context.Context, channelID string) (*goslack.Channel, error) {
	var ch *goslack.Channel
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		result, err := s.api.GetConversationInfoContext(ctx, &goslack.GetConversationInfoInput{
			ChannelID: channelID,
		})
		if err != nil {
			return err
		}
		ch = result
		return nil
	})
	return ch, err
}
