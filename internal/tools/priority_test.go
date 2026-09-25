package tools

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// ADR 112 — priority channels.

// prFake is a workspace with two channels: `alpha` is the operator's
// priority channel and has been READ (last_read after its newest
// message), `beta` has one genuinely unread message.
func prFake(t *testing.T) (*fakeSlack, string, string) {
	t.Helper()
	alphaTS := pgAgo(2*time.Hour, 1)
	betaTS := pgAgo(time.Hour, 2)
	readMark := pgAgo(30*time.Minute, 0)

	f := newFakeSlack(t)
	tmAuthTest(f, "U1", tmHost+"/")
	tmUsers(f, map[string]string{"U1": "alex", "U2": "sam"})
	f.On("users.conversations", `{"ok":true,"channels":[
		{"id":"C1","name":"alpha","is_channel":true,"is_member":true},
		{"id":"C2","name":"beta","is_channel":true,"is_member":true}]}`)
	f.OnFunc("conversations.info", func(r *http.Request) string {
		id := r.Form.Get("channel")
		lastRead := readMark // alpha: read
		if id == "C2" {
			lastRead = pgAgo(3*time.Hour, 0) // beta: unread
		}
		name := map[string]string{"C1": "alpha", "C2": "beta"}[id]
		return fmt.Sprintf(`{"ok":true,"channel":{"id":%q,"name":%q,"is_channel":true,"last_read":%q}}`, id, name, lastRead)
	})
	f.OnFunc("conversations.history", func(r *http.Request) string {
		oldest := r.Form.Get("oldest")
		var msgs []string
		switch r.Form.Get("channel") {
		case "C1":
			msgs = append(msgs, fmt.Sprintf(`{"type":"message","user":"U2","ts":%q,"text":"decision in the priority channel"}`, alphaTS))
		case "C2":
			msgs = append(msgs, fmt.Sprintf(`{"type":"message","user":"U2","ts":%q,"text":"ordinary unread message"}`, betaTS))
		}
		// Honour `oldest` the way Slack does, so the unread sweep (which
		// asks from last_read) sees nothing new in the read channel.
		var kept []string
		for _, m := range msgs {
			if oldest == "" || strings.Contains(m, "priority") && pgF(alphaTS) > pgF(oldest) ||
				strings.Contains(m, "ordinary") && pgF(betaTS) > pgF(oldest) {
				kept = append(kept, m)
			}
		}
		return `{"ok":true,"messages":[` + strings.Join(kept, ",") + `]}`
	})
	return f, alphaTS, readMark
}

func prParams() unreadParams {
	return unreadParams{maxPer: 20, replyCap: 3, logMode: "auto", logSamples: 1, priorityHours: 24, dmFullText: true}
}

func TestUnreadSummary_ReadPriorityChannelIsStillShownFirst(t *testing.T) {
	f, _, _ := prFake(t)
	h := newFakeHub(t, f, func(c *config.Config) { c.PriorityChannels = []string{"#alpha"} })

	body, _, err := h.buildUnreadSummary(context.Background(), prParams())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "decision in the priority channel") {
		t.Fatalf("a read priority channel must still be shown:\n%s", body)
	}
	if !strings.Contains(body, "★ #alpha · read") {
		t.Fatalf("the priority channel must be marked, and marked as read:\n%s", body)
	}
	if !strings.Contains(body, "ordinary unread message") {
		t.Fatalf("ordinary unread channels must still be shown:\n%s", body)
	}
	if strings.Index(body, "★ #alpha") > strings.Index(body, "#beta") {
		t.Fatalf("the priority channel must come first:\n%s", body)
	}
}

func TestUnreadSummary_WithoutPriorityChannelsAReadChannelStaysHidden(t *testing.T) {
	// The control: the same workspace without the setting behaves as the
	// sweep always has — which is why the feature is needed.
	f, _, _ := prFake(t)
	h := newFakeHub(t, f)

	body, _, err := h.buildUnreadSummary(context.Background(), prParams())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "decision in the priority channel") {
		t.Fatalf("without the setting a read channel is not in the sweep:\n%s", body)
	}
}

func TestUnreadSummary_UnknownPriorityChannelIsReported(t *testing.T) {
	f, _, _ := prFake(t)
	h := newFakeHub(t, f, func(c *config.Config) { c.PriorityChannels = []string{"alpha", "gamma"} })

	body, _, err := h.buildUnreadSummary(context.Background(), prParams())
	if err != nil {
		t.Fatal(err)
	}
	tmWant(t, body, "priority channel(s) not found or not joined: gamma", "★ #alpha")
}

func TestUnreadSummary_PriorityHoursZeroTurnsItOff(t *testing.T) {
	f, _, _ := prFake(t)
	h := newFakeHub(t, f, func(c *config.Config) { c.PriorityChannels = []string{"alpha"} })
	p := prParams()
	p.priorityHours = 0

	body, _, err := h.buildUnreadSummary(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "★") {
		t.Fatalf("priority_hours=0 must disable the feature:\n%s", body)
	}
}

func TestUnreadSummary_DeltaCursorBoundsThePriorityWindow(t *testing.T) {
	// With `after`, a re-pull shows only what is new in the priority
	// channel since the cursor — not the whole 24 hours again.
	f, alphaTS, _ := prFake(t)
	h := newFakeHub(t, f, func(c *config.Config) { c.PriorityChannels = []string{"alpha"} })
	p := prParams()
	p.afterTS = alphaTS // cursor at the priority message itself

	body, _, err := h.buildUnreadSummary(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "decision in the priority channel") {
		t.Fatalf("a message at or before the cursor must not reappear:\n%s", body)
	}
}

// ---------------------------------------------------------------- helpers

func TestMergePriority_MergesWithoutDuplicatingAndMarks(t *testing.T) {
	base := []*slack.ChannelUnread{{
		Channel:  goslack.Channel{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C1"}}},
		Messages: []goslack.Message{{Msg: goslack.Msg{Timestamp: "2.0", Text: "unread"}}},
	}}
	prio := []*slack.ChannelUnread{
		{
			Channel:  goslack.Channel{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C1"}}},
			LastRead: "5.0",
			Messages: []goslack.Message{{Msg: goslack.Msg{Timestamp: "2.0"}}, {Msg: goslack.Msg{Timestamp: "3.0", Text: "read"}}},
			Priority: true,
		},
		{
			Channel:  goslack.Channel{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C9"}}},
			Messages: []goslack.Message{{Msg: goslack.Msg{Timestamp: "1.0"}}},
			Priority: true,
		},
	}

	got := mergePriority(base, prio)

	if len(got) != 2 {
		t.Fatalf("want the existing channel merged and the new one appended, got %d", len(got))
	}
	c1 := got[0]
	if !c1.Priority || len(c1.Messages) != 2 || c1.LastRead != "5.0" {
		t.Fatalf("merged entry = %+v", c1)
	}
	if c1.Messages[0].Timestamp != "3.0" {
		t.Fatalf("merged messages must stay newest-first, got %s first", c1.Messages[0].Timestamp)
	}
}

func TestPriorityLabel(t *testing.T) {
	cu := &slack.ChannelUnread{LastRead: "5.0", Messages: []goslack.Message{{Msg: goslack.Msg{Timestamp: "4.0"}}}}
	if got := priorityLabel("#alpha", cu); got != "★ #alpha · read" {
		t.Fatalf("got %q", got)
	}
	cu.Messages = append(cu.Messages, goslack.Message{Msg: goslack.Msg{Timestamp: "6.0"}})
	if got := priorityLabel("#alpha", cu); got != "★ #alpha" {
		t.Fatalf("a newer-than-last_read message means not read, got %q", got)
	}
	if got := priorityLabel("#alpha", &slack.ChannelUnread{}); got != "★ #alpha" {
		t.Fatalf("unknown last_read must not claim read, got %q", got)
	}
}
