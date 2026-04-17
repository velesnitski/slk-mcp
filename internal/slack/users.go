package slack

import (
	"context"
	"log/slog"
	"sync"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// UserService resolves Slack user IDs to display names with an in-memory cache.
type UserService struct {
	api   *goslack.Client
	log   *slog.Logger
	mu    sync.RWMutex
	cache map[string]string
}

func newUserService(api *goslack.Client, log *slog.Logger) *UserService {
	return &UserService{api: api, log: log, cache: make(map[string]string)}
}

// Name returns a display name for the given user ID, resolving via
// the Slack API on cache miss. On any error the ID itself is returned
// so callers can render output without a branch.
func (s *UserService) Name(ctx context.Context, userID string) string {
	if userID == "" {
		return ""
	}
	s.mu.RLock()
	if n, ok := s.cache[userID]; ok {
		s.mu.RUnlock()
		return n
	}
	s.mu.RUnlock()

	var user *goslack.User
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		u, err := s.api.GetUserInfoContext(ctx, userID)
		if err != nil {
			return err
		}
		user = u
		return nil
	})
	if err != nil {
		s.log.Debug("resolve user failed", "user_id", userID, "err", err)
		s.mu.Lock()
		s.cache[userID] = userID
		s.mu.Unlock()
		return userID
	}

	name := user.RealName
	if name == "" {
		name = user.Profile.DisplayName
	}
	if name == "" {
		name = user.Name
	}
	if name == "" {
		name = userID
	}
	s.mu.Lock()
	s.cache[userID] = name
	s.mu.Unlock()
	return name
}

// NamesFor resolves many user IDs in one call, deduplicating and
// populating the cache for all of them.
func (s *UserService) NamesFor(ctx context.Context, userIDs []string) map[string]string {
	out := make(map[string]string, len(userIDs))
	for _, id := range userIDs {
		if _, ok := out[id]; ok {
			continue
		}
		out[id] = s.Name(ctx, id)
	}
	return out
}
