package slack

import (
	"context"
	"fmt"
	"strconv"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// readStatePage is how many of a conversation's newest messages ReadState
// counts; beyond it the count is a lower bound.
const readStatePage = 100

// ReadState is how far the operator has read one conversation, as Slack
// records it for the user token — the bot's marker is a different one and
// says nothing about what the operator saw (ADR 129).
type ReadState struct {
	LastRead string           // Slack ts of the read marker; "" when Slack reports none
	Newest   *goslack.Message // newest top-level message; nil for an empty conversation
	Unread   int              // messages by others after LastRead, on the newest page
	Capped   bool             // the page was full, so Unread is a lower bound
}

// ReadState reads the operator's read marker and counts what came after
// it. Thread replies are tracked separately by Slack and are not counted.
func (s *UnreadService) ReadState(ctx context.Context, channelID string) (ReadState, error) {
	var rs ReadState
	if !s.Enabled() {
		return rs, ErrNoUserToken
	}
	info, err := ratelimit.DoR(ctx, s.log, func() (*goslack.Channel, error) {
		return s.api.GetConversationInfoContext(ctx, &goslack.GetConversationInfoInput{ChannelID: channelID})
	})
	if err != nil {
		return rs, fmt.Errorf("conversations.info: %w", err)
	}
	rs.LastRead = info.LastRead
	resp, err := ratelimit.DoR(ctx, s.log, func() (*goslack.GetConversationHistoryResponse, error) {
		return s.api.GetConversationHistoryContext(ctx, &goslack.GetConversationHistoryParameters{
			ChannelID: channelID, Limit: readStatePage,
		})
	})
	if err != nil {
		return rs, fmt.Errorf("conversations.history: %w", err)
	}
	selfID, _ := s.Self(ctx)
	rs.Newest, rs.Unread = countUnread(resp.Messages, rs.LastRead, selfID)
	rs.Capped = len(resp.Messages) >= readStatePage && rs.Unread > 0
	return rs, nil
}

// countUnread returns the newest message of a newest-first page and how
// many messages by others are newer than lastRead. The operator's own
// messages are never unread to them, as in the Slack client. With no
// marker nothing is counted: unknown is not the same as unread. Pure.
func countUnread(page []goslack.Message, lastRead, selfID string) (*goslack.Message, int) {
	if len(page) == 0 {
		return nil, 0
	}
	newest := page[0]
	mark, err := strconv.ParseFloat(lastRead, 64)
	if err != nil {
		return &newest, 0
	}
	n := 0
	for _, m := range page {
		ts, err := strconv.ParseFloat(m.Timestamp, 64)
		if err != nil || ts <= mark {
			continue
		}
		if selfID != "" && m.User == selfID {
			continue
		}
		n++
	}
	return &newest, n
}
