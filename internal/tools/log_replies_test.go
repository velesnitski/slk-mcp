package tools

import (
	"context"
	"strings"
	"testing"
)

// ADR 123: summarising renderers keep thread replies.

func logChannelWithHumanReply(t *testing.T) *uaFakeSlack {
	t.Helper()
	f := uaNewFakeSlack(t)
	parent := uaTS(-600, 100)
	reply := uaTS(-120, 200)
	f.On("auth.test", uaAuthOK)
	f.uaNoSearch()
	f.uaNoCanvas()
	f.uaUsers(map[string]string{"U1": "Alex", "U2": "Sam"})
	f.uaConversations(t, uaChannel("C1", "example-alerts", nil))
	f.uaInfo(t, map[string]map[string]any{
		"C1": uaChannel("C1", "example-alerts", map[string]any{"last_read": "1700000000.000000"}),
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg(uaTS(-300, 300), "", "CRITICAL threshold exceeded on node-x", map[string]any{"bot_id": "B1"}),
			uaMsg(parent, "", "ERROR scheduled job failed", map[string]any{
				"bot_id": "B1", "thread_ts": parent, "reply_count": 1, "latest_reply": reply,
			}),
		},
	})
	f.uaReplies(t, map[string][]map[string]any{
		parent: {uaMsg(reply, "U2", "looking into it now", nil)},
	})
	return f
}

func TestUnreadSummary_LogChannelKeepsAHumanReply(t *testing.T) {
	f := logChannelWithHumanReply(t)
	h := uaHub(t, f)

	out := resultText(h.runUnreadSummary(context.Background(), uaUnreadDefaults(), ""))

	for _, want := range []string{"LOG MODE", "1 reply in earlier threads", "looking into it now"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestUnreadSummary_SkipLogModeStillShowsTheReply(t *testing.T) {
	f := logChannelWithHumanReply(t)
	h := uaHub(t, f)
	p := uaUnreadDefaults()
	p.skipLog = true

	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if strings.Contains(out, "LOG MODE") || strings.Contains(out, "threshold exceeded") {
		t.Fatalf("skip_log_mode must hide the bot feed:\n%s", out)
	}
	if !strings.Contains(out, "## #example-alerts") || !strings.Contains(out, "looking into it now") {
		t.Fatalf("the human reply must survive skip_log_mode:\n%s", out)
	}
}

func TestUnreadSummary_SkipLogModeDropsAFeedWithoutReplies(t *testing.T) {
	f := logChannelWithHumanReply(t)
	f.uaReplies(t, map[string][]map[string]any{})
	h := uaHub(t, f)
	p := uaUnreadDefaults()
	p.skipLog = true

	out := resultText(h.runUnreadSummary(context.Background(), p, ""))

	if strings.Contains(out, "example-alerts") {
		t.Fatalf("a pure feed is still skipped:\n%s", out)
	}
}
