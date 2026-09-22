package tools

import (
	"context"
	"strings"
	"testing"
)

func TestRunListChannels_RendersChannels(t *testing.T) {
	f := newFakeSlack(t).On("conversations.list", `{"ok":true,"channels":[
		{"id":"C1","name":"alpha","num_members":7,"is_member":true,"purpose":{"value":"Team alpha"}},
		{"id":"C2","name":"beta","num_members":3,"is_member":false}
	],"response_metadata":{"next_cursor":""}}`)
	h := newFakeHub(t, f)

	out := resultText(h.runListChannels(context.Background(), "", 10, false))
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Fatalf("channels missing from output:\n%s", out)
	}
	if !f.Called("conversations.list") {
		t.Fatalf("conversations.list not called; calls=%v", f.Calls())
	}
}
