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

// MessagesPages is Messages for up to pages pages of count hits each,
// newest first: a busy operator's own messages fill one page of 100 in a
// few days (ADR 133). It stops at the last page Slack reports, and a
// failure after the first page returns what was read with the error.
func (s *SearchService) MessagesPages(ctx context.Context, query string, count, pages int) ([]goslack.SearchMessage, error) {
	if count <= 0 {
		count = 20
	}
	if pages <= 0 {
		pages = 1
	}
	var out []goslack.SearchMessage
	for page := 1; page <= pages; page++ {
		params := goslack.SearchParameters{Sort: "timestamp", SortDirection: "desc", Count: count, Page: page}
		result, err := ratelimit.DoR(ctx, s.log, func() (*goslack.SearchMessages, error) {
			return s.api.SearchMessagesContext(ctx, query, params)
		})
		if err != nil {
			return out, fmt.Errorf("search.messages page %d: %w", page, err)
		}
		out = append(out, result.Matches...)
		if len(result.Matches) < count || page >= result.Paging.Pages {
			break
		}
	}
	return out, nil
}
