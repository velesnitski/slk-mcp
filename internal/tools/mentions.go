package tools

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"
)

// mentionResult is what resolveMentions did to a message: the text to
// send, the people it tagged, and the @words it could not place.
type mentionResult struct {
	text       string
	resolved   []string // "@word → Real Name", in text order
	unresolved []string // "@word", in text order, deduplicated
	ambiguous  []string // "@word (Name A, Name B)", in text order
}

// mentionName is one way a person can be written after "@": the handle,
// the display name or the real name.
type mentionName struct {
	name string
	id   string
}

// resolveMentions turns "@handle", "@Display Name" and "@Real Name" in a
// message into Slack's <@U…> mention tokens. chat.postMessage sends
// plain text as is: a typed "@some.handle" stays text and
// notifies nobody, and Slack's own link_names flag only knows handles,
// while people are known by the names they see. See ADR 137.
//
// Matching is case-insensitive and takes the longest name that fits, so
// "@Sam Lee" beats a user called "Sam". A name must end at a
// word boundary: "@sam" does not match inside "@samuel". A name shared
// by two people is left as typed and reported, never guessed.
//
// Left untouched: "@" inside a word (e-mail addresses), anything inside
// `code` or ```blocks``` or <…> (existing mentions and links), and the
// broadcast words @here, @channel and @everyone, which notify a whole
// channel and should not be produced by accident. Pure.
func resolveMentions(text string, users []goslack.User, groups ...goslack.UserGroup) mentionResult {
	res := mentionResult{text: text}
	if !strings.Contains(text, "@") {
		return res
	}
	names, label := mentionIndex(users, groups...)

	var b strings.Builder
	seenUnresolved := map[string]bool{}
	i := 0
	for i < len(text) {
		// Skip spans Slack already treats as markup or code.
		if end := skipSpan(text, i); end > i {
			b.WriteString(text[i:end])
			i = end
			continue
		}
		if text[i] != '@' || !mentionStart(text, i) {
			_, size := utf8.DecodeRuneInString(text[i:])
			b.WriteString(text[i : i+size])
			i += size
			continue
		}
		rest := text[i+1:]
		word := leadingWord(rest)
		if isBroadcast(word) {
			b.WriteString("@")
			i++
			continue
		}
		n, ids := longestName(rest, names)
		switch {
		case n == 0:
			if word != "" && !seenUnresolved[strings.ToLower(word)] {
				seenUnresolved[strings.ToLower(word)] = true
				res.unresolved = append(res.unresolved, "@"+word)
			}
			b.WriteString("@")
			i++
		case len(ids) > 1:
			who := make([]string, 0, len(ids))
			for _, id := range ids {
				who = append(who, label[id])
			}
			res.ambiguous = append(res.ambiguous, "@"+rest[:n]+" ("+strings.Join(who, ", ")+")")
			b.WriteString(text[i : i+1+n])
			i += 1 + n
		default:
			b.WriteString(mentionToken(ids[0]))
			res.resolved = append(res.resolved, "@"+rest[:n]+" → "+label[ids[0]])
			i += 1 + n
		}
	}
	res.text = b.String()
	return res
}

// mentionIndex lists every name each user can be written by, longest
// first, plus the label used to report who a mention went to.
func mentionIndex(users []goslack.User, groups ...goslack.UserGroup) ([]mentionName, map[string]string) {
	var names []mentionName
	label := map[string]string{}
	for _, u := range users {
		if u.ID == "" {
			continue
		}
		seen := map[string]bool{}
		for _, n := range []string{u.Name, u.Profile.DisplayName, u.RealName, u.Profile.RealName} {
			n = strings.TrimSpace(n)
			k := strings.ToLower(n)
			if n == "" || seen[k] {
				continue
			}
			seen[k] = true
			names = append(names, mentionName{name: n, id: u.ID})
		}
		label[u.ID] = firstNonEmpty(u.RealName, u.Profile.RealName, u.Profile.DisplayName, u.Name, u.ID)
	}
	// A group answers to its handle only: its display name ("Tech leads")
	// reads like prose and would catch ordinary words. ADR 138.
	for _, g := range groups {
		if g.ID == "" || g.Handle == "" || g.DateDelete.Time().Unix() > 0 {
			continue
		}
		names = append(names, mentionName{name: g.Handle, id: g.ID})
		label[g.ID] = fmt.Sprintf("group, %d people", len(g.Users))
	}
	sort.SliceStable(names, func(a, b int) bool { return len(names[a].name) > len(names[b].name) })
	return names, label
}

// longestName returns the byte length of the longest name that rest
// starts with (ending at a word boundary) and every distinct user ID
// written that way.
func longestName(rest string, names []mentionName) (int, []string) {
	best := 0
	var ids []string
	for _, nm := range names {
		l := len(nm.name)
		if l < best {
			break // sorted longest first: nothing shorter can win
		}
		if l > len(rest) || !strings.EqualFold(rest[:l], nm.name) || !wordEnds(rest, l) {
			continue
		}
		if l > best {
			best, ids = l, nil
		}
		if !containsString(ids, nm.id) {
			ids = append(ids, nm.id)
		}
	}
	return best, ids
}

// mentionToken is the Slack markup that notifies id: a person (U…/W…)
// or a user group (S…).
func mentionToken(id string) string {
	if strings.HasPrefix(id, "S") {
		return "<!subteam^" + id + ">"
	}
	return "<@" + id + ">"
}

// mentionStart reports whether the "@" at i opens a mention: it must not
// follow a letter, digit or word character, or it is an e-mail address.
func mentionStart(text string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return !isWordRune(r)
}

// wordEnds reports whether a name of n bytes at the start of rest ends a
// word. A dot or dash ends it only when no word character follows, so
// "@sam." (end of sentence) matches "sam" but "@sam.lee" does not.
func wordEnds(rest string, n int) bool {
	if n >= len(rest) {
		return true
	}
	r, size := utf8.DecodeRuneInString(rest[n:])
	if isWordRune(r) {
		return false
	}
	if r == '.' || r == '-' {
		next, _ := utf8.DecodeRuneInString(rest[n+size:])
		return n+size >= len(rest) || !isWordRune(next)
	}
	return true
}

// leadingWord is the handle-shaped word at the start of s, used to
// report a mention nobody matched.
func leadingWord(s string) string {
	end := 0
	for end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		if isWordRune(r) || ((r == '.' || r == '-') && end+size < len(s) && func() bool {
			next, _ := utf8.DecodeRuneInString(s[end+size:])
			return isWordRune(next)
		}()) {
			end += size
			continue
		}
		break
	}
	return s[:end]
}

// skipSpan returns where a code span, code block or <…> token starting
// at i ends, or i when none starts there. An unclosed opener is not a
// span: the rest of the message is still scanned.
func skipSpan(text string, i int) int {
	switch {
	case strings.HasPrefix(text[i:], "```"):
		if end := strings.Index(text[i+3:], "```"); end >= 0 {
			return i + 3 + end + 3
		}
	case text[i] == '`':
		if end := strings.IndexByte(text[i+1:], '`'); end >= 0 {
			return i + 1 + end + 1
		}
	case text[i] == '<':
		if end := strings.IndexByte(text[i+1:], '>'); end >= 0 && !strings.ContainsAny(text[i+1:i+1+end], "\n<") {
			return i + 1 + end + 1
		}
	}
	return i
}

func isBroadcast(word string) bool {
	switch strings.ToLower(word) {
	case "here", "channel", "everyone":
		return true
	}
	return false
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// mentionNote is the line post_message adds to its result, so the caller
// sees who was tagged and which @words stayed plain text.
func mentionNote(m mentionResult) string {
	var parts []string
	if len(m.resolved) > 0 {
		parts = append(parts, "tagged: "+strings.Join(m.resolved, ", "))
	}
	if len(m.ambiguous) > 0 {
		parts = append(parts, "left as text, more than one person has this name: "+strings.Join(m.ambiguous, "; "))
	}
	if len(m.unresolved) > 0 {
		parts = append(parts, "left as text, no such person: "+strings.Join(m.unresolved, ", "))
	}
	return strings.Join(parts, "\n")
}
