package tools

import (
	"testing"
)

func TestParseRange_hoursFallback(t *testing.T) {
	o, l, err := parseRange("", "", 24)
	if err != nil {
		t.Fatal(err)
	}
	if !l.IsZero() {
		t.Fatalf("latest must be zero when no before given, got %v", l)
	}
	if o.IsZero() {
		t.Fatal("oldest must be set from hours fallback")
	}
}

func TestParseRange_afterBefore(t *testing.T) {
	o, l, err := parseRange("2026-04-30", "2026-05-01", 0)
	if err != nil {
		t.Fatal(err)
	}
	if o.Format("2006-01-02") != "2026-04-30" {
		t.Fatalf("after wrong: %v", o)
	}
	// before is end-exclusive: 2026-05-01 + 24h = 2026-05-02 00:00 UTC
	if l.Format("2006-01-02") != "2026-05-02" {
		t.Fatalf("before-end-exclusive wrong: %v", l)
	}
}

func TestParseRange_invalidOrder(t *testing.T) {
	_, _, err := parseRange("2026-05-05", "2026-05-01", 0)
	if err == nil {
		t.Fatal("expected error when before <= after")
	}
}

func TestParseRange_badDate(t *testing.T) {
	_, _, err := parseRange("not-a-date", "", 0)
	if err == nil {
		t.Fatal("expected parse error")
	}
}
