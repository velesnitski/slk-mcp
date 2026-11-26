package actions

import "regexp"

// lexicon is one language's phrases for each commitment category. The
// detector matches the union of every language: a workspace mixes them,
// and one message often does too (ADR 153). To add a language, add a
// table and list it in lexicons.
type lexicon struct {
	promise  []string // the writer will do something
	request  []string // the writer asks the reader to do something
	done     []string // a follow-up says the work is finished
	ack      []string // a follow-up only accepts the work
	absence  []string // time off: promise verbs that commit to nothing
	thanks   []string // gratitude: a dated "thanks" is not a request
	urgent   []string // urgency words: due the day said
	polite   []string // politeness removed before judging a redirect
	redirect []string // "ask X instead"; group 1 is the target
	self     []string // targets that point back at the writer
}

var lexicons = []lexicon{lexRU, lexEN}

func collect(pick func(lexicon) []string) []string {
	var out []string
	for _, l := range lexicons {
		out = append(out, pick(l)...)
	}
	return out
}

func join(pick func(lexicon) []string) *regexp.Regexp { return words(collect(pick)...) }

func redirects() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, r := range collect(func(l lexicon) []string { return l.redirect }) {
		out = append(out, regexp.MustCompile(wb+r))
	}
	return out
}
