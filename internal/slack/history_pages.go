package slack

import (
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
)

// historyPageTTL bounds how long a DM history page read by the unread
// sweep may stand in for a fresh read — a backstop to UnreadAll resetting
// the pages each sweep. The DM window follows the unread phase within the
// sweep's own deadline, so a fresh page is always well inside it.
const historyPageTTL = 2 * time.Minute

// historyPages remembers the newest-first conversations.history page the
// unread sweep read for each DM, so the DM window — which used to read
// every DM's newest page again a few seconds later, roughly a minute of
// rate-limited calls — can reuse it. See ADR 125.
//
// Reuse is exact up to the moment the page was read: both reads are
// anchored at now with no lower bound, so the first n messages of a page
// requested with a limit of at least n are the page a request with limit
// n returns. A message arriving after the page was read is newer than
// the sweep's start, and the unread sweep caps its delta cursor at that
// start, so the next pull shows it. get_mentions has no such cursor and
// never reuses a page.
type historyPages struct {
	mu   sync.Mutex
	now  func() time.Time // seam for tests; nil means time.Now
	m    map[string]historyPage
	lent int // pages lent since the last reset, for the sweep log
}

type historyPage struct {
	msgs  []goslack.Message
	limit int
	at    time.Time
}

func (p *historyPages) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// put records the page read for channelID with the given request limit.
func (p *historyPages) put(channelID string, msgs []goslack.Message, limit int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = make(map[string]historyPage)
	}
	p.m[channelID] = historyPage{msgs: msgs, limit: limit, at: p.clock()}
}

// reset forgets every page, so a page from an earlier sweep is never lent.
func (p *historyPages) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m = nil
	p.lent = 0
}

// lentCount reports how many pages were lent since the last reset.
func (p *historyPages) lentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lent
}

// get returns the first limit messages of a fresh page for channelID read
// with a limit of at least limit. A hit is consumed: the next sweep reads
// again rather than reusing a page from the one before.
func (p *historyPages) get(channelID string, limit int) ([]goslack.Message, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg, ok := p.m[channelID]
	if !ok {
		return nil, false
	}
	delete(p.m, channelID)
	if pg.limit < limit || p.clock().Sub(pg.at) > historyPageTTL {
		return nil, false
	}
	p.lent++
	if len(pg.msgs) > limit {
		return pg.msgs[:limit], true
	}
	return pg.msgs, true
}
