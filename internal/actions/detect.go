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
	"unicode"
	"unicode/utf8"
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
	// Dated marks that a date phrase was found besides any urgency word.
	Dated bool
}

// IsZero reports whether no deadline was found.
func (d Deadline) IsZero() bool { return d.Day.IsZero() }

// OnlyUrgent reports whether the deadline comes from an urgency word alone
// ("горит", "asap") and no date. Such a word describes a situation as
// often as it asks for something: "а сейчас горит))) Ну работаем) Все ок"
// is a remark, not a request (ADR 150).
func (d Deadline) OnlyUrgent() bool {
	return d.Urgent && !d.Dated
}

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
		if absenceRe.MatchString(t) {
			return None // "возьму полдня больничного" takes time off, owes nothing
		}
		if addressed && requestRe.MatchString(t) && !onlyRedirect(t) {
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
	if requestRe.MatchString(t) {
		return Ask
	}
	if dated && !thanksRe.MatchString(t) {
		// "норм, дедлайн до завтра) мерси!" thanks for something already
		// delivered; the deadline is the writer's own, not a request.
		return Ask
	}
	if strings.Contains(t, "?") {
		return Question
	}
	return None
}

// onlyRedirect reports whether the operator's request only sends the
// other person elsewhere ("по доступам обратись pls к devops", "ask the
// ops team"): nothing is owed back to the operator, so it is not waiting
// on a reply. Pointing someone at the operator ("обратись ко мне", "ask
// me") is not a redirect, and any request left once the redirect and
// politeness words are removed ("ask ops and let me know") still waits.
func onlyRedirect(t string) bool {
	found := false
	rest := t
	for _, re := range redirectRes {
		for _, m := range re.FindAllStringSubmatch(rest, -1) {
			if selfTargetRe.MatchString(m[1]) {
				return false
			}
			found = true
		}
		rest = re.ReplaceAllString(rest, " ")
	}
	if !found {
		return false
	}
	rest = politeRe.ReplaceAllString(rest, " ")
	// A question ("can you check with legal?") expects the answer back.
	return !requestRe.MatchString(rest) && !strings.Contains(t, "?")
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
func Due(text string, at time.Time) Deadline { return due(text, at, false, DayFirst) }

// DueIn is Due with an explicit order for numeric dates: "by 12/3" is
// 12 March day-first and December 3 month-first (ADR 152).
func DueIn(text string, at time.Time, order DateOrder) Deadline {
	return due(text, at, false, order)
}

// DueOfRequest is Due for a request — an ask, a question or the
// operator's own request of someone — where a weak date often belongs to
// something other than the request: the writer's own plan ("завтра
// вышлю файл, посмотри") or the topic of a question ("обзор завтра?").
// Such a weak date is not a deadline; strong ones stand.
func DueOfRequest(text string, at time.Time) Deadline { return due(text, at, true, DayFirst) }

// DueOfRequestIn is DueOfRequest with an explicit numeric date order.
func DueOfRequestIn(text string, at time.Time, order DateOrder) Deadline {
	return due(text, at, true, order)
}

// DateOrder is how a numeric date like 12/3 is read.
type DateOrder int

const (
	// DayFirst reads 12/3 and 12.3 as 12 March (most of the world).
	DayFirst DateOrder = iota
	// MonthFirst reads 12/3 as December 3 (US). A dotted 12.3 stays
	// day-first: dots are the European form even in US-written text.
	MonthFirst
)

// ParseDateOrder reads a SLACK_DATE_ORDER value: "dmy" or "mdy", or a
// locale whose convention implies one ("en-US" is month-first). Empty is
// day-first.
func ParseDateOrder(s string) (DateOrder, bool) {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", "-")) {
	case "", "dmy", "day-first", "d/m":
		return DayFirst, true
	case "mdy", "month-first", "m/d", "us", "en-us":
		return MonthFirst, true
	}
	return DayFirst, false
}

// String is the setting value for o.
func (o DateOrder) String() string {
	if o == MonthFirst {
		return "mdy"
	}
	return "dmy"
}

// numericDate reads a numeric date in order. When that reading is
// impossible (12/25 day-first, 25/12 month-first) the other one is used:
// a date that exists only one way is not ambiguous.
func numericDate(day time.Time, a, sep, b, yy string, order DateOrder) time.Time {
	if order == MonthFirst && sep == "/" {
		if d := dateOf(day, b, a, yy); !d.IsZero() {
			return d
		}
		return dateOf(day, a, b, yy)
	}
	if d := dateOf(day, a, b, yy); !d.IsZero() {
		return d
	}
	return dateOf(day, b, a, yy)
}

func due(text string, at time.Time, request bool, order DateOrder) Deadline {
	t := normalize(text)
	day := midnight(at)
	// The earliest strong deadline wins; a weak one counts only when
	// there is no strong one.
	var best Deadline
	dated, fromUrgent := false, false
	consider := func(d time.Time, phrase string, strong bool) {
		if d.IsZero() || d.Before(day) {
			return
		}
		if request && !strong && weakNarrates(t, phrase) {
			return
		}
		if !fromUrgent {
			dated = true
		}
		if best.IsZero() || (strong && !best.Strong) || (strong == best.Strong && d.Before(best.Day)) {
			best = Deadline{Day: d, Phrase: strings.TrimSpace(phrase), Urgent: best.Urgent, Strong: strong}
		}
	}

	if m := urgentRe.FindString(t); m != "" {
		fromUrgent = true
		consider(day, m, true)
		fromUrgent = false
		best.Urgent = true
	}
	// "Friday EOD" is Friday's end of day, not today's: a relative phrase
	// inside a weekday-part phrase belongs to the weekday.
	parts := weekdayPartRe.FindAllStringIndex(t, -1)
	inPart := func(loc []int) bool {
		for _, p := range parts {
			if loc[0] >= p[0] && loc[1] <= p[1] {
				return true
			}
		}
		return false
	}
	for _, r := range relativeDays {
		if loc := r.re.FindStringIndex(t); loc != nil && !inPart(loc) {
			m := t[loc[0]:loc[1]]
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
	if m := monthEndRe.FindString(t); m != "" {
		// Day 0 of next month is the last day of this one.
		consider(time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, day.Location()), m, true)
	}
	for _, m := range weekdayPartRe.FindAllStringSubmatch(t, -1) {
		if wd, ok := weekdayOf(m[1]); ok {
			// "Friday EOD" names an end; "Monday morning" only a time.
			consider(weekdayOnOrAfter(day, wd), m[0], eodRe.MatchString(m[0]))
		}
	}
	for _, m := range weekdayRe.FindAllStringSubmatch(t, -1) {
		if wd, ok := weekdayOf(m[2]); ok {
			consider(weekdayOnOrAfter(day, wd), m[0], isStrongPrep(m[1]))
		}
	}
	for _, m := range numericDateRe.FindAllStringSubmatch(t, -1) {
		consider(numericDate(day, m[2], m[3], m[4], m[5], order), m[0], true)
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
	best.Dated = dated
	return best
}

// weakNarrates reports whether a weak date phrase in a request belongs
// to something else: a clause where the writer promises their own action
// ("мы сегодня отправим"), or a question that asks for nothing ("мит
// сегодня?" asks about the meeting; "можешь сегодня?" sets a deadline).
func weakNarrates(t, phrase string) bool {
	// The match carries its boundary characters: "сегодня?" → "сегодня".
	phrase = strings.TrimFunc(phrase, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	i := strings.Index(t, phrase)
	if i < 0 {
		return false
	}
	j := i + len(phrase)
	// " - " joins clauses like a comma; the swap keeps byte offsets.
	if promiseRe.MatchString(clause(strings.ReplaceAll(t, " - ", " ; "), i, j, ".!?\n;,—–")) {
		return true
	}
	end := strings.IndexAny(t[j:], ".!?\n")
	if end < 0 || t[j+end] != '?' {
		return false
	}
	return !requestRe.MatchString(clause(t, i, j, ".!?\n"))
}

// clause is the part of t around [i, j) between the nearest separators
// from seps on either side.
func clause(t string, i, j int, seps string) string {
	start := 0
	if k := strings.LastIndexAny(t[:i], seps); k >= 0 {
		_, size := utf8.DecodeRuneInString(t[k:])
		start = k + size
	}
	if k := strings.IndexAny(t[j:], seps); k >= 0 {
		return t[start : j+k]
	}
	return t[start:]
}

// normalize lowers the text and drops link targets. A URL is an address,
// not words: a "?" in its query made a links-only FYI read as a question,
// and a date or a "today" in its path could read as a deadline. A link's
// label is kept – it is what the writer typed (ADR 148).
func normalize(s string) string {
	s = slackLinkRe.ReplaceAllString(s, "$1")
	s = bareURLRe.ReplaceAllString(s, "")
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(s, "ё", "е")
}

var (
	// slackLinkRe is a Slack link: <https://x>, <https://x|label>,
	// <mailto:a@b|a@b>. User and channel references (<@U1>, <#C1|name>)
	// are not links and stay.
	slackLinkRe = regexp.MustCompile(`<(?:https?|mailto|ftp):[^>|]*(?:\|([^>]*))?>`)
	bareURLRe   = regexp.MustCompile(`(?i)\bhttps?://\S+`)
)

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
	promiseRe = join(func(l lexicon) []string { return l.promise })
	requestRe = join(func(l lexicon) []string { return l.request })
	doneRe    = join(func(l lexicon) []string { return l.done })
	ackRe     = join(func(l lexicon) []string { return l.ack })
	absenceRe = join(func(l lexicon) []string { return l.absence })
	thanksRe  = join(func(l lexicon) []string { return l.thanks })
	urgentRe  = join(func(l lexicon) []string { return l.urgent })
	politeRe  = join(func(l lexicon) []string { return l.polite })
	// redirectRes send someone to another person or team; group 1 is the
	// target, checked against selfTargetRe.
	redirectRes  = redirects()
	selfTargetRe = regexp.MustCompile(`^(?:` + strings.Join(collect(func(l lexicon) []string { return l.self }), "|") + `)` + web)

	relativeDays = []struct {
		re     *regexp.Regexp
		days   int
		strong bool
	}{
		{words(`послезавтра`, `day after tomorrow`), 2, false},
		{regexp.MustCompile(wb + `(?:завтра|завтрашн\p{L}*|tomorrow|tomorow|tmrw|tmr|tmrow)` + web), 1, false},
		{words(`tonight`, `eod`, `end of day`, `до конца дня`, `к концу дня`, `до вечера`, `к вечеру`, `до сегодня`, `by today`,
			`cob`, `eob`, `close of business`, `end of business`, `by noon`, `by \d{1,2}(?::\d{2})?\s*(?:am|pm)`), 0, true},
		{words(`this afternoon`, `later today`), 0, false},
		{words(`first thing in the morning`), 1, false},
		{words(`через пару дней`, `in a couple(?: of)? days`), 2, true},
		{words(`через несколько дней`, `in a few days`), 3, false},
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
	// monthEndRe is an explicit end: the month's last day.
	monthEndRe = words(`до конца месяца`, `к концу месяца`, `end of (?:the |this )?month`, `eom`)

	// ordinal is an English day suffix: "Dec 3rd", "the 1st of June".
	ordinal      = `(?:st|nd|rd|th)?`
	weekdayNames = `понедельник\p{L}*|вторник\p{L}*|сред[ауы]|четверг\p{L}*|пятниц\p{L}*|суббот\p{L}*|воскресень\p{L}*|mon(?:day)?|tue(?:s|sday)?|wed(?:nesday)?|thu(?:r|rs|rsday)?|fr+i(?:day)?|sat(?:urday)?|sun(?:day)?`
	prep         = `(?:до|к|ко|в|во|на|by|until|till|til|before|on|this|next|deadline|дедлайн|срок)`
	datePrep     = `(?:до|к|ко|by|until|till|til|before|on|deadline|дедлайн|срок)`
	weekdayRe    = regexp.MustCompile(wb + `(` + prep + `)\s+(` + weekdayNames + `)` + web)
	// weekdayPartRe is a weekday with a part of the day and no preposition,
	// "Monday morning", "Friday EOD": a weak date.
	eodRe         = words(`eod`, `end of day`)
	weekdayPartRe = regexp.MustCompile(wb + `(` + weekdayNames + `)\s+(?:morning|afternoon|evening|eod|end of day|утром|днем|вечером)` + web)

	numericDateRe = regexp.MustCompile(wb + `(` + datePrep + `)\s*:?\s*(\d{1,2})([./])(\d{1,2})(?:[./](\d{2}|\d{4}))?` + web)
	monthDayRe    = regexp.MustCompile(wb + `(` + datePrep + `)\s+(?:the\s+)?(\d{1,2})` + ordinal + `\s+(?:of\s+)?(январ\p{L}*|феврал\p{L}*|март\p{L}*|апрел\p{L}*|ма[яй]|июн\p{L}*|июл\p{L}*|август\p{L}*|сентябр\p{L}*|октябр\p{L}*|ноябр\p{L}*|декабр\p{L}*|jan\p{L}*|feb\p{L}*|mar\p{L}*|apr\p{L}*|may|jun\p{L}*|jul\p{L}*|aug\p{L}*|sep\p{L}*|oct\p{L}*|nov\p{L}*|dec\p{L}*)` + web)
	dayMonthEnRe  = regexp.MustCompile(wb + `(?:by|until|till|before|on)\s+(jan\p{L}*|feb\p{L}*|mar\p{L}*|apr\p{L}*|may|jun\p{L}*|jul\p{L}*|aug\p{L}*|sep\p{L}*|oct\p{L}*|nov\p{L}*|dec\p{L}*)\.?\s+(\d{1,2})` + ordinal + `,?` + web)
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
