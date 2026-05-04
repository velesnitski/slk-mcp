package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func shortMsg(user, text string) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = "1700000000.000000"
	m.User = user
	m.Text = text
	return m
}

func TestDetectLowSignalChannel_NameKeyword(t *testing.T) {
	cu := mkChannelUnread("team-checkin",
		[]goslack.Message{shortMsg("U1", "+")}, nil)
	if !detectLowSignalChannel(cu) {
		t.Fatal("checkin-named channel must be low-signal")
	}
}

func TestDetectLowSignalChannel_ShortMessages(t *testing.T) {
	cu := mkChannelUnread("anything",
		[]goslack.Message{
			shortMsg("U1", "+"),
			shortMsg("U2", "обед"),
			shortMsg("U3", "+"),
			shortMsg("U4", "Break"),
			shortMsg("U5", "пока"),
		}, nil)
	if !detectLowSignalChannel(cu) {
		t.Fatal("5 short messages with no replies must be low-signal")
	}
}

func TestDetectLowSignalChannel_LongMessagesNotLowSignal(t *testing.T) {
	long := strings.Repeat("x", 50)
	cu := mkChannelUnread("anything",
		[]goslack.Message{
			shortMsg("U1", long),
			shortMsg("U2", long),
			shortMsg("U3", long),
			shortMsg("U4", long),
			shortMsg("U5", long),
		}, nil)
	if detectLowSignalChannel(cu) {
		t.Fatal("long-body messages must not be low-signal")
	}
}

func TestDetectLowSignalChannel_WithRepliesNotLowSignal(t *testing.T) {
	cu := mkChannelUnread("anything",
		[]goslack.Message{
			shortMsg("U1", "+"),
			shortMsg("U2", "+"),
			shortMsg("U3", "+"),
			shortMsg("U4", "+"),
			shortMsg("U5", "+"),
		},
		map[string][]goslack.Message{"1": {shortMsg("U6", "real reply")}})
	if detectLowSignalChannel(cu) {
		t.Fatal("channel with thread replies must not be low-signal")
	}
}

func TestRenderLowSignalChannel_OneLine(t *testing.T) {
	cu := mkChannelUnread("status",
		[]goslack.Message{
			shortMsg("U1", "+"),
			shortMsg("U2", "обед"),
			shortMsg("U3", "+"),
		}, nil)
	out := renderLowSignalChannel("#status", cu)
	if strings.Contains(out, "\n") {
		t.Fatalf("low-signal render must be one line, got:\n%s", out)
	}
	for _, want := range []string{"## #status", "3 short status updates", "3 people"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %s", want, out)
		}
	}
}

func TestIsSuccessReport(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"status: passed pass rate: 100% failed: 0", true},
		{"Status: PASSED ... Failed: 0 ...", true},
		{"failed: 0", false},                                   // missing passed marker
		{"status: passed", false},                              // missing failed:0 marker
		{"status: failed", false},                              // not a success report
		{"some unrelated text", false},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			if got := isSuccessReport(strings.ToLower(c.text)); got != c.want {
				t.Fatalf("isSuccessReport(%q) = %v; want %v", c.text, got, c.want)
			}
		})
	}
}

func TestClassifyLogSeverity_PassedReportNotError(t *testing.T) {
	m := goslack.Message{}
	m.Text = "Build PASSED — Total: 12, Failed: 0, Pass rate: 100%, Status: PASSED"
	if got := classifyLogSeverity(m); got != SeverityInfo {
		t.Fatalf("passing test report should classify as INFO; got %v", got)
	}
}
