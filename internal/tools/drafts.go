package tools

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/velesnitski/slk-mcp/internal/actions"
)

// Drafts under get_my_actions items (ADR 144): the message the item calls
// for – a reminder that a promise is not forgotten, a nudge to whoever
// owes an answer, an acknowledgement to whoever asked – addressed to the
// right DM or thread, with the post_message arguments to send it. Drafts
// are text in the result; nothing is sent until the operator says so.

var draftNoise = regexp.MustCompile(`<[^>]*>|https?://\S+|\s+`)

// draftQuote is the item's text shortened to a quotable phrase: links and
// Slack markup dropped, at most 70 characters, cut at a word.
func draftQuote(text string) string {
	t := strings.TrimSpace(draftNoise.ReplaceAllStringFunc(text, func(m string) string {
		if strings.TrimSpace(m) == "" {
			return " "
		}
		return ""
	}))
	t = strings.Join(strings.Fields(t), " ")
	r := []rune(t)
	if len(r) <= 70 {
		return t
	}
	cut := string(r[:70])
	if i := strings.LastIndexByte(cut, ' '); i > 30 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, ",.;:–- ") + "…"
}

// isRussian reports whether a text is mostly Cyrillic, so the draft
// answers in the language the item was written in.
func isRussian(text string) bool {
	cyr, lat := 0, 0
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.IsLetter(r):
			lat++
		}
	}
	return cyr > lat
}

type draftKind int

const (
	draftReminder draftKind = iota // you promised: not forgotten
	draftNudge                     // you asked: any news?
	draftAck                       // they asked: seen, answer coming
)

func draftText(kind draftKind, quote string, ru bool) string {
	switch {
	case kind == draftReminder && ru:
		return fmt.Sprintf("Привет! Помню про «%s» – сделаю сегодня и напишу.", quote)
	case kind == draftReminder:
		return fmt.Sprintf("Hi! I haven't forgotten “%s” – I'll get it done today and let you know.", quote)
	case kind == draftNudge && ru:
		return fmt.Sprintf("Привет! Напомню про «%s» – есть новости?", quote)
	case kind == draftNudge:
		return fmt.Sprintf("Hi! A quick nudge on “%s” – any news?", quote)
	case ru:
		return fmt.Sprintf("Увидел про «%s», отвечу сегодня.", quote)
	}
	return fmt.Sprintf("Seen “%s” – I'll come back on it today.", quote)
}

// draftLine renders one draft with the arguments that would send it.
func draftLine(to, channel, threadTS, text string) string {
	args := fmt.Sprintf("channel=%s", channel)
	if threadTS != "" {
		args += " thread_ts=" + threadTS
	}
	return fmt.Sprintf("  ↳ draft to %s (post_message %s): %s", to, args, text)
}

// slackDraft is the draft for a Slack item: in its DM, or in its thread
// in a channel. A promise made in the open channel itself, with no thread
// and no addressee, gets none – there is no one to write to.
func slackDraft(it *actionItem, names map[string]string) string {
	var kind draftKind
	switch it.kind {
	case actions.Promise:
		kind = draftReminder
	case actions.Waiting:
		kind = draftNudge
	case actions.Ask, actions.Question:
		kind = draftAck
	default:
		return ""
	}
	quote := draftQuote(it.hit.Text)
	if quote == "" {
		return ""
	}
	text := draftText(kind, quote, isRussian(it.hit.Text))
	thread := ""
	if it.root != "" && it.root != it.hit.Timestamp {
		thread = it.root
	}
	switch {
	case it.dm && strings.HasPrefix(it.hit.Channel.Name, "U"):
		return draftLine(actorName(names, it.hit.Channel.Name), it.hit.Channel.ID, thread, text)
	case it.dm:
		return draftLine("the DM", it.hit.Channel.ID, thread, text)
	}
	if thread == "" {
		thread = it.hit.Timestamp // answer the message in its thread
	}
	if kind == draftReminder && it.root == it.hit.Timestamp && !strings.Contains(it.hit.Text, "<@") {
		return ""
	}
	return draftLine("the thread in "+whereLabel(it, names), it.hit.Channel.ID, thread, text)
}

// meetingDraft reminds the person a meeting promise was made to, in their
// DM. Only when that person was found in Slack.
func meetingDraft(v meetingVerdict) string {
	if v.handle == "" {
		return ""
	}
	quote := draftQuote(v.item.Text)
	if quote == "" {
		return ""
	}
	return draftLine(v.counterpart, "@"+v.handle, "", draftText(draftReminder, quote, isRussian(v.item.Text)))
}
