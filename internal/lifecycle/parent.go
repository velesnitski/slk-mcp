// Package lifecycle contains helpers that govern the slk-mcp process'
// own lifecycle — distinct from the Slack client lifecycle.
//
// The stdio transport relies on its parent process (typically the MCP
// host: Claude Code, Cursor, etc.) to close stdin when the session ends.
// In practice some hosts disconnect without closing the pipe, leaving
// orphan children that linger across reconnects. WatchParent provides a
// last line of defence: when the original parent process dies, the
// kernel reparents us to PID 1 / launchd; the watcher detects the PPID
// change and triggers a clean shutdown.
//
// What WatchParent does NOT detect: a host that is alive but has stopped
// talking to us. That scenario requires a heartbeat-based timeout and
// is intentionally not implemented here — the false-positive risk on a
// long-idle but valid session is too high.
package lifecycle

import (
	"context"
	"log/slog"
	"time"
)

// DefaultParentPollInterval is the polling cadence used when callers
// pass interval <= 0 to WatchParent.
const DefaultParentPollInterval = 5 * time.Second

// tickerFunc produces a tick channel and a stop function for a given
// interval. It is the seam that makes WatchParent testable without
// real time: production uses newRealTicker (a wrapped time.Ticker);
// tests inject a manually-driven channel so the poll loop advances
// deterministically rather than racing the wall clock.
//
// This seam exists because earlier attempts to stabilise the
// parent-watcher tests by tuning real-time constants (ADR 012 raised
// the deadline 2s→5s, ADR 015 raised the poll interval 1ms→10ms) only
// lowered the flake probability — a real ticker under the race
// detector on a loaded CI runner can still stall past any fixed
// deadline. Driving ticks deterministically removes the flake class
// entirely. See ADR 019.
type tickerFunc func(time.Duration) (ticks <-chan time.Time, stop func())

// newRealTicker is the production tickerFunc: a real time.Ticker.
func newRealTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// WatchParent polls getPpid until the value changes, then calls onLost.
// Returns when the context is cancelled or onLost has been invoked.
//
// Pass os.Getppid as getPpid in production. The injection seam lets
// tests drive the watcher deterministically without forking a real
// child process.
//
// If interval <= 0, DefaultParentPollInterval is used.
func WatchParent(
	ctx context.Context,
	log *slog.Logger,
	getPpid func() int,
	interval time.Duration,
	onLost func(),
) {
	watchParent(ctx, log, getPpid, interval, onLost, newRealTicker)
}

// watchParent is the testable core. newTicker supplies the poll
// cadence; production passes newRealTicker, tests pass a controllable
// fake. Behaviour is identical to the public WatchParent.
func watchParent(
	ctx context.Context,
	log *slog.Logger,
	getPpid func() int,
	interval time.Duration,
	onLost func(),
	newTicker tickerFunc,
) {
	if interval <= 0 {
		interval = DefaultParentPollInterval
	}
	initial := getPpid()
	if log != nil {
		log.Debug("watching parent process", "ppid", initial, "interval", interval)
	}

	ticks, stop := newTicker(interval)
	defer stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			current := getPpid()
			if current != initial {
				if log != nil {
					log.Info("parent process changed; initiating shutdown",
						"initial_ppid", initial, "current_ppid", current)
				}
				onLost()
				return
			}
		}
	}
}
