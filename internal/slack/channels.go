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

	mu      sync.RWMutex
	cache   map[string]string // name -> id
	idCache map[string]string // id -> name (reverse lookup for <#CID> resolution)
}

func newChannelService(api *goslack.Client, users *UserService, log *slog.Logger) *ChannelService {
	return &ChannelService{
		api:     api,
		users:   users,
		log:     log,
		cache:   make(map[string]string),
		idCache: make(map[string]string),
	}
}

// recordChannel populates both name→id and id→name caches under a
// single lock acquisition. Used by every code path that learns about
// a channel (List, ResolveID, Info, NamesForIDs).
func (s *ChannelService) recordChannel(name, id string) {
	if name == "" || id == "" {
		return
	}
	s.mu.Lock()
	s.cache[name] = id
	s.idCache[id] = name
	s.mu.Unlock()
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

		for _, ch := range channels {
			s.recordChannel(ch.Name, ch.ID)
		}

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

		for _, ch := range channels {
			s.recordChannel(ch.Name, ch.ID)
		}

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

// Info returns full channel metadata for a given channel ID. We always
// request num_members because Slack's conversations.info omits it by
// default and callers rely on it. Populates the name↔id cache as a
// side effect so subsequent NamesForIDs / ResolveID calls hit the
// cache.
func (s *ChannelService) Info(ctx context.Context, channelID string) (*goslack.Channel, error) {
	var ch *goslack.Channel
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		result, err := s.api.GetConversationInfoContext(ctx, &goslack.GetConversationInfoInput{
			ChannelID:         channelID,
			IncludeNumMembers: true,
		})
		if err != nil {
			return err
		}
		ch = result
		return nil
	})
	if err == nil && ch != nil {
		s.recordChannel(ch.Name, ch.ID)
	}
	return ch, err
}

// IsID reports whether a string looks like a Slack channel ID
// (`C…` public, `G…` private; `D…` DMs are intentionally excluded —
// callers asking for a "channel name" never mean a DM).
func IsChannelID(s string) bool {
	if len(s) < 8 || len(s) > 16 {
		return false
	}
	if s[0] != 'C' && s[0] != 'G' {
		return false
	}
	for _, r := range s[1:] {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// NamesForIDs resolves a batch of channel IDs to display names. Mirrors
// UserService.NamesFor: serves cached entries from idCache first, then
// falls back to one conversations.info call per remaining ID.
//
// Used by the digest / recap renderers to resolve `<#CID>` references
// embedded in message bodies. Failures (private channels the bot can't
// see, deleted channels) are silently skipped so the digest still
// renders with `#CID` placeholders.
func (s *ChannelService) NamesForIDs(ctx context.Context, channelIDs []string) map[string]string {
	if len(channelIDs) == 0 {
		return nil
	}
	out := make(map[string]string, len(channelIDs))

	var miss []string
	s.mu.RLock()
	for _, id := range channelIDs {
		if id == "" {
			continue
		}
		if name, ok := s.idCache[id]; ok {
			out[id] = name
			continue
		}
		miss = append(miss, id)
	}
	s.mu.RUnlock()

	for _, id := range miss {
		info, err := s.Info(ctx, id)
		if err != nil {
			s.log.Debug("resolve channel id failed", "channel_id", id, "err", err)
			continue
		}
		if info != nil && info.Name != "" {
			out[id] = info.Name
		}
	}
	return out
}

// Members returns up to limit user IDs in a channel. limit<=0 means all.
func (s *ChannelService) Members(ctx context.Context, channelID string, limit int) ([]string, error) {
	var all []string
	cursor := ""
	page := 200
	for {
		var ids []string
		var next string
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			users, cur, err := s.api.GetUsersInConversationContext(ctx, &goslack.GetUsersInConversationParameters{
				ChannelID: channelID,
				Cursor:    cursor,
				Limit:     page,
			})
			if err != nil {
				return err
			}
			ids = users
			next = cur
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("list members: %w", err)
		}
		all = append(all, ids...)
		if next == "" {
			break
		}
		if limit > 0 && len(all) >= limit {
			break
		}
		cursor = next
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
