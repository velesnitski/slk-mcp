// Package actions finds the commitments in a Slack conversation: what the
// operator promised, what others asked of them, and what they asked of
// others, each with the deadline its text names. It is pure — phrase and
// date rules over message text — so every rule is unit-testable and costs
// nothing to run. See ADR 133.
package actions

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is what a message commits someone to.
type Kind int

const (
	// None is a message that commits nobody to anything.
	None Kind = iota
	// Promise is the operator saying they will do something.
	Promise
	// Ask is someone asking the operator to do something.
	Ask
	// Question is someone asking the operator something; any answer
	// settles it.
	Question
	// Waiting is the operator asking someone else to do something.
	Waiting
)

func (k Kind) String() string {
	switch k {
	case Promise:
		return "promise"
	case Ask:
		return "ask"
	case Question:
		return "question"
	case Waiting:
		return "waiting"
	}
	return "none"
}

// Deadline is the due date a message names.
type Deadline struct {
	// Day is the due day at midnight in the message's location; zero
	// when the message names none.
	Day time.Time
	// Phrase is the text the date was read from, for the reader to judge.
	Phrase string
	// Urgent marks "asap" / "срочно" / "горит": due the day it was said.
	Urgent bool
	// Strong marks a deadline set with a preposition — "до пятницы",
	// "until tmrw", "by 15.11" — or as urgent. A bare "завтра" or "в
	// пятницу" also narrates ("доклад завтра", "открыли в пятницу"), so
	// only a strong deadline makes a message an ask on its own.
	Strong bool
}

// IsZero reports whether no deadline was found.
func (d Deadline) IsZero() bool { return d.Day.IsZero() }

// Classify says what the message commits to. own is whether the operator
// wrote it; addressed is whether it was written to someone in particular
// — a DM, or a message that @-mentions someone (the operator, for a
// message from someone else; anyone else, for the operator's own). dated
// is whether it names a deadline: "until tmrw the write-up" asks for
// something without a single request word.
func Classify(text string, own, addressed, dated bool) Kind {
	t := normalize(text)
	if t == "" {
		return None
	}
	if own {
		if addressed && requestRe.MatchString(t) {
			return Waiting
		}
		if promiseRe.MatchString(t) {
			return Promise
		}
		return None
	}
	if !addressed {
		return None
	}
	if requestRe.MatchString(t) || dated {
		return Ask
	}
	if strings.Contains(t, "?") {
		return Question
	}
	return None
}

// Done reports whether a follow-up message says the work is finished.
func Done(text string) bool { return doneRe.MatchString(normalize(text)) }

// Ack reports whether a follow-up message only accepts the work —
// "принял", "on it" — without finishing it.
func Ack(text string) bool { return ackRe.MatchString(normalize(text)) }

// DoneReactions are the reactions that mark a message's work finished.
var DoneReactions = map[string]bool{
	"white_check_mark": true, "heavy_check_mark": true, "ballot_box_with_check": true,
	"done": true, "check": true, "approved": true, "merged": true,
}

// Due finds the earliest deadline the text names, read relative to when
// it was written: "до завтра" said yesterday is due today.
func Due(text string, at time.Time) Deadline {
	t := normalize(text)
	day := midnight(at)
	// The earliest strong deadline wins; a weak one counts only when
	// there is no strong one.
	var best Deadline
	consider := func(d time.Time, phrase string, strong bool) {
		if d.IsZero() || d.Before(day) {
			return
		}
		if best.IsZero() || (strong && !best.Strong) || (strong == best.Strong && d.Before(best.Day)) {
			best = Deadline{Day: d, Phrase: strings.TrimSpace(phrase), Urgent: best.Urgent, Strong: strong}
		}
	}

	if m := urgentRe.FindString(t); m != "" {
		consider(day, m, true)
		best.Urgent = true
	}
	for _, r := range relativeDays {
		if m := r.re.FindString(t); m != "" {
			consider(day.AddDate(0, 0, r.days), m, r.strong || strongPrepRe.MatchString(t[:strings.Index(t, m)+len(m)]))
		}
	}
	if m := inDaysRe.FindStringSubmatch(t); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 && n <= 60 {
			mult := 1
			if strings.HasPrefix(m[2], "нед") || strings.HasPrefix(m[2], "week") {
				mult = 7
			}
			consider(day.AddDate(0, 0, n*mult), m[0], true)
		}
	}
	if m := thisWeekRe.FindString(t); m != "" {
		consider(weekdayOnOrAfter(day, time.Friday), m, strongWeekRe.MatchString(m))
	}
	if m := nextWeekRe.FindString(t); m != "" {
		// Friday of the week after this one.
		consider(weekdayOnOrAfter(day.AddDate(0, 0, 1), time.Monday).AddDate(0, 0, 4), m, false)
	}
	for _, m := range weekdayRe.FindAllStringSubmatch(t, -1) {
		if wd, ok := weekdayOf(m[2]); ok {
			consider(weekdayOnOrAfter(day, wd), m[0], isStrongPrep(m[1]))
		}
	}
	for _, m := range numericDateRe.FindAllStringSubmatch(t, -1) {
		consider(dateOf(day, m[2], m[3], m[4]), m[0], true)
	}
	for _, m := range monthDayRe.FindAllStringSubmatch(t, -1) {
		if mon, ok := monthOf(m[3]); ok {
			consider(dateOf(day, m[2], strconv.Itoa(int(mon)), ""), m[0], true)
		}
	}
	for _, m := range dayMonthEnRe.FindAllStringSubmatch(t, -1) {
		if mon, ok := monthOf(m[1]); ok {
			consider(dateOf(day, m[2], strconv.Itoa(int(mon)), ""), m[0], true)
		}
	}
	if best.IsZero() {
		if m := todayRe.FindString(t); m != "" {
			consider(day, m, false)
		}
	}
	return best
}

func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(s, "ё", "е")
}

func midnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// weekdayOnOrAfter is the first day on or after day that falls on wd.
func weekdayOnOrAfter(day time.Time, wd time.Weekday) time.Time {
	return day.AddDate(0, 0, (int(wd)-int(day.Weekday())+7)%7)
}

// dateOf builds a day from text parts. A date without a year that is
// more than two months behind the message is read as next year's.
func dateOf(day time.Time, dd, mm, yy string) time.Time {
	d, err1 := strconv.Atoi(dd)
	m, err2 := strconv.Atoi(mm)
	if err1 != nil || err2 != nil || d < 1 || d > 31 || m < 1 || m > 12 {
		return time.Time{}
	}
	y := day.Year()
	if yy != "" {
		n, err := strconv.Atoi(yy)
		if err != nil {
			return time.Time{}
		}
		if n < 100 {
			n += 2000
		}
		y = n
	}
	out := time.Date(y, time.Month(m), d, 0, 0, 0, 0, day.Location())
	if out.Day() != d {
		return time.Time{} // 31.02 and the like
	}
	if yy == "" && out.Before(day.AddDate(0, -2, 0)) {
		out = out.AddDate(1, 0, 0)
	}
	return out
}

// Word boundaries: Go's \b is ASCII-only, so Cyrillic words are bounded
// explicitly by start/end or a non-letter.
const (
	wb  = `(?:^|[^\p{L}\p{N}_])`
	web = `(?:$|[^\p{L}\p{N}_])`
)

func words(alts ...string) *regexp.Regexp {
	return regexp.MustCompile(wb + `(?:` + strings.Join(alts, "|") + `)` + web)
}

var (
	promiseRe = words(
		`сделаю`, `пришлю`, `подготовлю`, `посмотрю`, `проверю`, `отправлю`, `напишу`,
		`скину`, `дам знать`, `вернусь`, `займусь`, `беру`, `возьму`, `уточню`, `уточняю`,
		`разберусь`, `доделаю`, `допишу`, `соберу`, `оформлю`, `передам`, `подготовим`,
		`сделаем`, `пришлем`, `посмотрим`, `проверим`, `в процессе`, `в работе`, `принял`,
		`i'?ll`, `i will`, `we'?ll`, `we will`, `will (?:do|send|check|look|investigate|prepare|share|update|follow up|get back|ping|fix|review|come back)`,
		`on it`, `let me (?:check|look|see)`, `in process`, `in progress`, `will do`,
	)
	requestRe = words(
		`прошу`, `просьба`, `пожалуйста`, `пж`, `плз`, `можешь`, `можете`, `сможешь`, `сможете`,
		`подскажи(?:те)?`, `посмотри(?:те)?`, `проверь(?:те)?`, `сделай(?:те)?`, `пришли(?:те)?`,
		`скинь(?:те)?`, `глянь(?:те)?`, `ответь(?:те)?`, `согласуй(?:те)?`, `подготовь(?:те)?`,
		`напиши(?:те)?`, `помоги(?:те)?`, `дай(?:те)?`, `нужно`, `надо`, `нужен`, `нужна`, `нужны`,
		`требуется`, `жду`,
		`please`, `pls`, `plz`, `can you`, `could you`, `would you`, `need`, `needs`, `let me know`,
		`lmk`, `give me`, `think about`, `send me`, `make sure`,
	)
	doneRe = words(
		`готово`, `готов`, `готова`, `сделал`, `сделала`, `сделано`, `отправил`, `отправила`,
		`скинул`, `скинула`, `прислал`, `прислала`, `выкатил`, `залил`, `закрыл`, `закрыто`,
		`починил`, `исправил`, `держи`, `done`, `sent`, `shipped`, `fixed`, `merged`, `ready`,
		`deployed`, `here it is`, `here you go`, `attached`, `resolved`, `closed`,
	)
	ackRe = words(
		`принял`, `приняла`, `ок`, `окей`, `хорошо`, `понял`, `поняла`, `договорились`, `беру`,
		`в процессе`, `в работе`, `посмотрю`, `ok`, `okay`, `got it`, `on it`, `will do`,
		`will investigate`, `noted`, `sure`, `in process`, `in progress`,
	)
	urgentRe = words(`asap`, `срочно`, `горит`, `urgent`, `немедленно`, `прямо сейчас`)

	relativeDays = []struct {
		re     *regexp.Regexp
		days   int
		strong bool
	}{
		{words(`послезавтра`, `day after tomorrow`), 2, false},
		{regexp.MustCompile(wb + `(?:завтра|завтрашн\p{L}*|tomorrow|tomorow|tmrw|tmr|tmrow)` + web), 1, false},
		{words(`tonight`, `eod`, `end of day`, `до конца дня`, `к концу дня`, `до вечера`, `до сегодня`, `by today`), 0, true},
	}
	// strongPrepRe finds a deadline preposition right before the end of
	// the text it is given: "до завтра", "until tmrw", "к завтрашнему".
	strongPrepRe = regexp.MustCompile(wb + `(?:до|к|ко|by|until|till|til|before|не позже)\s+\p{L}+` + web + `?$`)
	strongWeekRe = words(`до конца недели`, `к концу недели`, `end of (?:the )?week`, `eow`, `by the weekend`)
	// todayRe is weak: "сегодня обсуждали" narrates more often than it
	// sets a deadline, so it counts only when nothing else does.
	todayRe    = words(`сегодня`, `today`)
	inDaysRe   = regexp.MustCompile(wb + `(?:через|in|within)\s+(\d{1,2})\s+(дн\p{L}*|день|days?|недел\p{L}*|weeks?)` + web)
	thisWeekRe = words(`на этой неделе`, `до конца недели`, `к концу недели`, `this week`, `end of (?:the )?week`, `eow`, `by the weekend`)
	nextWeekRe = words(`на следующей неделе`, `next week`)

	prep      = `(?:до|к|ко|в|во|на|by|until|till|til|before|on|this|next|deadline|дедлайн|срок)`
	datePrep  = `(?:до|к|ко|by|until|till|til|before|on|deadline|дедлайн|срок)`
	weekdayRe = regexp.MustCompile(wb + `(` + prep + `)\s+(понедельник\p{L}*|вторник\p{L}*|сред[ауы]|четверг\p{L}*|пятниц\p{L}*|суббот\p{L}*|воскресень\p{L}*|mon(?:day)?|tue(?:s|sday)?|wed(?:nesday)?|thu(?:r|rs|rsday)?|fr+i(?:day)?|sat(?:urday)?|sun(?:day)?)` + web)

	numericDateRe = regexp.MustCompile(wb + `(` + datePrep + `)\s*:?\s*(\d{1,2})[./](\d{1,2})(?:[./](\d{2}|\d{4}))?` + web)
	monthDayRe    = regexp.MustCompile(wb + `(` + datePrep + `)\s+(\d{1,2})\s+(январ\p{L}*|феврал\p{L}*|март\p{L}*|апрел\p{L}*|ма[яй]|июн\p{L}*|июл\p{L}*|август\p{L}*|сентябр\p{L}*|октябр\p{L}*|ноябр\p{L}*|декабр\p{L}*|jan\p{L}*|feb\p{L}*|mar\p{L}*|apr\p{L}*|may|jun\p{L}*|jul\p{L}*|aug\p{L}*|sep\p{L}*|oct\p{L}*|nov\p{L}*|dec\p{L}*)` + web)
	dayMonthEnRe  = regexp.MustCompile(wb + `(?:by|until|till|before|on)\s+(jan\p{L}*|feb\p{L}*|mar\p{L}*|apr\p{L}*|may|jun\p{L}*|jul\p{L}*|aug\p{L}*|sep\p{L}*|oct\p{L}*|nov\p{L}*|dec\p{L}*)\s+(\d{1,2})` + web)
)

// isStrongPrep reports whether a weekday's preposition sets a deadline
// ("до пятницы", "by Friday") rather than placing an event ("в пятницу").
func isStrongPrep(p string) bool {
	switch p {
	case "до", "к", "ко", "by", "until", "till", "til", "before", "deadline", "дедлайн", "срок":
		return true
	}
	return false
}

// monthOf reads a month name, Russian or English, by its stem.
func monthOf(s string) (time.Month, bool) {
	stems := []struct {
		ru, en string
	}{
		{"январ", "jan"}, {"феврал", "feb"}, {"март", "mar"}, {"апрел", "apr"}, {"ма", "may"}, {"июн", "jun"},
		{"июл", "jul"}, {"август", "aug"}, {"сентябр", "sep"}, {"октябр", "oct"}, {"ноябр", "nov"}, {"декабр", "dec"},
	}
	for i, st := range stems {
		if strings.HasPrefix(s, st.en) || (strings.HasPrefix(s, st.ru) && (st.ru != "ма" || s == "мая" || s == "май")) {
			return time.Month(i + 1), true
		}
	}
	return 0, false
}

func weekdayOf(s string) (time.Weekday, bool) {
	switch {
	case strings.HasPrefix(s, "понедельник"), strings.HasPrefix(s, "mon"):
		return time.Monday, true
	case strings.HasPrefix(s, "вторник"), strings.HasPrefix(s, "tue"):
		return time.Tuesday, true
	case strings.HasPrefix(s, "сред"), strings.HasPrefix(s, "wed"):
		return time.Wednesday, true
	case strings.HasPrefix(s, "четверг"), strings.HasPrefix(s, "thu"):
		return time.Thursday, true
	case strings.HasPrefix(s, "пятниц"), strings.HasPrefix(s, "fr"):
		return time.Friday, true
	case strings.HasPrefix(s, "суббот"), strings.HasPrefix(s, "sat"):
		return time.Saturday, true
	case strings.HasPrefix(s, "воскресень"), strings.HasPrefix(s, "sun"):
		return time.Sunday, true
	}
	return 0, false
}
