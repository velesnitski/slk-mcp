package config

import "testing"

// ADR 121: SLACK_SWEEP_TIMEOUT — default 100, 0 disables, junk falls back.

func TestLoad_SweepTimeout(t *testing.T) {
	for in, want := range map[string]int{"": 100, "45": 45, "0": 0, " 30 ": 30, "-5": 100, "soon": 100} {
		t.Setenv("SLACK_USER_TOKEN", "xox"+"p-"+"test")
		t.Setenv("SLACK_WORKSPACES", "")
		t.Setenv("SLACK_SWEEP_TIMEOUT", in)
		if got := Load().SweepTimeoutSeconds; got != want {
			t.Errorf("SLACK_SWEEP_TIMEOUT=%q → %d, want %d", in, got, want)
		}
	}
}

func TestLoad_SweepExclude(t *testing.T) {
	t.Setenv("SLACK_USER_TOKEN", "xox"+"p-"+"test")
	t.Setenv("SLACK_WORKSPACES", "")
	t.Setenv("SLACK_SWEEP_EXCLUDE", " third , other ")
	got := Load().SweepExclude
	if len(got) != 2 || got[0] != "third" || got[1] != "other" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("SLACK_SWEEP_EXCLUDE", "")
	if Load().SweepExclude != nil {
		t.Fatal("unset must mean no exclusion")
	}
}
