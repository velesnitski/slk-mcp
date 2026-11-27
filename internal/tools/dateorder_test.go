package tools

import (
	"testing"
	"time"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/actions"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// ADR 152: dates print in the workspace's order.
func TestDayLabel_Order(t *testing.T) {
	d := time.Date(2026, 11, 27, 15, 4, 0, 0, time.UTC)
	if got := dayLabel(d, actions.DayFirst); got != "Fri 27.11" {
		t.Errorf("day-first = %q", got)
	}
	if got := dayLabel(d, actions.MonthFirst); got != "Fri 11/27" {
		t.Errorf("month-first = %q", got)
	}
	if got := stampLabel(d, actions.MonthFirst); got != "11/27 15:04" {
		t.Errorf("stamp month-first = %q", got)
	}
}

func TestDateOrderFor_WorkspaceOverride(t *testing.T) {
	h := &Hub{cfg: &config.Config{Workspaces: []config.WorkspaceConfig{
		{Name: "alpha"}, {Name: "beta", DateOrder: "en-US"},
	}}}
	if got := h.dateOrderFor("alpha", actions.DayFirst); got != actions.DayFirst {
		t.Errorf("alpha = %v, want inherited day-first", got)
	}
	if got := h.dateOrderFor("beta", actions.DayFirst); got != actions.MonthFirst {
		t.Errorf("beta = %v, want month-first", got)
	}
}

// ADR 155: a message's dates are read in its author's order when their
// Slack locale and time zone make it clear.
func TestAuthorDateOrder(t *testing.T) {
	cases := []struct {
		locale, tz string
		want       actions.DateOrder
		known      bool
	}{
		{"en-US", "America/New_York", actions.MonthFirst, true},
		{"en-US", "America/Indiana/Indianapolis", actions.MonthFirst, true},
		{"en-US", "Pacific/Honolulu", actions.MonthFirst, true},
		{"en-PH", "Asia/Manila", actions.MonthFirst, true},
		// Slack's default locale outside the US says nothing.
		{"en-US", "Europe/Kyiv", actions.DayFirst, false},
		{"en-US", "America/Sao_Paulo", actions.DayFirst, false},
		{"en-US", "", actions.DayFirst, false},
		// A locale chosen on purpose is day-first.
		{"en-GB", "Europe/London", actions.DayFirst, true},
		{"ru-RU", "America/New_York", actions.DayFirst, true},
		{"uk-UA", "Europe/Kyiv", actions.DayFirst, true},
		// No locale, or no region: unknown.
		{"", "America/Chicago", actions.DayFirst, false},
		{"en", "America/Chicago", actions.DayFirst, false},
	}
	for _, c := range cases {
		got, known := authorDateOrder(c.locale, c.tz)
		if got != c.want || known != c.known {
			t.Errorf("authorDateOrder(%q, %q) = %v, %v; want %v, %v", c.locale, c.tz, got, known, c.want, c.known)
		}
	}
}

func TestClassifyHits_DatesByAuthor(t *testing.T) {
	authors := map[string]actions.DateOrder{"U2": actions.MonthFirst, "U3": actions.DayFirst}
	hit := func(user, ts string) goslack.SearchMessage {
		return goslack.SearchMessage{User: user, Text: "can you send the report by 12/3", Timestamp: ts,
			Channel: goslack.CtxChannel{ID: "C0AAAAAAA1", Name: "alpha"}}
	}
	// 2 November 2026: 12/3 is December 3 month-first, 12 March day-first.
	ts := "1793620800.000100"
	items := classifyHits(nil, []goslack.SearchMessage{hit("U2", ts), hit("U3", ts), hit("U4", ts)},
		"U1", nil, orderByAuthor(authors, actions.DayFirst, true))
	got := map[string]string{}
	for _, it := range items {
		got[it.hit.User] = it.due.Day.Format("2006-01-02")
	}
	want := map[string]string{"U2": "2026-12-03", "U3": "2027-03-12", "U4": "2027-03-12"}
	for u, w := range want {
		if got[u] != w {
			t.Errorf("author %s: due %s, want %s", u, got[u], w)
		}
	}
	// Disabled: everyone in the workspace order.
	items = classifyHits(nil, []goslack.SearchMessage{hit("U2", ts)}, "U1", nil, orderByAuthor(authors, actions.DayFirst, false))
	if d := items[0].due.Day.Format("2006-01-02"); d != "2027-03-12" {
		t.Errorf("disabled: due %s, want workspace day-first", d)
	}
}
