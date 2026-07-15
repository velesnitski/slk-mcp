package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// ErrNoUserTokenScheduled is returned when listing scheduled messages is
// attempted on a workspace with no user token. Scheduled messages are
// per-identity — to see YOUR queued messages the request must go out
// under your user token (xoxp-), not a bot's.
var ErrNoUserTokenScheduled = errors.New("listing scheduled messages requires a user token (xoxp-) for this workspace")

// ScheduledService lists the authenticated user's pending scheduled
// messages (chat.scheduledMessages.list) — the ones queued to send
// later. Built on the USER client so it returns the operator's own
// queue; api is nil for a bot-only workspace and List guards on that.
type ScheduledService struct {
	api *goslack.Client
	log *slog.Logger
}

func newScheduledService(user *goslack.Client, log *slog.Logger) *ScheduledService {
	return &ScheduledService{api: user, log: log}
}

// Enabled reports whether a user token backs this service.
func (s *ScheduledService) Enabled() bool { return s.api != nil }

// List returns every pending scheduled message for the authenticated
// user, following pagination to completion (newest Slack pages cap at
// 100). Order is Slack's; the renderer sorts by send time.
func (s *ScheduledService) List(ctx context.Context) ([]goslack.ScheduledMessage, error) {
	if s.api == nil {
		return nil, ErrNoUserTokenScheduled
	}
	var all []goslack.ScheduledMessage
	cursor := ""
	for {
		page, err := ratelimit.DoR(ctx, s.log, func() (struct {
			Msgs []goslack.ScheduledMessage
			Next string
		}, error) {
			msgs, next, err := s.api.GetScheduledMessagesContext(ctx, &goslack.GetScheduledMessagesParameters{
				Cursor: cursor,
				Limit:  100,
			})
			return struct {
				Msgs []goslack.ScheduledMessage
				Next string
			}{msgs, next}, err
		})
		if err != nil {
			return nil, fmt.Errorf("chat.scheduledMessages.list: %w", err)
		}
		all = append(all, page.Msgs...)
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	return all, nil
}
