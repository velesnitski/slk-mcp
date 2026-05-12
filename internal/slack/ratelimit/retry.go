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

// DoR is the generic companion to Do for API calls that return a
// value alongside their error. The classic pattern in slack service
// code is:
//
//	var resp *T
//	err := ratelimit.Do(ctx, log, 0, func() error {
//	    r, err := api.XContext(ctx, ...)
//	    if err != nil { return err }
//	    resp = r
//	    return nil
//	})
//
// — three lines of glue to shuttle the value out through a captured
// variable. DoR collapses that to one line:
//
//	resp, err := ratelimit.DoR(ctx, log, func() (*T, error) {
//	    return api.XContext(ctx, ...)
//	})
//
// Retry semantics are identical to Do; maxRetries uses the package
// default.
func DoR[T any](ctx context.Context, log *slog.Logger, fn func() (T, error)) (T, error) {
	var out T
	err := Do(ctx, log, 0, func() error {
		v, err := fn()
		if err != nil {
			return err
		}
		out = v
		return nil
	})
	return out, err
}

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
