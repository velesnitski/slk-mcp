package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	goslack "github.com/slack-go/slack"
)

// ChannelCount is one conversation's read state as client.counts reports it.
type ChannelCount struct {
	HasUnreads   bool
	MentionCount int
}

// ClientCounts fetches every conversation's unread state in one call.
//
// client.counts is the endpoint Slack's own clients use for the sidebar
// badges. It is not part of the documented Web API, may refuse a user
// (xoxp) token, and can change without notice — so it is used only when a
// sweep asks for it (unread_source=counts, ADR 131), and every caller
// falls back to the per-channel path when it fails.
func (s *CanvasService) ClientCounts(ctx context.Context) (map[string]ChannelCount, error) {
	if s.token == "" {
		return nil, ErrNoUserToken
	}
	form := url.Values{}
	form.Set("thread_counts_by_channel", "true")
	form.Set("org_wide_aware", "true")
	form.Set("include_file_channels", "true")

	endpoint := strings.TrimRight(s.BaseURL, "/") + "/client.counts"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("client.counts: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client.counts: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("client.counts: read body: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &goslack.RateLimitedError{RetryAfter: parseRetryAfterSeconds(resp.Header.Get("Retry-After"))}
	}
	return parseClientCounts(raw)
}

// parseClientCounts reads the channels, mpims and ims lists of a
// client.counts reply. Pure, for unit tests.
func parseClientCounts(raw []byte) (map[string]ChannelCount, error) {
	type entry struct {
		ID           string `json:"id"`
		HasUnreads   bool   `json:"has_unreads"`
		MentionCount int    `json:"mention_count"`
	}
	var env struct {
		OK       bool    `json:"ok"`
		Error    string  `json:"error"`
		Channels []entry `json:"channels"`
		MPIMs    []entry `json:"mpims"`
		IMs      []entry `json:"ims"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("client.counts: parse: %w (body=%q)", err, truncateRaw(raw, 200))
	}
	if !env.OK {
		if env.Error == "" {
			env.Error = "unknown_error"
		}
		return nil, fmt.Errorf("client.counts: %s", env.Error)
	}
	out := make(map[string]ChannelCount, len(env.Channels)+len(env.MPIMs)+len(env.IMs))
	for _, list := range [][]entry{env.Channels, env.MPIMs, env.IMs} {
		for _, e := range list {
			if e.ID != "" {
				out[e.ID] = ChannelCount{HasUnreads: e.HasUnreads, MentionCount: e.MentionCount}
			}
		}
	}
	return out, nil
}
