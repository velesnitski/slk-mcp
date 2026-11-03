package actions

import (
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	item := Msg{User: "U2", Text: "можешь проверить?", TS: 100}
	cases := []struct {
		name   string
		kind   Kind
		item   Msg
		follow []Msg
		want   Status
	}{
		{"silence", Ask, item, nil, Open},
		{"ack only", Ask, item, []Msg{{User: "U1", Text: "принял", TS: 101}}, Acked},
		{"done message", Ask, item, []Msg{{User: "U1", Text: "принял", TS: 101}, {User: "U1", Text: "готово", TS: 102}}, Closed},
		{"file counts as done", Ask, item, []Msg{{User: "U1", Text: "вот", TS: 101, Files: true}}, Closed},
		{"done reaction", Ask, Msg{User: "U2", TS: 100, Reactions: map[string][]string{"white_check_mark": {"U2"}}}, nil, Closed},
		{"others' replies do not move an ask", Ask, item, []Msg{{User: "U3", Text: "done", TS: 101}}, Open},
		{"older messages are ignored", Ask, item, []Msg{{User: "U1", Text: "done", TS: 99}}, Open},
		{"any answer settles a question", Question, item, []Msg{{User: "U1", Text: "в 11:30", TS: 101}}, Answered},
		{"waiting: their ack", Waiting, Msg{User: "U1", TS: 100}, []Msg{{User: "U3", Text: "ok", TS: 101}}, Acked},
		{"waiting: my own follow-up is not their reply", Waiting, Msg{User: "U1", TS: 100}, []Msg{{User: "U1", Text: "done?", TS: 101}}, Open},
		{"waiting: their done", Waiting, Msg{User: "U1", TS: 100}, []Msg{{User: "U3", Text: "отправил", TS: 101}}, Closed},
	}
	for _, c := range cases {
		if got := Resolve(c.kind, c.item, c.follow, "U1").Status; got != c.want {
			t.Errorf("%s: status %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStale(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if !Stale(now.Add(-25*time.Hour), Outcome{}, now, 24*time.Hour) {
		t.Fatal("open for 25h is stale")
	}
	if Stale(now.Add(-25*time.Hour), Outcome{Status: Acked}, now, 24*time.Hour) {
		t.Fatal("an acknowledged item is not waiting for a nudge")
	}
	if Stale(now.Add(-2*time.Hour), Outcome{}, now, 24*time.Hour) {
		t.Fatal("2h is not stale")
	}
}
