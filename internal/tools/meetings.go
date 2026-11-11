package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/format"
)

// Meeting commitments in get_my_actions. A meeting-notes tool (wispr-mcp
// in headless mode, or anything printing the same JSON) supplies what the
// operator promised on calls; slk-mcp lists those next to the Slack ones,
// drops the ones Slack already holds, and marks the ones a later Slack
// message looks like it delivered. The notes tool never sees Slack and
// Slack never sees the meeting: the command's JSON is the whole contract.
// See ADR 141.

// meetingItem is one commitment as the notes tool reports it. Only the
// fields used here are decoded; unknown fields are ignored.
type meetingItem struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Mine         bool     `json:"mine"`
	Text         string   `json:"text"`
	Due          string   `json:"due"`
	DuePhrase    string   `json:"due_phrase"`
	DueApprox    bool     `json:"due_approx"`
	Counterparts []string `json:"counterparts"`
	MeetingTitle string   `json:"meeting_title"`
	MeetingStart string   `json:"meeting_start"`
	MeetingEnd   string   `json:"meeting_end"`
	ShareURL     string   `json:"share_url"`

	start, end time.Time
	due        time.Time
}

// meetingSource fetches the operator's meeting commitments of the last
// days. nil when no command is configured.
type meetingSource func(ctx context.Context, days int) ([]meetingItem, error)

// meetingTimeout bounds the notes command; it reads a local database and
// answers in well under a second.
const meetingTimeout = 30 * time.Second

// commandMeetingSource runs cmdline (split on spaces, no shell) with
// `since=<days>d` appended and parses its stdout: either the headless
// envelope {"ok":…,"items":[…]} or a bare {"items":[…]}.
func commandMeetingSource(cmdline string) meetingSource {
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return nil
	}
	return func(ctx context.Context, days int) ([]meetingItem, error) {
		ctx, cancel := context.WithTimeout(ctx, meetingTimeout)
		defer cancel()
		args := append(append([]string(nil), fields[1:]...), fmt.Sprintf("since=%dd", days))
		cmd := exec.CommandContext(ctx, fields[0], args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr := cmd.Run()
		items, parseErr := parseMeetingItems(stdout.Bytes())
		if parseErr == nil {
			// A tool error still prints its JSON envelope; trust the parse.
			return items, nil
		}
		if runErr != nil {
			msg := headLine(stderr.String())
			if msg == "" {
				msg = runErr.Error()
			}
			return nil, fmt.Errorf("%s: %s", fields[0], msg)
		}
		return nil, parseErr
	}
}

// headLine is the first line of s, or "" when s is blank.
func headLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func parseMeetingItems(raw []byte) ([]meetingItem, error) {
	var env struct {
		OK    *bool         `json:"ok"`
		Text  string        `json:"text"`
		Items []meetingItem `json:"items"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &env); err != nil {
		return nil, fmt.Errorf("meeting notes: not JSON (%v)", err)
	}
	if env.OK != nil && !*env.OK {
		return nil, fmt.Errorf("meeting notes: %s", headLine(env.Text))
	}
	out := env.Items[:0]
	for _, it := range env.Items {
		it.start, _ = time.Parse(time.RFC3339, it.MeetingStart)
		it.end, _ = time.Parse(time.RFC3339, it.MeetingEnd)
		if it.end.IsZero() {
			it.end = it.start
		}
		if it.Due != "" {
			if d, err := time.ParseInLocation("2006-01-02", it.Due, time.Local); err == nil {
				it.due = d
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// --- matching ------------------------------------------------------------

// stems are the content words of a text, cut to five runes so inflected
// Russian forms meet ("ссылку", "ссылка" → "ссылк") and English plurals
// too. Short words and names of people are noise for matching.
func stems(text string, skip map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		r := []rune(w)
		if len(r) < 4 || stopStems[w] {
			continue
		}
		if len(r) > 5 {
			r = r[:5]
		}
		s := string(r)
		if stopStems[s] || skip[s] {
			continue
		}
		out[s] = true
	}
	return out
}

// stopStems are frequent words that say nothing about what was promised.
var stopStems = map[string]bool{
	"this": true, "that": true, "with": true, "from": true, "will": true, "have": true,
	"about": true, "after": true, "today": true, "tomor": true, "please": true, "pleas": true,
	"send": true, "check": true, "write": true, "сегод": true, "завтр": true, "чтобы": true,
	"будет": true, "можно": true, "нужно": true, "надо": true, "пожал": true, "этого": true,
	"когда": true, "после": true, "тоже": true, "также": true, "очень": true, "всех": true,
	"проверить": true, "провер": true, "написа": true, "напис": true, "отпра": true,
}

func overlap(a, b map[string]bool) int {
	n := 0
	for s := range a {
		if b[s] {
			n++
		}
	}
	return n
}

// nameSkip turns people's names into stems to ignore: "send Pat the
// link" and "Pat, here is the link" share Pat, not the commitment.
func nameSkip(names ...string) map[string]bool {
	skip := map[string]bool{}
	for _, n := range names {
		for s := range stems(n, nil) {
			skip[s] = true
		}
	}
	return skip
}

var linkWords = []string{"ссылк", "link", "url", "доступ", "access", "пример", "example", "файл", "file", "отчёт", "отчет", "report", "доку", "doc"}

// delivered reports whether a message the operator sent after the meeting
// looks like it fulfils the item: two content words in common, or the
// item is about handing something over and the message carries a link or
// an attachment.
func delivered(item meetingItem, itemStems map[string]bool, m goslack.SearchMessage, skip map[string]bool) bool {
	if overlap(itemStems, stems(m.Text, skip)) >= 2 {
		return true
	}
	if !strings.Contains(m.Text, "http") && len(m.Attachments) == 0 {
		return false
	}
	lower := strings.ToLower(item.Text)
	for _, w := range linkWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// rosterMatch finds the Slack user a meeting participant name refers to.
// Platforms record the name as written in the call ("Pat Novak"); the
// roster has handles, display and real names. An exact name first, then
// every name part present in the real name (platforms swap the order).
func rosterMatch(name string, users []goslack.User) (goslack.User, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return goslack.User{}, false
	}
	parts := strings.Fields(want)
	var exact, partial []goslack.User
	for _, u := range users {
		if u.IsBot || u.Deleted {
			continue
		}
		hit := false
		for _, n := range []string{u.RealName, u.Profile.RealName, u.Profile.DisplayName, u.Name} {
			if strings.ToLower(strings.TrimSpace(n)) == want {
				hit = true
				break
			}
		}
		if hit {
			exact = append(exact, u)
			continue
		}
		if len(parts) < 2 {
			continue
		}
		real := strings.ToLower(u.RealName + " " + u.Profile.RealName)
		all := true
		for _, p := range parts {
			if !strings.Contains(real, p) {
				all = false
				break
			}
		}
		if all {
			partial = append(partial, u)
		}
	}
	// One person, or no guess: a name two people share would put the
	// evidence search in the wrong DM.
	switch {
	case len(exact) == 1:
		return exact[0], true
	case len(exact) == 0 && len(partial) == 1:
		return partial[0], true
	}
	return goslack.User{}, false
}

// --- evaluation ----------------------------------------------------------

// meetingVerdict is what Slack says about one meeting commitment.
type meetingVerdict struct {
	item        meetingItem
	counterpart string // display name for the line
	duplicate   bool   // the same commitment is already a Slack item
	evidence    *goslack.SearchMessage
	workspace   string
}

// wsRoster is a workspace with its user list, for counterpart lookups.
type wsRoster struct {
	name  string
	hub   *Hub
	users []goslack.User
	items []*actionItem // the Slack commitments found there
}

// judgeMeetings checks each mine/action meeting item against Slack: a
// Slack item saying the same thing makes it a duplicate; a later DM to its
// counterpart that delivers it is evidence it was done.
func judgeMeetings(ctx context.Context, items []meetingItem, rosters []wsRoster) []meetingVerdict {
	var mine []meetingItem
	for _, it := range items {
		if it.Mine && (it.Kind == "" || it.Kind == "action") && strings.TrimSpace(it.Text) != "" {
			mine = append(mine, it)
		}
	}
	verdicts := make([]meetingVerdict, len(mine))
	jobs := make(chan func(), len(mine))
	var mu sync.Mutex
	for i, it := range mine {
		i, it := i, it
		verdicts[i].item = it
		jobs <- func() {
			v := judgeOne(ctx, it, rosters)
			mu.Lock()
			verdicts[i] = v
			mu.Unlock()
		}
	}
	close(jobs)
	runJobs(ctx, jobs)
	return verdicts
}

func judgeOne(ctx context.Context, it meetingItem, rosters []wsRoster) meetingVerdict {
	v := meetingVerdict{item: it}
	if len(it.Counterparts) > 0 {
		v.counterpart = it.Counterparts[0]
	}
	skip := nameSkip(it.Counterparts...)
	itemStems := stems(it.Text, skip)

	// The workspace where the counterpart is a person; the first one when
	// nobody is named, so the duplicate check still runs.
	var ws *wsRoster
	var who goslack.User
	for i := range rosters {
		if v.counterpart == "" {
			break
		}
		if u, ok := rosterMatch(v.counterpart, rosters[i].users); ok {
			ws, who = &rosters[i], u
			break
		}
	}

	for i := range rosters {
		for _, si := range rosters[i].items {
			if sameCommitment(it, itemStems, si, skip, who.ID) {
				v.duplicate, v.workspace = true, rosters[i].name
				return v
			}
		}
	}
	if ws == nil || who.Name == "" || len(itemStems) == 0 {
		return v
	}
	v.workspace = ws.name
	after := it.start.AddDate(0, 0, -1).Format("2006-01-02")
	hits, err := ws.hub.Search().MessagesPages(ctx, fmt.Sprintf("from:me in:@%s after:%s", who.Name, after), 50, 1)
	if err != nil {
		return v
	}
	sort.Slice(hits, func(a, b int) bool { return hits[a].Timestamp < hits[b].Timestamp })
	for i := range hits {
		if format.ParseTS(hits[i].Timestamp).Before(it.start) {
			continue
		}
		if delivered(it, itemStems, hits[i], skip) {
			v.evidence = &hits[i]
			return v
		}
	}
	return v
}

// sameCommitment reports whether a Slack item restates a meeting one:
// within two days of the call, two content words in common, and – when
// the meeting named whom it was for – that person is in the Slack item.
func sameCommitment(it meetingItem, itemStems map[string]bool, si *actionItem, skip map[string]bool, counterpartID string) bool {
	if it.start.IsZero() || si.at.Before(it.start.AddDate(0, 0, -1)) || si.at.After(it.end.AddDate(0, 0, 2)) {
		return false
	}
	if overlap(itemStems, stems(si.hit.Text, skip)) < 2 {
		return false
	}
	if counterpartID == "" {
		return true
	}
	if si.hit.User == counterpartID || si.hit.Channel.Name == counterpartID {
		return true
	}
	return strings.Contains(si.hit.Text, "<@"+counterpartID)
}

// --- rendering -----------------------------------------------------------

// renderMeetings writes the "From meetings" section for the view: open
// items in the due window, then the likely-done ones with their evidence.
// Duplicates of Slack items are only counted – the Slack line is richer.
func renderMeetings(verdicts []meetingVerdict, p actionParams, multi bool) string {
	today := midnightOf(p.now)
	horizon := today
	if p.due == "week" {
		horizon = today.AddDate(0, 0, 7)
	}
	var open, done []meetingVerdict
	dups, later := 0, 0
	for _, v := range verdicts {
		switch {
		case v.duplicate:
			dups++
		case v.evidence != nil:
			done = append(done, v)
		case p.due == "all" || (!v.item.due.IsZero() && !v.item.due.After(horizon)):
			open = append(open, v)
		default:
			later++
		}
	}
	sort.SliceStable(open, func(i, j int) bool {
		a, b := open[i].item, open[j].item
		if a.due.IsZero() != b.due.IsZero() {
			return !a.due.IsZero()
		}
		if !a.due.Equal(b.due) {
			return a.due.Before(b.due)
		}
		return a.start.Before(b.start)
	})

	var b strings.Builder
	if len(open) > 0 {
		fmt.Fprintf(&b, "### From meetings (%d)\n", len(open))
		for _, v := range open {
			b.WriteString(meetingLine(v, p))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	if len(done) > 0 {
		fmt.Fprintf(&b, "### From meetings – probably done, check (%d)\n", len(done))
		for _, v := range done {
			b.WriteString(meetingLine(v, p))
			e := v.evidence
			text := strings.Join(strings.Fields(e.Text), " ")
			if r := []rune(text); len(r) > 80 {
				text = string(r[:80]) + "…"
			}
			fmt.Fprintf(&b, " · ✅ you wrote %s %s: \"%s\"", e.Channel.Name, format.ParseTS(e.Timestamp).Format("02.01 15:04"), text)
			if e.Permalink != "" {
				b.WriteString(" · " + e.Permalink)
			}
			if multi && v.workspace != "" {
				b.WriteString(" [" + v.workspace + "]")
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	var notes []string
	if later > 0 {
		notes = append(notes, fmt.Sprintf("%d more from meetings without a date or due later (due=all lists them)", later))
	}
	if dups > 0 {
		notes = append(notes, fmt.Sprintf("%d from meetings also found in Slack and listed there", dups))
	}
	if b.Len() == 0 && len(notes) == 0 {
		return ""
	}
	if len(notes) > 0 {
		b.WriteString(strings.Join(notes, " · "))
		b.WriteByte('\n')
	}
	return b.String()
}

func meetingLine(v meetingVerdict, p actionParams) string {
	it := v.item
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(meetingDueBadge(it, p))
	b.WriteString(" · you promised")
	if v.counterpart != "" {
		b.WriteString(" " + v.counterpart)
	}
	b.WriteString(" · meeting")
	if title := strings.TrimSpace(it.MeetingTitle); title != "" {
		fmt.Fprintf(&b, " “%s”", title)
	}
	if !it.start.IsZero() {
		b.WriteString(" " + it.start.Local().Format("02.01 15:04"))
	}
	text := strings.Join(strings.Fields(it.Text), " ")
	if r := []rune(text); len(r) > 160 {
		text = string(r[:160]) + "…"
	}
	fmt.Fprintf(&b, ": \"%s\"", text)
	if it.DuePhrase != "" {
		fmt.Fprintf(&b, " (“%s”)", it.DuePhrase)
	}
	if it.ShareURL != "" {
		b.WriteString(" · " + it.ShareURL)
	}
	return b.String()
}

func meetingDueBadge(it meetingItem, p actionParams) string {
	if it.due.IsZero() {
		return "· no date, " + ageLabel(int(p.now.Sub(it.start).Hours())) + " old"
	}
	days := int(midnightOf(p.now).Sub(it.due).Hours() / 24)
	approx := ""
	if it.DueApprox {
		approx = "≈"
	}
	switch {
	case days > 0:
		return fmt.Sprintf("⏰ overdue %s%dd", approx, days)
	case days == 0:
		return "📅 due today"
	case days == -1:
		return "📅 due tomorrow"
	}
	return "📅 due " + approx + it.due.Format("Mon 02.01")
}

// meetingsSection fetches, judges and renders the meeting commitments.
// Failures never cost the Slack part: they become a one-line note.
func (h *Hub) meetingsSection(ctx context.Context, p actionParams, rosters []wsRoster, multi bool) string {
	if p.meetings == nil {
		return ""
	}
	items, err := p.meetings(ctx, p.days)
	if err != nil {
		return "_meetings not included: " + err.Error() + "_\n"
	}
	for i := range rosters {
		if rosters[i].users == nil {
			rosters[i].users, _ = rosters[i].hub.Users().List(ctx)
		}
	}
	return renderMeetings(judgeMeetings(ctx, items, rosters), p, multi)
}

// rosterFor builds the lookup entry for one swept workspace.
func rosterFor(name string, hub *Hub, items []*actionItem) wsRoster {
	return wsRoster{name: name, hub: hub, items: items}
}
