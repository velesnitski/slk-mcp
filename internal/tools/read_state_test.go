package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// ADR 129: get_channel_info reports the operator's read state.

func rsChannel(f *fakeSlack, lastRead string) {
	f.On("auth.test", `{"ok":true,"user_id":"U1","user":"alex","team_id":"T1","url":"https://example.invalid/"}`)
	f.On("conversations.list", `{"ok":true,"channels":[{"id":"C1","name":"alpha"}]}`)
	f.On("conversations.info", `{"ok":true,"channel":{"id":"C1","name":"alpha","num_members":3,"last_read":"`+lastRead+`"}}`)
	f.On("users.info", `{"ok":true,"user":{"id":"U2","name":"sam","real_name":"Sam"}}`)
	f.On("conversations.history", `{"ok":true,"messages":[
		{"type":"message","user":"U2","text":"newest","ts":"1700000300.000100"},
		{"type":"message","user":"U1","text":"mine","ts":"1700000200.000100"},
		{"type":"message","user":"U2","text":"older","ts":"1700000100.000100"}]}`)
}

func TestGetChannelInfo_ReportsUnreadSinceTheMarker(t *testing.T) {
	f := newFakeSlack(t)
	rsChannel(f, "1700000150.000000")
	res := dcCallTool(t, newFakeHub(t, f), "get_channel_info", map[string]any{"channel": "alpha"})
	got := res.Text()
	// The operator's own message after the marker is not unread.
	if res.IsError || !strings.Contains(got, "· 1 unread") || !strings.Contains(got, "newest") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestGetChannelInfo_SaysReadWhenNothingFollowsTheMarker(t *testing.T) {
	f := newFakeSlack(t)
	rsChannel(f, "1700000300.000100")
	got := dcCallTool(t, newFakeHub(t, f), "get_channel_info", map[string]any{"channel": "alpha"}).Text()
	if !strings.Contains(got, "· read") || strings.Contains(got, "unread") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestGetChannelInfo_ReadStateCanBeSwitchedOff(t *testing.T) {
	f := newFakeSlack(t)
	rsChannel(f, "1700000150.000000")
	got := dcCallTool(t, newFakeHub(t, f), "get_channel_info", map[string]any{"channel": "alpha", "read_state": false}).Text()
	if strings.Contains(got, "read state") || f.Called("conversations.history") {
		t.Fatalf("read_state=false must skip it:\n%s", got)
	}
}

func TestFormatReadState_NoMarkerIsNotUnread(t *testing.T) {
	m := goslack.Message{}
	m.Timestamp = "1700000300.000100"
	got := formatReadState(slack.ReadState{Newest: &m, Unread: 0}, "Sam")
	if !strings.Contains(got, "no read marker") || strings.Contains(got, "unread") || strings.Contains(got, "· read") {
		t.Fatalf("an unknown marker must not be called read or unread: %q", got)
	}
	got = formatReadState(slack.ReadState{LastRead: "1700000000.000000", Newest: &m, Unread: 100, Capped: true}, "Sam")
	if !strings.Contains(got, "100+ unread") {
		t.Fatalf("a full page is a lower bound: %q", got)
	}
}
