package slack

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// userResolveRetryAfter is how long a failed users.info lookup is
// remembered before the ID is tried again. A failure is usually
// transient — a rate-limit pause, a dropped connection — and caching it
// permanently means one bad moment renders that person as a raw ID for
// the rest of the process's life, on every surface, with no way to
// recover short of a restart. Long enough to stop a hot loop from
// hammering the API, short enough that a sweep a minute later is clean.
// See ADR 093.
const userResolveRetryAfter = time.Minute

// UserService resolves Slack user IDs to display names with an in-memory cache.
//
// Successful resolutions are cached for the life of the process — display
// names change rarely and a stale name is harmless. Failures are cached
// separately and briefly: see userResolveRetryAfter.
type UserService struct {
	api    *goslack.Client
	log    *slog.Logger
	mu     sync.RWMutex
	cache  map[string]string
	failed map[string]time.Time
	now    func() time.Time
}

func newUserService(api *goslack.Client, log *slog.Logger) *UserService {
	return &UserService{
		api:    api,
		log:    log,
		cache:  make(map[string]string),
		failed: make(map[string]time.Time),
		now:    time.Now,
	}
}

// Name returns a display name for the given user ID, resolving via
// the Slack API on cache miss. On any error the ID itself is returned
// so callers can render output without a branch — but the failure is
// only remembered for userResolveRetryAfter, so a transient error does
// not pin that person to a raw ID for the rest of the session.
func (s *UserService) Name(ctx context.Context, userID string) string {
	if userID == "" {
		return ""
	}
	s.mu.RLock()
	n, cached := s.cache[userID]
	failedAt, everFailed := s.failed[userID]
	s.mu.RUnlock()
	if cached {
		return n
	}
	if everFailed && s.now().Sub(failedAt) < userResolveRetryAfter {
		return userID
	}

	user, err := ratelimit.DoR(ctx, s.log, func() (*goslack.User, error) {
		return s.api.GetUserInfoContext(ctx, userID)
	})
	if err != nil {
		s.log.Debug("resolve user failed", "user_id", userID, "err", err)
		s.mu.Lock()
		s.failed[userID] = s.now()
		s.mu.Unlock()
		return userID
	}

	name := formatUserDisplay(user, userID)
	s.mu.Lock()
	s.cache[userID] = name
	delete(s.failed, userID)
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

// List returns all non-deleted workspace users. Pagination is
// handled internally; one users.list call per 200-user page.
func (s *UserService) List(ctx context.Context) ([]goslack.User, error) {
	us, err := ratelimit.DoR(ctx, s.log, func() ([]goslack.User, error) {
		return s.api.GetUsersContext(ctx, goslack.GetUsersOptionLimit(200))
	})
	if err != nil {
		return nil, err
	}
	out := us[:0]
	for _, u := range us {
		if u.Deleted {
			continue
		}
		out = append(out, u)
	}
	return out, nil
}

// IDForHandle resolves a @handle (or bare username / display name) to a
// user ID by scanning the workspace roster. The leading '@' is
// optional. Matching is case-insensitive and tries, in order: exact
// handle (Name), display name, then real name — the first exact match
// wins. Returns an error naming the unresolved handle when nothing
// matches, so the caller can surface it.
func (s *UserService) IDForHandle(ctx context.Context, handle string) (string, error) {
	if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(handle), "@")) == "" {
		return "", fmt.Errorf("empty handle")
	}
	users, err := s.List(ctx)
	if err != nil {
		return "", err
	}
	if id, ok := matchHandle(users, handle); ok {
		return id, nil
	}
	return "", fmt.Errorf("no user matches handle %q", handle)
}

// matchHandle finds the user whose handle matches want (leading '@'
// optional, case-insensitive). Exact username (Name) wins over display
// name / real name, and username matches are preferred across the whole
// roster before falling back — so a display-name collision can't shadow
// the real @handle. Split out of IDForHandle for unit testing without a
// live roster fetch. Returns ("", false) when nothing matches.
func matchHandle(users []goslack.User, handle string) (string, bool) {
	want := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(handle), "@"))
	if want == "" {
		return "", false
	}
	for _, u := range users {
		if strings.ToLower(u.Name) == want {
			return u.ID, true
		}
	}
	for _, u := range users {
		if strings.ToLower(u.Profile.DisplayName) == want || strings.ToLower(u.RealName) == want {
			return u.ID, true
		}
	}
	return "", false
}

// formatUserDisplay renders a user as "Real Name (handle)" so the
// LLM can correlate Slack usernames with the human names. Falls
// through to whichever fields are present.
func formatUserDisplay(u *goslack.User, fallbackID string) string {
	real := u.RealName
	if real == "" {
		real = u.Profile.RealName
	}
	if real == "" {
		real = u.Profile.DisplayName
	}
	handle := u.Name
	if handle == "" {
		handle = u.Profile.DisplayName
	}

	switch {
	case real == "" && handle == "":
		return fallbackID
	case real == "":
		return handle
	case handle == "" || strings.EqualFold(handle, real):
		return real
	default:
		return real + " (" + handle + ")"
	}
}
