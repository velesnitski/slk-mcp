package tools

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
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

	// Phases of one sweep may run concurrently (ADR 131); mu guards the
	// two lists they report into.
	mu   sync.Mutex
	cut  []string // phases whose data is missing: the result is partial
	thin []string // display-only phases cut short: nothing is missing
	took map[string]time.Duration
}

func newSweep(ctx context.Context, log *slog.Logger, tool, ws string) *sweep {
	return &sweep{ctx: ctx, log: log, tool: tool, ws: ws, start: time.Now()}
}

// run executes one phase under the fetch context. A phase that does not
// start because the deadline already passed, or that fails because it
// passed meanwhile, is recorded as cut; any other error is the phase's
// own and is returned untouched for the caller's existing fallback.
func (s *sweep) run(phase string, fn func(ctx context.Context) error) error {
	return s.runPhase(phase, false, fn)
}

// runDisplay is run for a phase that only refines what is shown — the
// answered-DM probe, the pending-reply check — so being cut short loses
// no messages and must not hold the delta cursor back.
func (s *sweep) runDisplay(phase string, fn func(ctx context.Context) error) error {
	return s.runPhase(phase, true, fn)
}

func (s *sweep) runPhase(phase string, display bool, fn func(ctx context.Context) error) error {
	if err := s.ctx.Err(); err != nil {
		s.record(phase, display)
		return err
	}
	t0 := time.Now()
	err := fn(s.ctx)
	took := time.Since(t0)
	s.mu.Lock()
	if s.took == nil {
		s.took = make(map[string]time.Duration)
	}
	s.took[phase] = took
	s.mu.Unlock()
	attrs := []any{"tool", s.tool, "workspace", s.ws, "phase", phase,
		"took", took.Round(time.Millisecond).String()}
	if err != nil {
		attrs = append(attrs, "err", err)
	}
	s.log.Info("sweep phase", attrs...)
	if err != nil && s.ctx.Err() != nil {
		s.record(phase, display)
	}
	return err
}

func (s *sweep) record(phase string, display bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if display {
		s.thin = append(s.thin, phase)
	} else {
		s.cut = append(s.cut, phase)
	}
}

// incomplete reports whether the deadline cut any phase.
func (s *sweep) incomplete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cut) > 0
}

// phaseRank orders cut phases the way the sweep reads them, so the note
// does not depend on which concurrent phase noticed the deadline first.
var phaseRank = map[string]int{
	"unread": 0, "dm-window": 1, "priority": 2, "thread-mentions": 3,
	"own-threads": 4, "canvases": 5, "answered-dms": 6, "dm-history": 7, "pending-check": 8,
}

func sortPhases(phases []string) []string {
	out := append([]string(nil), phases...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, iok := phaseRank[out[i]]
		rj, jok := phaseRank[out[j]]
		if iok != jok {
			return iok
		}
		return ri < rj
	})
	return out
}

// note is the line rendered into the workspace's body when a phase was
// cut. withCursor adds the cursor consequence for the unread sweep.
func (s *sweep) note(withCursor bool) string {
	s.mu.Lock()
	cut, thin := sortPhases(s.cut), sortPhases(s.thin)
	s.mu.Unlock()
	var b strings.Builder
	took := time.Since(s.start).Round(time.Second)
	if len(cut) > 0 {
		fmt.Fprintf(&b, "incomplete: the %s deadline passed after %s — not finished: %s",
			s.tool, took, strings.Join(cut, ", "))
		if withCursor {
			b.WriteString("; cursor not advanced for this workspace, so the next pull re-reads them")
		}
		b.WriteString("\n")
	}
	if len(thin) > 0 {
		fmt.Fprintf(&b, "deadline cut short: %s — results are complete but less filtered\n", strings.Join(thin, ", "))
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

// timings renders how long the sweep and each phase took, for the
// show_timings option (ADR 131). Phases run concurrently, so the total is
// the wall time, not the sum of the phases.
func (s *sweep) timings() string {
	s.mu.Lock()
	phases := make([]string, 0, len(s.took))
	for ph := range s.took {
		phases = append(phases, ph)
	}
	took := make(map[string]time.Duration, len(s.took))
	for k, v := range s.took {
		took[k] = v
	}
	s.mu.Unlock()
	phases = sortPhases(phases)
	parts := make([]string, 0, len(phases))
	for _, ph := range phases {
		parts = append(parts, fmt.Sprintf("%s %s", ph, took[ph].Round(10*time.Millisecond)))
	}
	return fmt.Sprintf("timings: %s total · %s\n", time.Since(s.start).Round(10*time.Millisecond), strings.Join(parts, " · "))
}
