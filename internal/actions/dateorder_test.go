package actions

import (
	"testing"
	"time"
)

// ADR 152: numeric date order, ordinals, and redirects.

func TestDueIn_DateOrder(t *testing.T) {
	cases := []struct {
		text  string
		order DateOrder
		want  time.Time
	}{
		{"by 12/3", DayFirst, time.Date(2027, 3, 12, 0, 0, 0, 0, time.UTC)},
		{"by 12/3", MonthFirst, day(12, 3)},
		{"by 11/20/2026", MonthFirst, day(11, 20)},
		// A dotted date stays day-first even in a month-first workspace.
		{"до 12.11", MonthFirst, day(11, 12)},
		// A date that exists only one way is read that way in either order.
		{"by 12/25", DayFirst, day(12, 25)},
		{"by 25/12", MonthFirst, day(12, 25)},
		{"by 13/13", MonthFirst, time.Time{}},
	}
	for _, c := range cases {
		if got := DueIn(c.text, mon, c.order).Day; !got.Equal(c.want) {
			t.Errorf("DueIn(%q, %s) = %v, want %v", c.text, c.order, got, c.want)
		}
	}
	// The default entry points stay day-first.
	if got := Due("by 12/3", mon).Day; !got.Equal(time.Date(2027, 3, 12, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Due default order = %v, want day-first", got)
	}
}

func TestDue_Ordinals(t *testing.T) {
	cases := []struct {
		text string
		want time.Time
	}{
		{"by Dec 3rd", day(12, 3)},
		{"by Nov. 21st, thanks", day(11, 21)},
		{"until the 22nd of November", day(11, 22)},
		{"by 1st December", day(12, 1)},
		{"by Nov 20", day(11, 20)},
	}
	for _, c := range cases {
		if got := Due(c.text, mon).Day; !got.Equal(c.want) {
			t.Errorf("Due(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestParseDateOrder(t *testing.T) {
	for in, want := range map[string]DateOrder{
		"": DayFirst, "dmy": DayFirst, "DMY": DayFirst, "mdy": MonthFirst,
		"en-US": MonthFirst, "en_us": MonthFirst, "us": MonthFirst,
	} {
		got, ok := ParseDateOrder(in)
		if !ok || got != want {
			t.Errorf("ParseDateOrder(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseDateOrder("ymd"); ok {
		t.Error(`ParseDateOrder("ymd") accepted`)
	}
}

func TestClassify_Redirect(t *testing.T) {
	cases := []struct {
		text string
		want Kind
	}{
		{"привет, по доступам обратись pls к devops", None},
		{"это к Пете", None},
		{"это вопрос к девопсам", None},
		{"please ask the ops team", None},
		{"ping alice about it, she owns that", None},
		{"спроси у Маши, пожалуйста", None},
		// Pointing at the operator is a real ask.
		{"обратись ко мне завтра pls", Waiting},
		{"ask me if anything breaks, please", Waiting},
		// A request left after the redirect still waits.
		{"ask ops and let me know", Waiting},
		{"обратись к девопсам и пришли мне ответ", Waiting},
		{"can you check with legal?", Waiting},
		// No redirect: unchanged.
		{"посмотри пожалуйста отчёт", Waiting},
	}
	for _, c := range cases {
		if got := Classify(c.text, true, true, false); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}
