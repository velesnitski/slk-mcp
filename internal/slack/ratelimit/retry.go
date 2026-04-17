// Package ratelimit implements retry-with-backoff for Slack API calls.
//
// Slack returns 429 with a Retry-After header (seconds). slack-go
// surfaces this as slack.RateLimitedError. This helper wraps any
// API call and retries with the wait the server asks for.
package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/slack-go/slack"
)

// DefaultMaxRetries is the retry ceiling for Do.
const DefaultMaxRetries = 5

// Do invokes fn, retrying on slack.RateLimitedError up to maxRetries times.
// Respects context cancellation and the Retry-After duration Slack returns.
// If maxRetries <= 0, DefaultMaxRetries is used.
func Do(ctx context.Context, log *slog.Logger, maxRetries int, fn func() error) error {
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		var rle *slack.RateLimitedError
		if !errors.As(lastErr, &rle) {
			return lastErr
		}

		wait := rle.RetryAfter
		if wait <= 0 {
			wait = time.Duration(1<<attempt) * time.Second
		}
		log.Warn("slack rate-limited, retrying",
			"attempt", attempt+1,
			"max", maxRetries,
			"wait", wait.String(),
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
	return lastErr
}
