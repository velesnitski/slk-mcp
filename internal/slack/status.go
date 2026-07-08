package slack

import (
	"context"
	"errors"
	"log/slog"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// ErrNoUserTokenStatus is returned when a status/presence write is
// attempted on a workspace that has no user token. Custom status and
// presence are PERSONAL actions (users.profile.set / users.setPresence)
// — a bot token cannot set a human's status, so these require xoxp-.
var ErrNoUserTokenStatus = errors.New("setting status/presence requires a user token (xoxp-) for this workspace")

// StatusService sets the authenticated user's custom status and
// presence. It is built on the USER client (never the bot client);
// api is nil when the workspace has no user token, and every method
// guards on that so a bot-only workspace fails loudly instead of
// silently no-op'ing.
type StatusService struct {
	api *goslack.Client
	log *slog.Logger
}

func newStatusService(user *goslack.Client, log *slog.Logger) *StatusService {
	return &StatusService{api: user, log: log}
}

// Enabled reports whether a user token backs this service.
func (s *StatusService) Enabled() bool { return s.api != nil }

// SetCustomStatus sets (or clears) the user's custom status. Both text
// and emoji empty clears it. expiration is a Unix timestamp; 0 means
// no expiry. Slack auto-fills a :speech_balloon: emoji when text is set
// but emoji is empty — the handler passes a sensible default instead so
// the status the operator sees matches what they asked for.
func (s *StatusService) SetCustomStatus(ctx context.Context, text, emoji string, expiration int64) error {
	if s.api == nil {
		return ErrNoUserTokenStatus
	}
	return ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.SetUserCustomStatusContext(ctx, text, emoji, expiration)
	})
}

// SetPresence forces presence to "away" (true) or restores automatic
// presence (false). Slack only accepts these two values on
// users.setPresence; "auto" lets Slack flip to away on idle again.
func (s *StatusService) SetPresence(ctx context.Context, away bool) error {
	if s.api == nil {
		return ErrNoUserTokenStatus
	}
	presence := "auto"
	if away {
		presence = "away"
	}
	return ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.SetUserPresenceContext(ctx, presence)
	})
}
