package tools

import (
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/actions"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// ADR 152: dates print in the workspace's order.
func TestDayLabel_Order(t *testing.T) {
	d := time.Date(2026, 11, 27, 15, 4, 0, 0, time.UTC)
	if got := dayLabel(d, actions.DayFirst); got != "Fri 27.11" {
		t.Errorf("day-first = %q", got)
	}
	if got := dayLabel(d, actions.MonthFirst); got != "Fri 11/27" {
		t.Errorf("month-first = %q", got)
	}
	if got := stampLabel(d, actions.MonthFirst); got != "11/27 15:04" {
		t.Errorf("stamp month-first = %q", got)
	}
}

func TestDateOrderFor_WorkspaceOverride(t *testing.T) {
	h := &Hub{cfg: &config.Config{Workspaces: []config.WorkspaceConfig{
		{Name: "alpha"}, {Name: "beta", DateOrder: "en-US"},
	}}}
	if got := h.dateOrderFor("alpha", actions.DayFirst); got != actions.DayFirst {
		t.Errorf("alpha = %v, want inherited day-first", got)
	}
	if got := h.dateOrderFor("beta", actions.DayFirst); got != actions.MonthFirst {
		t.Errorf("beta = %v, want month-first", got)
	}
}
