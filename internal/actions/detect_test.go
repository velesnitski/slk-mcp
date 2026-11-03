package actions

import (
	"testing"
	"time"
)

// Monday 2 November 2026, 18:48 — the day the examples were written.
var mon = time.Date(2026, 11, 2, 18, 48, 0, 0, time.UTC)

func day(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }

func TestDue(t *testing.T) {
	cases := []struct {
		text string
		want time.Time
	}{
		{"send me some ideas until frriday", day(11, 6)},
		{"and until tmrw the full write-up", day(11, 3)},
		{"К завтрашнему дню нужен черновик", day(11, 3)},
		{"Сегодня обсуждали оценки. К завтрашнему дню нужен план", day(11, 3)},
		{"сделаю сегодня", day(11, 2)},
		{"нужно до конца дня", day(11, 2)},
		{"послезавтра созвонимся", day(11, 4)},
		{"пришлю до пятницы", day(11, 6)},
		{"демо на среду", day(11, 4)},
		{"by Monday please", day(11, 2)},
		{"до 15.11", day(11, 15)},
		{"deadline: 05.01.2027", time.Date(2027, 1, 5, 0, 0, 0, 0, time.UTC)},
		{"до 10 ноября", day(11, 10)},
		{"by Nov 20", day(11, 20)},
		{"через 3 дня", day(11, 5)},
		{"in 2 weeks", day(11, 16)},
		{"до конца недели", day(11, 6)},
		{"на следующей неделе", day(11, 13)},
		{"срочно, горит", day(11, 2)},
		{"до 5 января", time.Date(2027, 1, 5, 0, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got := Due(c.text, mon)
		if !got.Day.Equal(c.want) {
			t.Errorf("Due(%q) = %v (%q), want %v", c.text, got.Day.Format("02.01.2006"), got.Phrase, c.want.Format("02.01.2006"))
		}
	}
}

func TestDue_NoDeadline(t *testing.T) {
	for _, text := range []string{
		"03.11.26 • TASK-1 daily report",
		"созвон на 11:30?",
		"в среде разработки всё ок",
		"встреча в 10.11",
		"FYI, in process",
		"",
	} {
		if got := Due(text, mon); !got.IsZero() {
			t.Errorf("Due(%q) = %v (%q), want none", text, got.Day.Format("02.01"), got.Phrase)
		}
	}
}

func TestDue_Urgent(t *testing.T) {
	got := Due("это горит, нужно до пятницы", mon)
	if !got.Urgent || !got.Day.Equal(day(11, 2)) {
		t.Fatalf("got %+v", got)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		text             string
		own, addr, dated bool
		want             Kind
	}{
		{"hi, will investigate", true, true, false, Promise},
		{"Принял, сделаю до пятницы", true, false, true, Promise},
		{"Боб, посмотри пожалуйста отчёт", true, true, false, Waiting},
		{"посмотри пожалуйста", true, false, false, None},
		{"Think about the new feature", false, true, false, Ask},
		{"and until tmrw the full write-up", false, true, true, Ask},
		{"созвон в 11:30 или ещё не решили?", false, true, false, Question},
		{"прошу проверить", false, false, false, None},
		{"for both teams", false, true, false, None},
		{"больше ничего нет", true, true, false, None},
	}
	for _, c := range cases {
		if got := Classify(c.text, c.own, c.addr, c.dated); got != c.want {
			t.Errorf("Classify(%q, own=%v, addr=%v) = %v, want %v", c.text, c.own, c.addr, got, c.want)
		}
	}
}

func TestDoneAndAck(t *testing.T) {
	if !Done("Готово, отправил") || !Done("done, see MR") || Done("принял") {
		t.Fatal("Done")
	}
	if !Ack("Принял 👌") || !Ack("will investigate") || Ack("отправил файл") {
		t.Fatal("Ack")
	}
}

func FuzzDue(f *testing.F) {
	for _, s := range []string{"до 31.02", "через 99 дней", "until frriday", "deadline: 5/13/2026", "к 0 ноября"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d := Due(s, mon)
		if !d.IsZero() && d.Day.Before(day(11, 2)) {
			t.Fatalf("Due(%q) = %v, before the message", s, d.Day)
		}
		_ = Classify(s, false, true, !d.IsZero())
	})
}

// ADR 133: only a deadline set with a preposition makes a message an ask
// on its own; a bare "завтра" or "в пятницу" narrates as often.
func TestDue_Strong(t *testing.T) {
	cases := []struct {
		text   string
		strong bool
	}{
		{"and until tmrw the full write-up", true},
		{"К завтрашнему дню нужен черновик", true},
		{"пришлю до пятницы", true},
		{"до 15.11", true},
		{"срочно", true},
		{"нужно до конца дня", true},
		{"У коллег доклад завтра", false},
		{"аккаунт был открыт в пятницу", false},
		{"Завтра утром надеюсь приеду", false},
		{"на следующей неделе", false},
	}
	for _, c := range cases {
		if got := Due(c.text, mon); got.Strong != c.strong {
			t.Errorf("Due(%q).Strong = %v, want %v", c.text, got.Strong, c.strong)
		}
	}
	if got := Due("в пятницу созвон, а до среды пришли план", mon); !got.Strong || !got.Day.Equal(day(11, 4)) {
		t.Errorf("a strong deadline beats an earlier weak one: %+v", got)
	}
}
