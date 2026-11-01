package slack

import (
	"context"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
)

// joinedTTL bounds how long a users.conversations listing is reused. One
// sweep lists the joined conversations up to three times — the unread
// pass, the DM window and the priority channels — and those phases now run
// at the same time (ADR 131). The TTL is short on purpose: it spans one
// sweep, so a DM opened by someone new is never hidden for longer than a
// minute. A variable so tests can expire it.
var joinedTTL = 60 * time.Second

// joinedCache shares one users.conversations listing between the phases of
// a sweep. Concurrent callers wait on the call in flight instead of paging
// the listing again; an error is never cached.
type joinedCache struct {
	mu       sync.Mutex
	list     []goslack.Channel
	at       time.Time
	inflight *joinedCall
}

type joinedCall struct {
	done chan struct{}
	list []goslack.Channel
	err  error
}

func (c *joinedCache) get(ctx context.Context, fetch func(context.Context) ([]goslack.Channel, error)) ([]goslack.Channel, error) {
	c.mu.Lock()
	if c.list != nil && time.Since(c.at) < joinedTTL {
		out := cloneChannels(c.list)
		c.mu.Unlock()
		return out, nil
	}
	if call := c.inflight; call != nil {
		c.mu.Unlock()
		select {
		case <-call.done:
			if call.err != nil {
				return nil, call.err
			}
			return cloneChannels(call.list), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &joinedCall{done: make(chan struct{})}
	c.inflight = call
	c.mu.Unlock()

	call.list, call.err = fetch(ctx)

	c.mu.Lock()
	c.inflight = nil
	if call.err == nil {
		c.list, c.at = call.list, time.Now()
	}
	c.mu.Unlock()
	close(call.done)

	if call.err != nil {
		return nil, call.err
	}
	return cloneChannels(call.list), nil
}

// cloneChannels hands each caller its own slice header, so one phase
// reordering or filtering its copy cannot disturb another's.
func cloneChannels(in []goslack.Channel) []goslack.Channel {
	return append([]goslack.Channel(nil), in...)
}
