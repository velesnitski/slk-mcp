package tools

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

// ---------------------------------------------------------------- parsing

func TestParseMeetingItems(t *testing.T) {
	items, err := parseMeetingItems([]byte(`{"ok":true,"tool":"list_action_items","items":[
		{"id":"m1:a","kind":"action","mine":true,"text":"send the plan","due":"2026-11-13","due_phrase":"by Friday",
		 "counterparts":["Pat Novak"],"meeting_start":"2026-11-11T16:06:08.894+03:00","meeting_end":"2026-11-11T16:41:38+03:00",
		 "unknown_field":42}]}`))
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	it := items[0]
	if it.due.Format("2006-01-02") != "2026-11-13" || it.start.IsZero() || !it.end.After(it.start) || it.Counterparts[0] != "Pat Novak" {
		t.Fatalf("decoded wrong: %+v", it)
	}

	if _, err := parseMeetingItems([]byte(`{"ok":false,"text":"no such tool\nmore"}`)); err == nil || !strings.Contains(err.Error(), "no such tool") || strings.Contains(err.Error(), "more") {
		t.Fatalf("a tool error must surface its first line, got %v", err)
	}
	if _, err := parseMeetingItems([]byte("not json")); err == nil {
		t.Fatal("garbage must be an error")
	}
	// A bare {"items":[…]} from another notes tool is accepted; no end
	// time falls back to the start.
	items, err = parseMeetingItems([]byte(`{"items":[{"text":"x","mine":true,"meeting_start":"2026-11-10T10:00:00Z"}]}`))
	if err != nil || len(items) != 1 || !items[0].end.Equal(items[0].start) {
		t.Fatalf("bare items: %v %v", items, err)
	}
}

// The command runs without a shell, gets since=<days>d appended, and a
// failure carries its stderr line instead of costing the whole tool.
func TestCommandMeetingSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "notes")
	body := "#!/bin/sh\n" +
		`if [ "$1" = "fail" ]; then echo "database locked" >&2; exit 3; fi` + "\n" +
		`printf '{"ok":true,"items":[{"text":"%s %s","mine":true}]}' "$1" "$2"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	items, err := commandMeetingSource(script+" owner=me")(context.Background(), 5)
	if err != nil || len(items) != 1 || items[0].Text != "owner=me since=5d" {
		t.Fatalf("items=%+v err=%v", items, err)
	}

	_, err = commandMeetingSource(script+" fail")(context.Background(), 5)
	if err == nil || !strings.Contains(err.Error(), "database locked") {
		t.Fatalf("want the stderr line, got %v", err)
	}
	if commandMeetingSource("   ") != nil {
		t.Fatal("an empty command means no meetings")
	}
}

// ---------------------------------------------------------------- matching

func TestStemsMeetInflectedForms(t *testing.T) {
	a := stems("Отправить ссылку на YouTube-канал про триаж", nil)
	b := stems("вот ссылка на канал youtube.com/@x", nil)
	if n := overlap(a, b); n < 2 {
		t.Fatalf("ссылку/ссылка and канал/канал must meet, overlap=%d a=%v b=%v", n, a, b)
	}
	if n := overlap(stems("send Pat the report", nameSkip("Pat Novak")), stems("Pat, thanks", nameSkip("Pat Novak"))); n != 0 {
		t.Fatalf("a shared name and filler are not a match, overlap=%d", n)
	}
}

func TestRosterMatch(t *testing.T) {
	users := []goslack.User{
		mnUser("U01", "sam.lee", "Sam", "Sam Lee"),
		mnUser("U02", "pat", "", "Pat Novak"),
		mnUser("U03", "kim.o", "Kim", "Kim Ortega"),
		mnUser("U04", "kim.r", "Kim", "Kim Ross"),
	}
	for _, c := range []struct{ name, want string }{
		{"Pat Novak", "U02"}, {"pat novak", "U02"}, {"Sam", "U01"},
		{"Novak Pat", "U02"}, // platforms sometimes swap the order
		{"Kim", ""},          // two people share it: no guess
		{"Nobody Here", ""}, {"", ""},
	} {
		u, ok := rosterMatch(c.name, users)
		if (c.want == "") == ok || (ok && u.ID != c.want) {
			t.Errorf("%q → %q ok=%v, want %q", c.name, u.ID, ok, c.want)
		}
	}
}

// ---------------------------------------------------------------- rendering

func mtItem(text string, due time.Time, start time.Time) meetingItem {
	return meetingItem{Kind: "action", Mine: true, Text: text, due: due, start: start, end: start.Add(30 * time.Minute), MeetingTitle: "Weekly sync"}
}

func TestRenderMeetings_Views(t *testing.T) {
	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.Local)
	day := func(d int) time.Time { return midnightOf(now).AddDate(0, 0, d) }
	start := now.Add(-26 * time.Hour)
	verdicts := []meetingVerdict{
		{item: mtItem("overdue item", day(-1), start), counterpart: "Pat Novak"},
		{item: mtItem("due in three days", day(3), start)},
		{item: mtItem("undated item", time.Time{}, start)},
		{item: mtItem("restated in Slack", day(0), start), duplicate: true},
		{item: mtItem("delivered item", day(0), start), evidence: &goslack.SearchMessage{
			Channel: goslack.CtxChannel{Name: "U02"}, Timestamp: "1794400000.000100", Text: "here it is https://x", Permalink: "https://x.slack.com/archives/D2/p9"}},
	}

	today := renderMeetings(verdicts, actionParams{due: "today", now: now}, false)
	for _, want := range []string{
		"### From meetings (1)",
		`⏰ overdue 1d · you promised Pat Novak · meeting “Weekly sync”`,
		"### From meetings – probably done, check (1)",
		"✅ you wrote", "https://x.slack.com/archives/D2/p9",
		"2 more from meetings without a date or due later",
		"1 from meetings also found in Slack",
	} {
		if !strings.Contains(today, want) {
			t.Errorf("today: missing %q in\n%s", want, today)
		}
	}
	if strings.Contains(today, "restated in Slack") || strings.Contains(today, "undated item") {
		t.Errorf("today must hide duplicates and undated items:\n%s", today)
	}

	week := renderMeetings(verdicts, actionParams{due: "week", now: now}, false)
	if !strings.Contains(week, "### From meetings (2)") || !strings.Contains(week, "due in three days") {
		t.Errorf("week must include the item due in 3 days:\n%s", week)
	}
	all := renderMeetings(verdicts, actionParams{due: "all", now: now}, false)
	if !strings.Contains(all, "### From meetings (3)") || !strings.Contains(all, "undated item") {
		t.Errorf("all must list undated items:\n%s", all)
	}
	if renderMeetings(nil, actionParams{due: "today", now: now}, false) != "" {
		t.Error("no items, no section")
	}
}

// ---------------------------------------------------------------- end to end

// One meeting promise is restated in Slack (counted, not repeated), one
// was delivered by a later DM (probably done), one is still open, and an
// item owned by someone else or a decision never shows.
func TestRunMyActions_MergesMeetings(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "pat", "U3": "sam.lee"})
	f.On("users.list", `{"ok":true,"members":[`+
		`{"id":"U2","name":"pat","real_name":"Pat Novak","profile":{"real_name":"Pat Novak"}},`+
		`{"id":"U3","name":"sam.lee","real_name":"Sam Lee","profile":{"real_name":"Sam Lee","display_name":"Sam"}}`+
		`],"response_metadata":{"next_cursor":""}}`)

	meetStart := time.Now().Add(-26 * time.Hour)
	promiseTS := uaTS(-25*3600, 100) // my Slack promise, after the call
	linkTS := uaTS(-24*3600, 200)    // my DM to Sam with the link

	slackPromise := uaHit("D2", "U2", promiseTS, "U1", "Pat, пришлю бюджет по серверам завтра", "https://x.slack.com/archives/D2/p1")
	linkDM := uaHit("D3", "U3", linkTS, "U1", "вот: https://example.com/video", "https://x.slack.com/archives/D3/p2")
	var queries []string
	f.OnFunc("search.messages", func(r *http.Request) string {
		q := r.Form.Get("query")
		queries = append(queries, q)
		switch {
		case strings.Contains(q, "in:@sam.lee"):
			return uaJSON(t, map[string]any{"ok": true, "messages": map[string]any{"matches": []any{linkDM}, "total": 1}})
		case strings.Contains(q, "in:@"):
			return `{"ok":true,"messages":{"matches":[],"total":0}}`
		case strings.HasPrefix(q, "from:me"):
			return uaJSON(t, map[string]any{"ok": true, "messages": map[string]any{"matches": []any{slackPromise}, "total": 1}})
		}
		return `{"ok":true,"messages":{"matches":[],"total":0}}`
	})
	f.uaHistory(t, map[string][]map[string]any{
		"D2": {uaMsg(promiseTS, "U1", "Pat, пришлю бюджет по серверам завтра", nil)},
	})
	h := uaHub(t, f)

	yesterday := midnightOf(time.Now()).AddDate(0, 0, -1)
	items := []meetingItem{
		{Kind: "action", Mine: true, Text: "Прислать Pat бюджет по серверам", Counterparts: []string{"Pat Novak"}, due: midnightOf(time.Now()), DuePhrase: "завтра"},
		{Kind: "action", Mine: true, Text: "Отправить Sam ссылку на видео", Counterparts: []string{"Sam Lee"}},
		{Kind: "action", Mine: true, Text: "Провести интервью с кандидатом", due: yesterday, DuePhrase: "вчера"},
		{Kind: "action", Mine: false, Text: "someone else's task", due: yesterday},
		{Kind: "decision", Mine: true, Text: "a decision, not a task", due: yesterday},
	}
	for i := range items {
		items[i].start, items[i].end, items[i].MeetingTitle = meetStart, meetStart.Add(40*time.Minute), "Planning"
	}
	p := actionDefaults()
	p.meetings = func(context.Context, int) ([]meetingItem, error) { return items, nil }

	out := resultText(h.runMyActions(context.Background(), p, ""))

	for _, want := range []string{
		"## Meetings",
		"### From meetings (1)", "⏰ overdue 1d · you promised · meeting “Planning”", "Провести интервью",
		"### From meetings – probably done, check (1)", "Отправить Sam ссылку", "https://x.slack.com/archives/D3/p2",
		"1 from meetings also found in Slack",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, not := range []string{"someone else's task", "a decision", "Прислать Pat бюджет"} {
		if strings.Contains(out, not) {
			t.Errorf("%q must not be listed:\n%s", not, out)
		}
	}
	if !strings.Contains(strings.Join(queries, "|"), "from:me in:@sam.lee after:") {
		t.Errorf("the DM with the counterpart must be searched, queries=%v", queries)
	}
}

// A failing notes command costs a note, never the Slack part.
func TestRunMyActions_MeetingFailureIsANote(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.On("search.messages", `{"ok":true,"messages":{"matches":[],"total":0}}`)
	h := uaHub(t, f)
	p := actionDefaults()
	p.meetings = func(context.Context, int) ([]meetingItem, error) { return nil, errors.New("notes: database locked") }

	res := h.runMyActions(context.Background(), p, "")

	if res.IsError {
		t.Fatalf("must not fail: %s", resultText(res))
	}
	tmWant(t, resultText(res), "nothing due", "_meetings not included: notes: database locked_")
}

// A message sent before the meeting cannot be what the meeting promised.
func TestJudgeMeetings_EvidenceMustFollowTheMeeting(t *testing.T) {
	f := uaNewFakeSlack(t)
	before := uaTS(-30*3600, 100)
	after := uaTS(-20*3600, 200)
	f.OnFunc("search.messages", func(r *http.Request) string {
		return uaJSON(t, map[string]any{"ok": true, "messages": map[string]any{"total": 1, "matches": []any{
			uaHit("D3", "U3", before, "U1", "old link https://example.com/a", "https://x.slack.com/archives/D3/old"),
		}}})
	})
	h := uaHub(t, f)
	sam := []goslack.User{mnUser("U3", "sam.lee", "Sam", "Sam Lee")}
	it := meetingItem{Kind: "action", Mine: true, Text: "send Sam the link", Counterparts: []string{"Sam Lee"}}
	it.start = time.Now().Add(-26 * time.Hour)
	it.end = it.start.Add(time.Hour)

	v := judgeMeetings(context.Background(), []meetingItem{it}, []wsRoster{{name: "primary", hub: h, users: sam}})
	if v[0].evidence != nil {
		t.Fatalf("a message older than the meeting was taken as evidence: %+v", v[0].evidence)
	}

	// The same message after the meeting is evidence.
	f.OnFunc("search.messages", func(r *http.Request) string {
		return uaJSON(t, map[string]any{"ok": true, "messages": map[string]any{"total": 1, "matches": []any{
			uaHit("D3", "U3", after, "U1", "here https://example.com/a", "https://x.slack.com/archives/D3/new"),
		}}})
	})
	v = judgeMeetings(context.Background(), []meetingItem{it}, []wsRoster{{name: "primary", hub: h, users: sam}})
	if v[0].evidence == nil || v[0].evidence.Permalink != "https://x.slack.com/archives/D3/new" {
		t.Fatalf("want the later message as evidence, got %+v", v[0].evidence)
	}
}
