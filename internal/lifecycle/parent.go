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
	if interval <= 0 {
		interval = DefaultParentPollInterval
	}
	initial := getPpid()
	if log != nil {
		log.Debug("watching parent process", "ppid", initial, "interval", interval)
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
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
