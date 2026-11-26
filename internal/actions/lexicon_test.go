package actions

import (
	"reflect"
	"testing"
	"time"
)

// ADR 153: every category works in US English. These are the phrases a
// US team writes; a regression in any table shows up here by category.

func TestEnUS_Promise(t *testing.T) {
	for _, s := range []string{
		"I'll send it over", "we'll take a look", "I'm gonna fix that", "I'm going to review it",
		"will circle back", "working on it", "I can take this", "leave it with me",
		"let me dig into it", "on my list", "will get to it",
	} {
		if got := Classify(s, true, false, false); got != Promise {
			t.Errorf("promise %q = %v", s, got)
		}
	}
}

func TestEnUS_Request(t *testing.T) {
	for _, s := range []string{
		"can u review the PR", "do you mind checking the logs", "would be great if you could update the doc",
		"when you get a chance, look at the dashboard", "any updates?", "ETA?", "just following up on this",
		"gentle reminder about the invoice", "just checking in", "keep me posted", "mind taking a look",
	} {
		if got := Classify(s, false, true, false); got != Ask {
			t.Errorf("ask %q = %v", s, got)
		}
		if got := Classify(s, true, true, false); got != Waiting {
			t.Errorf("own request %q = %v", s, got)
		}
	}
}

func TestEnUS_Done(t *testing.T) {
	for _, s := range []string{
		"finished", "completed it", "wrapped up", "all set", "taken care of", "PR landed", "it's live",
		"updated the doc", "uploaded", "LGTM", "approved", "good to go", "shipped", "merged",
	} {
		if !Done(s) {
			t.Errorf("done %q not recognised", s)
		}
	}
}

func TestEnUS_Ack(t *testing.T) {
	for _, s := range []string{"sounds good", "ack", "roger", "will check", "on my radar", "👍", ":+1:", "got it"} {
		if !Ack(s) {
			t.Errorf("ack %q not recognised", s)
		}
	}
}

func TestEnUS_AbsenceAndThanks(t *testing.T) {
	for _, s := range []string{"I'll be OOO tomorrow", "I'm out sick today, will reply later", "I'll be on leave next week"} {
		if got := Classify(s, true, true, false); got != None {
			t.Errorf("absence %q = %v, want none", s, got)
		}
	}
	for _, s := range []string{"appreciate it, deadline is tomorrow anyway", "cheers, it's due friday"} {
		if got := Classify(s, false, true, true); got != None {
			t.Errorf("thanks %q = %v, want none", s, got)
		}
	}
}

func TestEnUS_Urgent(t *testing.T) {
	for _, s := range []string{"P0, please look", "this is a blocker", "time-sensitive: sign the form", "high priority pls"} {
		d := Due(s, mon)
		if !d.Urgent || !d.Day.Equal(day(11, 2)) {
			t.Errorf("urgent %q = %+v", s, d)
		}
	}
}

func TestEnUS_Deadlines(t *testing.T) {
	cases := []struct {
		text string
		want time.Time
	}{
		{"need it by COB", day(11, 2)},
		{"send it by close of business", day(11, 2)},
		{"by noon please", day(11, 2)},
		{"by 5pm today", day(11, 2)},
		{"by 3:30 pm", day(11, 2)},
		{"can you do it this afternoon", day(11, 2)},
		{"first thing in the morning", day(11, 3)},
		{"in a couple of days", day(11, 4)},
		{"in a few days", day(11, 5)},
		{"review it Thursday morning", day(11, 5)},
		{"Friday EOD works", day(11, 6)},
		{"by tomorrow EOD", day(11, 2)}, // EOD is strong and earliest
		{"by end of next week", day(11, 13)},
		{"by Dec 3rd", day(12, 3)},
	}
	for _, c := range cases {
		if got := Due(c.text, mon).Day; !got.Equal(c.want) {
			t.Errorf("Due(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestEnUS_StatusAsks(t *testing.T) {
	for _, s := range []string{"any update on the migration?", "what's the ETA", "keep me posted", "where are we on the launch", "how's it going with the report"} {
		if !AsksForAnswer(s) {
			t.Errorf("status ask %q not recognised", s)
		}
	}
	if AsksForAnswer("beta release notes") {
		t.Error(`"beta" must not read as ETA`)
	}
}

// Every language table fills every category, so a new language cannot
// ship half-done.
func TestLexiconsComplete(t *testing.T) {
	for _, l := range lexicons {
		v := reflect.ValueOf(l)
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).Len() == 0 {
				t.Errorf("a lexicon has an empty %s list", v.Type().Field(i).Name)
			}
		}
	}
}
