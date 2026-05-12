package slack

import (
	"context"
	"fmt"
	"log/slog"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// SearchService wraps Slack's search.messages endpoint.
//
// search.messages requires a user token for new Slack apps. If only a bot
// token is configured, this service will return whatever the bot token is
// allowed to see (usually nothing).
type SearchService struct {
	api *goslack.Client
	log *slog.Logger
}

func newSearchService(api *goslack.Client, log *slog.Logger) *SearchService {
	return &SearchService{api: api, log: log}
}

// Messages searches across the workspace using Slack search syntax.
// Supports: from:@user, in:#channel, has:link, before:/after:DATE, "phrase".
func (s *SearchService) Messages(ctx context.Context, query string, count int) ([]goslack.SearchMessage, error) {
	if count <= 0 {
		count = 20
	}
	params := goslack.SearchParameters{
		Sort:          "timestamp",
		SortDirection: "desc",
		Count:         count,
	}

	result, err := ratelimit.DoR(ctx, s.log, func() (*goslack.SearchMessages, error) {
		return s.api.SearchMessagesContext(ctx, query, params)
	})
	if err != nil {
		return nil, fmt.Errorf("search.messages: %w", err)
	}
	return result.Matches, nil
}
