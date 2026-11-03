package actions

import (
	"sort"
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
// closes it. Any reply settles a question. Otherwise a reply from the
// responsible side only acknowledges it, and silence leaves it open.
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
		out.By, out.Reply, out.At = m.User, m.Text, m.TS
		switch {
		case Done(m.Text) || m.Files:
			out.Status = Closed
			return out
		case kind == Question:
			out.Status = Answered
			return out
		default:
			out.Status = Acked
		}
	}
	return out
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
