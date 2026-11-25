package actions

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Status is where a commitment stands, judged from what followed it.
type Status int

const (
	// Open: nothing after it shows progress.
	Open Status = iota
	// Acked: the person responsible replied but did not say it is done.
	Acked
	// Answered: a question got a reply. Closed.
	Answered
	// Closed: a done reaction or a "done" message. Closed.
	Closed
)

// IsClosed reports whether the commitment needs nothing more.
func (s Status) IsClosed() bool { return s == Answered || s == Closed }

// Msg is the part of a Slack message the resolver reads.
type Msg struct {
	User      string
	Text      string
	TS        float64
	Files     bool
	Reactions map[string][]string // reaction name → users
}

// Outcome is the resolver's verdict on one commitment.
type Outcome struct {
	Status Status
	// By is who moved it last ("" when nobody did); Reply is what they
	// wrote and At when.
	By    string
	Reply string
	At    float64
	// Replies counts the follow-ups from the person responsible.
	Replies int
}

// Resolve judges a commitment from the message itself and the messages
// after it in its thread or conversation. self is the operator.
//
// For a promise, an ask or a question the operator is responsible; for a
// waiting item it is anyone else. A done reaction on the item, or a
// follow-up from the responsible side that says done or carries a file,
// closes it. Any reply settles a question, and an answer that does not
// just accept the work settles an ask put as a question. Otherwise a
// reply from the responsible side only acknowledges it, and silence
// leaves it open.
func Resolve(kind Kind, item Msg, follow []Msg, self string) Outcome {
	responsible := func(user string) bool {
		if user == "USLACKBOT" {
			return false // Slackbot's notices are nobody's reply
		}
		if kind == Waiting {
			return user != "" && user != self
		}
		return user == self
	}
	for name := range item.Reactions {
		if DoneReactions[name] {
			return Outcome{Status: Closed, By: item.Reactions[name][0], Reply: ":" + name + ":"}
		}
	}
	sorted := append([]Msg(nil), follow...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].TS < sorted[j].TS })

	var out Outcome
	for _, m := range sorted {
		if m.TS <= item.TS || !responsible(m.User) {
			continue
		}
		out.Replies++
		if out.Replies == 1 {
			// The first reply is the one that answers it; later ones in a
			// running DM are as often about something else.
			out.By, out.Reply, out.At = m.User, m.Text, m.TS
		}
		switch {
		case Done(m.Text) || m.Files:
			out.Status = Closed
			out.By, out.Reply, out.At = m.User, m.Text, m.TS
			return out
		case kind == Question:
			out.Status = Answered
			return out
		case kind == Waiting && AsksForRef(item.Text) && HasRef(m.Text):
			// "нужен taskid pls" is settled by whoever posts the key or the
			// link, not by a "done" (ADR 150).
			out.Status = Answered
			out.By, out.Reply, out.At = m.User, m.Text, m.TS
			return out
		case kind == Ask && strings.Contains(item.Text, "?") && !accepts(m.Text):
			// "надо презентацию на завтра?" asks a question with a request
			// word in it; "подвинул на пару недель" answers it. Accepting
			// the work ("ок", "сделаю") still leaves it to do.
			out.Status = Answered
			return out
		case kind == Ask && AsksForAnswer(item.Text) && (!accepts(m.Text) || substantive(m.Text)):
			// "Дайте ответ завтра" asks for the reply itself. A real answer
			// often mentions work in progress ("взяли в работу, срок –
			// декабрь"), so a reply of some length settles it even when it
			// contains an acceptance word; a bare "ок" does not.
			out.Status = Answered
			return out
		default:
			out.Status = Acked
		}
	}
	return out
}

// answerRe matches asks whose deliverable is the reply itself – an answer,
// a status, information – rather than work: "дайте плз ответ завтра",
// "подскажи статус", "let me know". "Пришли отчёт" is work and stays out.
var answerRe = regexp.MustCompile(`(?i)` +
	`(^|[^\p{L}])(дай|дайте|пришли|пришлите|скинь|скиньте|напиши|напишите|give|send)(\s+\S+){0,2}?\s+` +
	`(ответ|статус|апдейт|инфо|информаци|фидбек|обратн\S* связь|answer|status|update|feedback)` +
	`|(^|[^\p{L}])(ответь|ответьте|подскажи|подскажите|дай знать|дайте знать|сообщи|сообщите)([^\p{L}]|$)` +
	`|let me know|get back to (me|us)|what'?s the status|какой статус`)

// AsksForAnswer reports whether an ask is settled by a reply that answers
// it, not only by a "done".
func AsksForAnswer(text string) bool { return answerRe.MatchString(normalize(text)) }

// refAskRe matches a request for a reference: a task id, a ticket, a link.
var refAskRe = regexp.MustCompile(`(?i)(task ?id|ticket|link|номер задачи|номер тикета|тикет|ссылк|айди задачи|id задачи|\bid\b)`)

// refRe is a tracker key (PROJ-123) or a link.
var refRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,15}-\d+\b|https?://\S+`)

// AsksForRef reports whether a request asks for a task id or a link.
func AsksForRef(text string) bool { return refAskRe.MatchString(normalize(text)) }

// HasRef reports whether a reply carries a tracker key or a link. The raw
// text is used: keys are upper-case and links are dropped by normalize.
func HasRef(text string) bool { return refRe.MatchString(text) }

// substantiveWords is the length from which a reply to an ask for an answer
// counts as the answer, whatever acceptance words it also carries.
const substantiveWords = 10

func substantive(text string) bool { return len(strings.Fields(text)) >= substantiveWords }

// affirmatives are the short replies that accept a request rather than
// answer it: "да", "ага, сейчас".
var affirmatives = map[string]bool{
	"да": true, "ага": true, "угу": true, "конечно": true, "давай": true, "+": true,
	"yes": true, "yep": true, "yeah": true, "sure": true,
}

// accepts reports whether a reply takes the work on – "ок", "сделаю",
// "да, сейчас" – rather than answering the question the ask was put as.
func accepts(text string) bool {
	t := normalize(text)
	if Ack(t) || promiseRe.MatchString(t) {
		return true
	}
	fields := strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == ',' || r == '!' || r == '.' })
	return len(fields) > 0 && len(fields) <= 3 && affirmatives[fields[0]]
}

// AnsweredByOther settles an ask or a question put to several people
// when one of them other than the operator replied after it, and the
// operator has not: someone else took it. Any other outcome is returned
// unchanged.
func AnsweredByOther(kind Kind, item Msg, follow []Msg, self string, o Outcome) Outcome {
	if (kind != Ask && kind != Question) || o.Status != Open {
		return o
	}
	for _, m := range follow {
		if m.TS > item.TS && m.User != "" && m.User != self && m.User != item.User && m.User != "USLACKBOT" {
			return Outcome{Status: Answered, By: m.User, Reply: m.Text, At: m.TS}
		}
	}
	return o
}

// Stale reports whether an undated commitment has waited long enough to
// need a nudge: open for at least after, measured from when it was made.
func Stale(made time.Time, o Outcome, now time.Time, after time.Duration) bool {
	return o.Status == Open && now.Sub(made) >= after
}
