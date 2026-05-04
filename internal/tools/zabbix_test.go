package tools

import (
	"strings"
	"testing"
)

const sampleZabbixAlert = `Resolved in 56s: Load average is too high (per CPU load over 2 for 5m)
Host
api-host-1.example.test [api-host-1.example.test]
Event time
2026.05.04 13:49:08
Severity
Average
Opdata
Load averages(1m 5m 15m): (3.35 7.77 5.47), # of CPUs: 2
Event tags
Application: CPU
Trigger description
Per CPU load average is too high. Your system may be slow to respond.`

const sampleZabbixDisk = `Problem: /: Disk space is low (used > 80%)
Host
api-host-2.example.test [api-host-2.example.test]
Event time
2026.05.04 13:53:58
Severity
Warning
Opdata
Space used: 47.97 GB of 57.97 GB (82.77 %)
Event tags
Application: Filesystem /
Trigger description
Two conditions should match: First, space utilization should be above 80.`

func TestParseZabbixAlert_LoadAverage(t *testing.T) {
	a := parseZabbixAlert(sampleZabbixAlert)
	if a == nil {
		t.Fatal("expected non-nil ZabbixAlert")
	}
	if !strings.HasPrefix(a.State, "Resolved") {
		t.Errorf("State = %q; want Resolved...", a.State)
	}
	if !strings.Contains(a.Trigger, "Load average is too high") {
		t.Errorf("Trigger = %q; want load-average phrase", a.Trigger)
	}
	if a.Host != "api-host-1.example.test" {
		t.Errorf("Host = %q; want api-host-1.example.test (no bracket dup)", a.Host)
	}
	if a.Severity != "Average" {
		t.Errorf("Severity = %q; want Average", a.Severity)
	}
	if a.Opdata == "" {
		t.Errorf("Opdata empty")
	}
}

func TestParseZabbixAlert_Disk(t *testing.T) {
	a := parseZabbixAlert(sampleZabbixDisk)
	if a == nil {
		t.Fatal("expected non-nil ZabbixAlert")
	}
	if a.State != "Problem" {
		t.Errorf("State = %q; want Problem", a.State)
	}
	if a.Severity != "Warning" {
		t.Errorf("Severity = %q; want Warning", a.Severity)
	}
}

func TestParseZabbixAlert_NotZabbix(t *testing.T) {
	cases := []string{
		"Just a regular slack message",
		"Host downloaded the file", // contains "Host" but not Severity
		"Severity blue line",       // contains "Severity" but not Host
		"",
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			if a := parseZabbixAlert(c); a != nil {
				t.Fatalf("expected nil for %q, got %+v", c, a)
			}
		})
	}
}

func TestZabbixAlert_OneLine_LoadAverage(t *testing.T) {
	a := parseZabbixAlert(sampleZabbixAlert)
	out := a.OneLine()
	for _, want := range []string{
		"Resolved",
		"api-host-1.example.test",
		"Load average",
		"sev Average",
		"load5=7.77",
		"CPUs=2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("OneLine missing %q in: %s", want, out)
		}
	}
}

func TestZabbixAlert_OneLine_Disk(t *testing.T) {
	a := parseZabbixAlert(sampleZabbixDisk)
	out := a.OneLine()
	for _, want := range []string{
		"Problem",
		"api-host-2.example.test",
		"Disk space",
		"sev Warning",
		"82.77 %",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("OneLine missing %q in: %s", want, out)
		}
	}
}

func TestCompactOpdata_Fallthrough(t *testing.T) {
	// Unknown opdata format passes through (truncated if very long).
	short := "MQ depth 14, retries 3"
	if got := compactOpdata(short); got != short {
		t.Fatalf("expected passthrough for short string, got %q", got)
	}
	long := strings.Repeat("x", 200)
	if got := compactOpdata(long); len(got) > 90 {
		t.Fatalf("long string should be truncated to ~80 chars, got len=%d", len(got))
	}
}
