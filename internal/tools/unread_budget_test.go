package tools

import (
	"strings"
	"testing"
)

func TestBudgetAppend_unlimitedAlwaysWrites(t *testing.T) {
	var b strings.Builder
	if !budgetAppend(&b, "hello", 0) {
		t.Fatal("maxChars=0 must always emit")
	}
	if b.String() != "hello\n\n" {
		t.Fatalf("expected separator suffix, got %q", b.String())
	}
}

func TestBudgetAppend_underBudgetWrites(t *testing.T) {
	var b strings.Builder
	if !budgetAppend(&b, "abc", 100) {
		t.Fatal("under-budget write should emit")
	}
}

func TestBudgetAppend_overBudgetDrops(t *testing.T) {
	var b strings.Builder
	b.WriteString("xxxxx") // 5 chars already in buffer
	// "world" (5) + "\n\n" (2) + 5 already in buf = 12 chars
	// cap at 11 → must drop.
	if budgetAppend(&b, "world", 11) {
		t.Fatal("over-budget write must be dropped")
	}
	if b.String() != "xxxxx" {
		t.Fatalf("dropped write must not mutate buffer; got %q", b.String())
	}
}

func TestBudgetAppend_exactlyFitsWrites(t *testing.T) {
	var b strings.Builder
	// 5 chars + "\n\n" = 7. cap exactly 7 → fits.
	if !budgetAppend(&b, "hello", 7) {
		t.Fatal("exact-fit write should emit")
	}
}

func TestBudgetAppend_smallerLaterChannelStillFits(t *testing.T) {
	// Simulates the urgency-ordered loop: a large channel was rejected,
	// but a subsequent smaller one should still squeeze in. The handler
	// uses `continue` (not break) precisely so this works.
	var b strings.Builder
	b.WriteString("preamble-of-fixed-size")
	large := strings.Repeat("L", 100)
	small := "ok"
	if budgetAppend(&b, large, 40) {
		t.Fatal("large channel must be rejected at cap=40")
	}
	if !budgetAppend(&b, small, 40) {
		t.Fatal("small channel must fit after large was rejected")
	}
}
