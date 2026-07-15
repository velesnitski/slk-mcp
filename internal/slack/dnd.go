package slack

import (
	"context"
	"errors"
	"log/slog"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// ErrNoUserTokenDND is returned when a DND (snooze) write is attempted on
// a workspace with no user token. Pausing notifications is a PERSONAL
// action (dnd.setSnooze) — a bot token cannot snooze a human's
// notifications, so it requires xoxp-.
var ErrNoUserTokenDND = errors.New("pausing notifications requires a user token (xoxp-) for this workspace")

// DNDService snoozes / resumes the authenticated user's Do Not Disturb
// (the notification pause). Built on the USER client (never the bot
// client); api is nil when the workspace has no user token, and every
// method guards on that so a bot-only workspace fails loudly instead of
// silently no-op'ing. Mirrors StatusService — both are personal,
// user-token-only surfaces.
type DNDService struct {
	api *goslack.Client
	log *slog.Logger
}

func newDNDService(user *goslack.Client, log *slog.Logger) *DNDService {
	return &DNDService{api: user, log: log}
}

// Enabled reports whether a user token backs this service.
func (s *DNDService) Enabled() bool { return s.api != nil }

// Snooze pauses notifications (Do Not Disturb) for minutes. Slack begins
// a snooze session if none is active, or extends the existing one. The
// caller guarantees minutes > 0.
func (s *DNDService) Snooze(ctx context.Context, minutes int) error {
	if s.api == nil {
		return ErrNoUserTokenDND
	}
	return ratelimit.Do(ctx, s.log, 0, func() error {
		_, err := s.api.SetSnoozeContext(ctx, minutes)
		return err
	})
}

// EndSnooze ends the current snooze, resuming notifications immediately.
// A no-op on Slack's side when no snooze is active.
func (s *DNDService) EndSnooze(ctx context.Context) error {
	if s.api == nil {
		return ErrNoUserTokenDND
	}
	return ratelimit.Do(ctx, s.log, 0, func() error {
		_, err := s.api.EndSnoozeContext(ctx)
		return err
	})
}
