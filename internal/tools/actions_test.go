package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	goslack "github.com/slack-go/slack"
)

func actionDefaults() actionParams {
	return actionParams{due: "today", days: 7, staleHours: 24, limit: 60, now: time.Now()}
}

// ADR 133: one sweep of the commitment kinds, each judged from what
// followed it.
func TestRunMyActions(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "alice", "U3": "bob", "U4": "carol"})

	askTS := uaTS(-26*3600, 100)   // yesterday: "until tmrw" → due today
	waitTS := uaTS(-30*3600, 200)  // a request of mine, no reply for 30h
	promTS := uaTS(-20*3600, 300)  // a promise marked done with a reaction
	chAskTS := uaTS(-5*3600, 400)  // a channel ask I answered "done"
	chDoneTS := uaTS(-4*3600, 401) // my reply in its thread
	ackTS := uaTS(-3*3600, 500)    // an ask I only acknowledged
	ackReplyTS := uaTS(-2*3600, 501)

	f.uaSearchByQuery(t, map[string][]map[string]any{
		"from:me": {
			uaHit("D2", "U3", waitTS, "U1", "Боб, посмотри пожалуйста отчёт", "https://x.slack.com/archives/D2/p1"),
			uaHit("C1", "alpha", promTS, "U1", "пришлю отчёт до пятницы", "https://x.slack.com/archives/C1/p2"),
			uaHit("D4", "U4", ackReplyTS, "U1", "принял", ""),
		},
		"to:me": {
			uaHit("D1", "U2", askTS, "U2", "and until tmrw the full write-up", "https://x.slack.com/archives/D1/p3"),
			uaHit("D4", "U4", ackTS, "U4", "можешь проверить домен?", "https://x.slack.com/archives/D4/p4"),
		},
		"@alex": {
			uaHit("C1", "alpha", chAskTS, "U3", "<@U1> please review the MR", "https://x.slack.com/archives/C1/p5"),
		},
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D1": {uaMsg(askTS, "U2", "and until tmrw the full write-up", nil)},
		"D2": {uaMsg(waitTS, "U1", "посмотри пожалуйста", nil)},
		"D4": {uaMsg(ackReplyTS, "U1", "принял", nil), uaMsg(ackTS, "U4", "можешь проверить домен?", nil)},
	})
	f.uaReplies(t, map[string][]map[string]any{
		promTS: {uaMsg(promTS, "U1", "пришлю отчёт до пятницы", map[string]any{
			"reactions": []map[string]any{{"name": "white_check_mark", "users": []string{"U1"}, "count": 1}},
		})},
		chAskTS: {uaMsg(chAskTS, "U3", "<@U1> please review the MR", nil), uaMsg(chDoneTS, "U1", "done, approved", nil)},
	})
	h := uaHub(t, f)

	out := resultText(h.runMyActions(context.Background(), actionDefaults(), ""))

	for _, want := range []string{
		"### On you (1)",
		"📅 due today · asked of you by alice · DM alice",
		"(“tmrw”)",
		"### Waiting on others — ping (1)",
		"💤 no reply 30h · you asked bob · DM bob",
		"1 more open without a date",
		"2 done or answered",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "пришлю отчёт") || strings.Contains(out, "please review") {
		t.Errorf("done items must not be listed:\n%s", out)
	}

	p := actionDefaults()
	p.due = "all"
	all := resultText(h.runMyActions(context.Background(), p, ""))
	if !strings.Contains(all, "можешь проверить домен?") || !strings.Contains(all, "you replied") || !strings.Contains(all, "not marked done") {
		t.Errorf("due=all lists the acknowledged ask with its reply:\n%s", all)
	}
	if strings.Contains(all, "you promised") {
		t.Errorf("an acknowledgement that answers an ask is not a second item:\n%s", all)
	}
}

func TestHandleMyActions_RejectsUnknownDue(t *testing.T) {
	h := uaHub(t, uaNewFakeSlack(t))
	var req mcp.CallToolRequest
	req.Params.Arguments = map[string]any{"due": "someday"}
	res, _ := h.handleMyActions(context.Background(), req)
	if !res.IsError || !strings.Contains(resultText(res), "due must be today, week or all") {
		t.Fatalf("got %s", resultText(res))
	}
}

// ADR 133: what is not a commitment, whatever its words say.
func TestClassifyHits_SkipsNoise(t *testing.T) {
	hit := func(ch, name string, mpim bool, user, text string) goslack.SearchMessage {
		var m goslack.SearchMessage
		m.Channel.ID, m.Channel.Name, m.Channel.IsMPIM = ch, name, mpim
		m.User, m.Text, m.Timestamp = user, text, uaTS(-3600, 1)
		return m
	}
	bots := map[string]bool{"UBOT": true}
	own := []goslack.SearchMessage{
		hit("D9", "U1", false, "U1", "сделаю до пятницы"),                       // a note to self
		hit("C1", "ops", false, "U1", "<@U5> urgent "+strings.Repeat("x", 700)), // a report
	}
	toMe := []goslack.SearchMessage{
		hit("D8", "UBOT", false, "UBOT", "*Today* — please review"),   // a bot DM
		hit("G1", "mpdm-a--b", true, "U2", "Ждем всех, будем завтра"), // group chatter
		hit("G1", "mpdm-a--b", true, "U2", "<@U1> пришли пожалуйста билеты"),
		hit("D2", "U2", false, "U2", "У коллег доклад завтра"), // narrative date, no request
	}
	got := classifyHits(own, toMe, "U1", bots)
	if len(got) != 1 || !strings.Contains(got[0].hit.Text, "билеты") {
		var texts []string
		for _, it := range got {
			texts = append(texts, it.hit.Text)
		}
		t.Fatalf("want only the mention in the group DM, got %q", texts)
	}
}
