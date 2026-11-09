package tools

import (
	"context"
	"net/http"
	"strconv"
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
		hit("D9", "U1", false, "U1", "сделаю до пятницы"),                           // a note to self
		hit("C1", "ops", false, "U1", "<@U5> urgent "+strings.Repeat("line\n", 20)), // a report
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

// ADR 133: in one DM a reply after the operator's next request answers
// that request, not the one before it.
func TestRunMyActions_ReplyBelongsToTheNextTurn(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "alice"})
	oldTS := uaTS(-26*3600, 100) // yesterday: a request with a date
	oldReply := uaTS(-25*3600, 101)
	newTS := uaTS(-3*3600, 200) // today: another request
	newReply := uaTS(-2*3600, 201)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"from:me": {
			uaHit("D2", "U2", newTS, "U1", "Алиса, нужна помощь срочно с аккаунтом", ""),
			uaHit("D2", "U2", oldTS, "U1", "Алиса, сделай пожалуйста отчёт до завтра", ""),
		},
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D2": {
			uaMsg(newReply, "U2", "предупредите, когда будет свободно", nil),
			uaMsg(newTS, "U1", "Алиса, нужна помощь срочно с аккаунтом", nil),
			uaMsg(oldReply, "U2", "посмотрю", nil),
			uaMsg(oldTS, "U1", "Алиса, сделай пожалуйста отчёт до завтра", nil),
		},
	})
	h := uaHub(t, f)
	out := resultText(h.runMyActions(context.Background(), actionDefaults(), ""))
	if !strings.Contains(out, `alice replied`) || strings.Count(out, "предупредите") != 1 {
		t.Fatalf("today's reply must answer today's request only:\n%s", out)
	}
	if !strings.Contains(out, `"посмотрю"`) {
		t.Fatalf("yesterday's request keeps yesterday's reply:\n%s", out)
	}
}

// ADR 133: the operator's own messages are read past the first page.
func TestRunMyActions_ReadsSeveralSearchPages(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	pages := map[string][]map[string]any{}
	for p := 1; p <= searchPages; p++ {
		var hits []map[string]any
		for i := 0; i < searchPageMax; i++ {
			hits = append(hits, uaHit("C1", "alpha", uaTS(-int64(p*1000+i), i), "U1", "plain note", ""))
		}
		pages[strconv.Itoa(p)] = hits
	}
	empty := `{"ok":true,"messages":{"matches":[],"total":0,"paging":{"pages":0}}}`
	f.OnFunc("search.messages", func(r *http.Request) string {
		if !strings.Contains(r.Form.Get("query"), "from:me") {
			return empty
		}
		page := r.Form.Get("page")
		if page == "" {
			page = "1" // slack-go leaves the default page out
		}
		return uaJSON(t, map[string]any{"ok": true, "messages": map[string]any{
			"matches": pages[page], "total": searchPageMax * searchPages,
			"paging": map[string]any{"count": searchPageMax, "total": searchPageMax * searchPages, "page": 1, "pages": searchPages},
		}})
	})
	h := uaHub(t, f)
	out := resultText(h.runMyActions(context.Background(), actionDefaults(), ""))
	if n := f.CountOf("search.messages"); n != searchPages+2 {
		t.Fatalf("search.messages called %d times, want %d pages of from:me + 2 mention queries", n, searchPages)
	}
	// The note says how far back the truncated search actually reached.
	if !strings.Contains(out, "search page limit") || !strings.Contains(out, "your messages only since ") {
		t.Fatalf("300 own messages still fill the window, and the output says so:\n%s", out)
	}
}

// ADR 133: a question asked at a channel's top level is answered by the
// operator's own top-level post there soon after, not only in a thread.
func TestChannelAnswers(t *testing.T) {
	msg := func(ts, user, threadTS string) goslack.Message {
		var m goslack.Message
		m.Timestamp, m.User, m.ThreadTimestamp = ts, user, threadTS
		return m
	}
	msgs := []goslack.Message{
		msg("1000.000100", "U1", ""),            // before the question
		msg("2000.000100", "U2", ""),            // someone else
		msg("2600.000100", "U1", ""),            // my answer, 10 min later
		msg("2700.000100", "U1", "2650.000100"), // my reply in another thread
		msg("20000.000100", "U1", ""),           // hours later, unrelated
	}
	got := channelAnswers(msgs, 2000.0001, "U1")
	if len(got) != 1 || got[0].TS != 2600.0001 {
		t.Fatalf("got %+v, want only the post 10 minutes later", got)
	}
}

// ADR 133: a follow-up from the asker before the operator has answered is
// the same turn, so the answer after both settles the first question too.
func TestRunMyActions_ClarificationIsTheSameTurn(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "alice"})
	qTS := uaTS(-30*3600, 100)       // a question to me
	clarTS := uaTS(-30*3600+60, 200) // her clarification with a request word
	ansTS := uaTS(-30*3600+240, 300) // my answer after both
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {
			uaHit("D2", "U2", clarTS, "U2", "мы так и написали, надо сказать что-то конкретное", ""),
			uaHit("D2", "U2", qTS, "U2", "а общими словами это как?", ""),
		},
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D2": {
			uaMsg(ansTS, "U1", "тогда убираем, отвечу завтра подробно", nil),
			uaMsg(clarTS, "U2", "мы так и написали, надо сказать что-то конкретное", nil),
			uaMsg(qTS, "U2", "а общими словами это как?", nil),
		},
	})
	h := uaHub(t, f)
	p := actionDefaults()
	p.due = "all"
	out := resultText(h.runMyActions(context.Background(), p, ""))
	if strings.Contains(out, "общими словами") {
		t.Fatalf("the question was answered after the clarification:\n%s", out)
	}
}
