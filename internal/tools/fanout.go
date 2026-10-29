package tools

import (
	"fmt"
	"strings"
	"sync"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

// sweepTargets splits the workspaces a sweep covers into the ones it reads
// and the ones SLACK_SWEEP_EXCLUDE keeps out of it (ADR 122). Exclusion
// applies only when no workspace was named: asking for one by name always
// reaches it. Excluding every workspace would leave nothing to sweep, so
// in that case the setting is ignored. Unknown name → nil, nil.
func (h *Hub) sweepTargets(workspace string) (read, excluded []slack.Workspace) {
	all := h.workspaceTargets(workspace)
	if all == nil {
		return nil, nil
	}
	if strings.TrimSpace(workspace) != "" || len(h.cfg.SweepExclude) == 0 {
		return all, nil
	}
	skip := make(map[string]bool, len(h.cfg.SweepExclude))
	for _, name := range h.cfg.SweepExclude {
		skip[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for _, ws := range all {
		if skip[strings.ToLower(ws.Name)] {
			excluded = append(excluded, ws)
		} else {
			read = append(read, ws)
		}
	}
	if len(read) == 0 {
		return all, nil
	}
	return read, excluded
}

// selfTargets resolves the workspaces a you-global change (status,
// presence, DND) applies to. An empty name follows the sweep: workspaces
// SLACK_SWEEP_EXCLUDE keeps out of the operator's view are left alone,
// because an AFK the operator set for the workspaces they work in should
// not land in one they do not follow (ADR 130). "all" or "*" reaches every
// workspace; a label reaches that one. Unknown name → nil, nil.
func (h *Hub) selfTargets(workspace string) (apply, skipped []slack.Workspace) {
	switch strings.ToLower(strings.TrimSpace(workspace)) {
	case "all", "*":
		return h.registry, nil
	}
	return h.sweepTargets(workspace)
}

// selfSkippedLines reports the workspaces a you-global change left
// alone, so "set everywhere" is never assumed where it did not happen.
func (h *Hub) selfSkippedLines(skipped []slack.Workspace) []string {
	var lines []string
	for _, ws := range skipped {
		lines = append(lines, fmt.Sprintf("- unchanged%s: excluded by SLACK_SWEEP_EXCLUDE — pass workspace=%s or workspace=all", h.wsLabel(ws.Name), ws.Name))
	}
	return lines
}

// excludedNote names the workspaces a default sweep left out, so their
// absence is never mistaken for "all caught up".
func excludedNote(excluded []slack.Workspace) string {
	if len(excluded) == 0 {
		return ""
	}
	names := workspaceOrder(excluded)
	return "not swept by default (SLACK_SWEEP_EXCLUDE): " + strings.Join(names, ", ") +
		" — pass workspace=" + names[0] + " to read it"
}

// fanOut runs fn for every workspace concurrently and returns the results
// in registry order (ADR 122). Workspaces have separate tokens and rate
// limits, so a sweep that waited for each in turn spent its time queueing
// behind the slowest one for nothing. Results are indexed, not appended,
// so the rendered order — and the combined cursor — stay deterministic.
func fanOut[T any](targets []slack.Workspace, fn func(ws slack.Workspace) T) []T {
	out := make([]T, len(targets))
	var wg sync.WaitGroup
	for i, ws := range targets {
		wg.Add(1)
		go func(i int, ws slack.Workspace) {
			defer wg.Done()
			out[i] = fn(ws)
		}(i, ws)
	}
	wg.Wait()
	return out
}
