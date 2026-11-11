package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/actions"
)

func TestDraftQuote(t *testing.T) {
	for in, want := range map[string]string{
		"<@U0AAAAAAA1> пришлю отчёт https://x.example/a завтра": "пришлю отчёт завтра",
		"short":                      "short",
		"   ":                        "",
		strings.Repeat("слово ", 20): "слово слово слово слово слово слово слово слово слово слово слово…",
	} {
		if got := draftQuote(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestSlackDraft(t *testing.T) {
	names := map[string]string{"U0PATNOVAK": "pat"}
	dm := func(kind actions.Kind, text string) *actionItem {
		return &actionItem{kind: kind, dm: true, root: "1.1", hit: goslack.SearchMessage{
			Text: text, Timestamp: "1.1", Channel: goslack.CtxChannel{ID: "D0AAAAAAA1", Name: "U0PATNOVAK"}}}
	}
	cases := []struct {
		it   *actionItem
		want []string
	}{
		{dm(actions.Promise, "пришлю отчёт до пятницы"), []string{"↳ draft to pat (post_message channel=D0AAAAAAA1)", "Помню про «пришлю отчёт до пятницы»"}},
		{dm(actions.Waiting, "can you review the MR?"), []string{"A quick nudge on “can you review the MR?” – any news?"}},
		{dm(actions.Ask, "можешь проверить домен?"), []string{"Увидел про «можешь проверить домен?»"}},
	}
	for _, c := range cases {
		got := slackDraft(c.it, names)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("missing %q in %q", w, got)
			}
		}
	}

	// In a channel the draft answers in the thread.
	ch := &actionItem{kind: actions.Ask, root: "5.5", hit: goslack.SearchMessage{
		Text: "<@U1> please review", Timestamp: "6.6", Channel: goslack.CtxChannel{ID: "C0AAAAAAA1", Name: "alpha"}}}
	if got := slackDraft(ch, names); !strings.Contains(got, "channel=C0AAAAAAA1 thread_ts=5.5") || !strings.Contains(got, "thread in #alpha") {
		t.Errorf("channel draft: %q", got)
	}
	// A promise said into an open channel, no thread, no one named: nobody to write to.
	open := &actionItem{kind: actions.Promise, root: "7.7", hit: goslack.SearchMessage{
		Text: "сделаю завтра", Timestamp: "7.7", Channel: goslack.CtxChannel{ID: "C0AAAAAAA1", Name: "alpha"}}}
	if got := slackDraft(open, names); got != "" {
		t.Errorf("no addressee, no draft: %q", got)
	}
}

func TestMeetingDraft(t *testing.T) {
	v := meetingVerdict{item: meetingItem{Text: "Send Pat the server budget"}, counterpart: "Pat Novak", handle: "pat"}
	if got := meetingDraft(v); !strings.Contains(got, "draft to Pat Novak (post_message channel=@pat)") || !strings.Contains(got, "I haven't forgotten “Send Pat the server budget”") {
		t.Fatalf("got %q", got)
	}
	v.handle = ""
	if meetingDraft(v) != "" {
		t.Fatal("no Slack person, no draft")
	}
}

// drafts=true adds the draft under the item; drafts are never sent.
func TestRunMyActions_Drafts(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "alice"})
	askTS := uaTS(-26*3600, 100)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {uaHit("D1", "U2", askTS, "U2", "and until tmrw the full write-up", "https://x.slack.com/archives/D1/p3")},
	})
	f.uaHistory(t, map[string][]map[string]any{"D1": {uaMsg(askTS, "U2", "and until tmrw the full write-up", nil)}})
	h := uaHub(t, f)
	p := actionDefaults()
	p.drafts = true

	out := resultText(h.runMyActions(context.Background(), p, ""))

	tmWant(t, out, "📅 due today · asked of you by alice", "↳ draft to alice (post_message channel=D1): Seen “and until tmrw the full write-up”")
	if f.Called("chat.postMessage") {
		t.Fatal("a draft must never be sent")
	}
}

// post_message reaches a person's DM by @handle and the operator's own
// DM by @me.
func TestPostMessage_HandleAndSelfTargets(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.On("users.list", `{"ok":true,"members":[{"id":"U0PATNOVAK","name":"pat","real_name":"Pat Novak"}],"response_metadata":{"next_cursor":""}}`)
	f.On("conversations.open", `{"ok":true,"channel":{"id":"D0PATDM001"}}`)
	var got []string
	f.OnFunc("chat.postMessage", func(r *http.Request) string {
		got = append(got, r.Form.Get("channel"))
		return `{"ok":true,"channel":"x","ts":"1.1"}`
	})
	h := uaHub(t, f)

	for _, target := range []string{"@pat", "@me"} {
		if res := h.runPostMessage(context.Background(), "", target, "hi", "", 0, false); res.IsError {
			t.Fatalf("%s: %s", target, resultText(res))
		}
	}
	if strings.Join(got, ",") != "D0PATDM001,U1" {
		t.Fatalf("posted to %v, want the DM with pat and the operator's own ID", got)
	}
}
