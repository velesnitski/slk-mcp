package format

import (
	"testing"
	"time"
)

// ADR 154: one clock setting for every rendered time.
func TestClock_12and24(t *testing.T) {
	t.Cleanup(func() { SetClock12(false) })
	at := time.Date(2026, 11, 26, 15, 4, 0, 0, time.UTC)
	if got := Clock(at); got != "15:04" {
		t.Errorf("24h = %q", got)
	}
	SetClock12(true)
	if got := Clock(at); got != "3:04 PM" {
		t.Errorf("12h = %q", got)
	}
	old := clockNow
	clockNow = func() time.Time { return at.Add(48 * time.Hour) }
	t.Cleanup(func() { clockNow = old })
	if got := ClockLabel(at); got != "26.11 3:04 PM" {
		t.Errorf("ClockLabel 12h other day = %q", got)
	}
	SetMonthFirst(true)
	t.Cleanup(func() { SetMonthFirst(false) })
	if got := ClockLabel(at); got != "11/26 3:04 PM" {
		t.Errorf("ClockLabel US = %q", got)
	}
}
