package slack

import (
	"context"
	"sync"

	goslack "github.com/slack-go/slack"
)

// historyPages shares the newest-first conversations.history page of each
// DM between the two phases of one unread sweep that read it: the unread
// pass and the DM window. The DM window used to read every DM's newest
// page again after the unread pass, roughly a minute of rate-limited
// calls (ADR 125); it then had to wait for the whole unread pass, so a
// sweep the deadline cut there had no DM window at all. Now the phases
// run side by side and whichever asks first reads the page; the other
// waits for it (ADR 132).
//
// Reuse is exact up to the moment the page was read: both reads are
// anchored at now with no lower bound, so the first n messages of a page
// requested with a limit of at least n are the page a request with limit
// n returns. A message arriving after the page was read is newer than
// the sweep's start, and the unread sweep caps its delta cursor at that
// start, so the next pull shows it.
//
// The pages live in the sweep's context (WithSweepPages), so they cannot
// outlive the sweep or leak into a caller without that cursor cap —
// get_mentions has none and never reuses a page.
type historyPages struct {
	mu   sync.Mutex
	m    map[string]*historyPage
	lent int // pages served to a second reader, for the sweep log
}

type historyPage struct {
	done  chan struct{} // closed when msgs and err are set
	limit int
	msgs  []goslack.Message
	err   error
}

type sweepPagesKey struct{}

// WithSweepPages returns ctx carrying a fresh page cache for one unread
// sweep of one workspace.
func WithSweepPages(ctx context.Context) context.Context {
	return context.WithValue(ctx, sweepPagesKey{}, &historyPages{})
}

// pagesFrom returns the sweep's page cache, or nil outside a sweep.
func pagesFrom(ctx context.Context) *historyPages {
	p, _ := ctx.Value(sweepPagesKey{}).(*historyPages)
	return p
}

// load returns the first limit messages of the newest page of channelID,
// reading it with fetch(limit) unless another reader in the sweep has
// read, or is reading, a page at least that long. A nil cache always
// reads. A failed or too-short shared read is not reused: the caller
// reads for itself.
func (p *historyPages) load(ctx context.Context, channelID string, limit int, fetch func(limit int) ([]goslack.Message, error)) ([]goslack.Message, error) {
	if p == nil {
		return fetch(limit)
	}
	p.mu.Lock()
	if p.m == nil {
		p.m = make(map[string]*historyPage)
	}
	if pg, ok := p.m[channelID]; ok && pg.limit >= limit {
		p.mu.Unlock()
		select {
		case <-pg.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if pg.err == nil {
			p.mu.Lock()
			p.lent++
			p.mu.Unlock()
			return head(pg.msgs, limit), nil
		}
		return fetch(limit)
	}
	pg := &historyPage{done: make(chan struct{}), limit: limit}
	p.m[channelID] = pg
	p.mu.Unlock()

	pg.msgs, pg.err = fetch(limit)
	close(pg.done)
	return pg.msgs, pg.err
}

// lentCount reports how many pages were served to a second reader.
func (p *historyPages) lentCount() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lent
}

func head(msgs []goslack.Message, n int) []goslack.Message {
	if len(msgs) > n {
		return msgs[:n]
	}
	return msgs
}
