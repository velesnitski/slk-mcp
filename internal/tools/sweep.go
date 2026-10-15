package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// sweep times the fetch phases of one workspace's get_unread_summary or
// get_mentions pass and records which of them a deadline cut short.
// See ADR 121.
//
// Both tools run several Slack-bound phases one after another. With no
// bound, a slow phase — usually a rate-limit wait — held the whole call
// for minutes, and nothing said which phase it was. Each phase is now
// logged with its duration, and once the deadline passes the remaining
// phases are skipped and named, so the caller gets what was gathered plus
// an honest account of what was not.
type sweep struct {
	ctx   context.Context // the deadline-bound fetch context
	log   *slog.Logger
	tool  string
	ws    string
	start time.Time
	cut   []string // phases whose data is missing: the result is partial
	thin  []string // display-only phases cut short: nothing is missing
}

func newSweep(ctx context.Context, log *slog.Logger, tool, ws string) *sweep {
	return &sweep{ctx: ctx, log: log, tool: tool, ws: ws, start: time.Now()}
}

// run executes one phase under the fetch context. A phase that does not
// start because the deadline already passed, or that fails because it
// passed meanwhile, is recorded as cut; any other error is the phase's
// own and is returned untouched for the caller's existing fallback.
func (s *sweep) run(phase string, fn func(ctx context.Context) error) error {
	if err := s.ctx.Err(); err != nil {
		s.cut = append(s.cut, phase)
		return err
	}
	t0 := time.Now()
	err := fn(s.ctx)
	attrs := []any{"tool", s.tool, "workspace", s.ws, "phase", phase,
		"took", time.Since(t0).Round(time.Millisecond).String()}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	s.log.Info("sweep phase", attrs...)
	if err != nil && s.ctx.Err() != nil {
		s.cut = append(s.cut, phase)
	}
	return err
}

// runDisplay is run for a phase that only refines what is shown — the
// answered-DM probe, the pending-reply check — so being cut short loses
// no messages and must not hold the delta cursor back.
func (s *sweep) runDisplay(phase string, fn func(ctx context.Context) error) error {
	before := len(s.cut)
	err := s.run(phase, fn)
	if len(s.cut) > before {
		s.cut = s.cut[:before]
		s.thin = append(s.thin, phase)
	}
	return err
}

// incomplete reports whether the deadline cut any phase.
func (s *sweep) incomplete() bool { return len(s.cut) > 0 }

// note is the line rendered into the workspace's body when a phase was
// cut. withCursor adds the cursor consequence for the unread sweep.
func (s *sweep) note(withCursor bool) string {
	var b strings.Builder
	took := time.Since(s.start).Round(time.Second)
	if s.incomplete() {
		fmt.Fprintf(&b, "incomplete: the %s deadline passed after %s — not finished: %s",
			s.tool, took, strings.Join(s.cut, ", "))
		if withCursor {
			b.WriteString("; cursor not advanced for this workspace, so the next pull re-reads them")
		}
		b.WriteString("\n")
	}
	if len(s.thin) > 0 {
		fmt.Fprintf(&b, "deadline cut short: %s — results are complete but less filtered\n", strings.Join(s.thin, ", "))
	}
	return b.String()
}

// sweepUnit is the length of one SweepTimeoutSeconds step; a variable so
// tests can run the deadline paths in milliseconds.
var sweepUnit = time.Second

// sweepContext returns the fetch context for a sweep: bounded by seconds
// when positive, otherwise the caller's context unchanged.
func sweepContext(ctx context.Context, seconds int) (context.Context, context.CancelFunc) {
	if seconds <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(seconds)*sweepUnit)
}
