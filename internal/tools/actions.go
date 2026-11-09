package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/actions"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// get_my_actions answers the morning question: what do I owe today, what
// is owed to me, and whom should I ping. It reads the operator's own
// messages and the messages addressed to them, keeps those that promise
// or ask something, reads each deadline relative to when it was written,
// and judges from what followed whether it is still open. See ADR 133.

func (h *Hub) registerActionTools(s *server.MCPServer) {
	h.register(s, toolDef{
		Name: "get_my_actions",
		Description: "Your open commitments from Slack: what you promised (\"I'll send it by Friday\"), what others asked of you, and what you asked of others and are still waiting for — each with the deadline its text names, read relative to when it was written (\"until tomorrow\" said yesterday is due today), and judged open or done from what followed in its thread or DM. " +
			"due=today (default) lists what is overdue or due today, plus undated asks nobody answered and requests of yours nobody replied to for stale_hours — the ping list; due=week looks 7 days ahead; due=all lists every open item. Found by phrase rules (Russian and English), not by a model: a commitment worded unusually can be missed, and the output says so.",
		Opts: []mcp.ToolOption{
			mcp.WithString("due", mcp.Description("today (default) | week | all")),
			mcp.WithNumber("days", mcp.Description("How far back to look for commitments, in days (default: 7, max 30)")),
			mcp.WithNumber("stale_hours", mcp.Description("An undated ask or request with no reply for this long is listed for a ping (default: 24)")),
			mcp.WithNumber("limit", mcp.Description("Max commitments checked per workspace, newest first (default: 60)")),
			mcp.WithString("workspace", mcp.Description(workspaceArgAll)),
		},
		RequiresUserToken: true,
		Handle:            h.handleMyActions,
	})
}

type actionParams struct {
	due        string
	days       int
	staleHours int
	limit      int
	now        time.Time
}

func (h *Hub) handleMyActions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p := actionParams{
		due:        strings.ToLower(strings.TrimSpace(req.GetString("due", "today"))),
		days:       int(req.GetFloat("days", 7)),
		staleHours: int(req.GetFloat("stale_hours", 24)),
		limit:      int(req.GetFloat("limit", 60)),
		now:        time.Now(),
	}
	switch p.due {
	case "", "today":
		p.due = "today"
	case "week", "all":
	default:
		return mcp.NewToolResultError("due must be today, week or all"), nil
	}
	if p.days <= 0 {
		p.days = 7
	}
	if p.days > 30 {
		p.days = 30
	}
	if p.staleHours <= 0 {
		p.staleHours = 24
	}
	if p.limit <= 0 {
		p.limit = 60
	}
	return h.runMyActions(ctx, p, req.GetString("workspace", "")), nil
}

func (h *Hub) runMyActions(ctx context.Context, p actionParams, workspace string) *mcp.CallToolResult {
	targets, excluded := h.sweepTargets(workspace)
	if targets == nil {
		return mcp.NewToolResultError(unknownWorkspaceMsg(workspace, h.workspaceNames()))
	}
	fetchCtx, cancel := sweepContext(ctx, h.cfg.SweepTimeoutSeconds)
	defer cancel()

	type wsActions struct {
		noToken bool
		body    string
		err     error
	}
	results := fanOut(targets, func(ws slack.Workspace) wsActions {
		scoped := h.withClient(ws.Client)
		if !scoped.client.HasUserToken() {
			return wsActions{noToken: true}
		}
		sw := newSweep(fetchCtx, scoped.log, "get_my_actions", ws.Name)
		body, err := scoped.myActions(ctx, sw, p)
		return wsActions{body: body, err: err}
	})

	multi := len(targets) > 1 || len(excluded) > 0
	var sections []string
	for i, ws := range targets {
		r := results[i]
		body := r.body
		switch {
		case r.noToken:
			body = "_skipped: no user token for this workspace_"
		case r.err != nil && !multi:
			return mcp.NewToolResultError(r.err.Error())
		case r.err != nil:
			body = "_error: " + r.err.Error() + "_"
		}
		if !multi {
			sections = append(sections, body)
			continue
		}
		sections = append(sections, workspaceSection(ws.Name, body))
	}
	header := fmt.Sprintf("# My actions — %s · %s", dueLabel(p.due), p.now.Format("Mon 02.01"))
	out := header + "\n\n" + strings.Join(sections, "\n\n")
	if note := excludedNote(excluded); note != "" {
		out = strings.TrimRight(out, "\n") + "\n\n" + note
	}
	out = strings.TrimRight(out, "\n") + "\n\n_Found by phrase rules, not a model: a commitment worded unusually can be missed._"
	return mcp.NewToolResultText(out)
}

func dueLabel(due string) string {
	switch due {
	case "week":
		return "due within a week"
	case "all":
		return "all open"
	}
	return "due today"
}

// actionItem is one commitment found in a workspace.
type actionItem struct {
	kind    actions.Kind
	hit     goslack.SearchMessage
	ts      float64
	at      time.Time
	root    string // thread root ts; == hit.Timestamp for a top-level message
	dm      bool
	due     actions.Deadline
	outcome actions.Outcome
	checked bool // its follow-ups were read
}

func (it *actionItem) channel() string { return it.hit.Channel.ID }

// myActions collects, resolves and renders one workspace's commitments.
func (h *Hub) myActions(ctx context.Context, sw *sweep, p actionParams) (string, error) {
	selfID, err := h.Unread().Self(ctx)
	if err != nil || selfID == "" {
		return "", fmt.Errorf("get_my_actions needs auth.test to name you: %v", err)
	}
	handle, _ := h.Unread().SelfHandle(ctx)
	after := p.now.AddDate(0, 0, -p.days-1).Format("2006-01-02")

	// The searches are independent; run them side by side.
	queries := append([]string{"from:me after:" + after}, slack.MentionQueries(handle, after)...)
	hits := make([][]goslack.SearchMessage, len(queries))
	errs := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q string) {
			defer wg.Done()
			errs[i] = sw.run(fmt.Sprintf("search-%d", i+1), func(c context.Context) error {
				r, e := h.Search().MessagesPages(c, q, searchPageMax, searchPages)
				hits[i] = r
				return e
			})
		}(i, q)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil && !sw.incomplete() {
			return "", e
		}
	}
	var toMe []goslack.SearchMessage
	for _, hs := range hits[1:] {
		toMe = mergeSearchHits(toMe, hs)
	}
	toMe = filterOwnMessages(toMe, selfID)

	items := classifyHits(hits[0], toMe, selfID, h.botIDs(ctx))
	sort.Slice(items, func(i, j int) bool { return items[i].ts > items[j].ts })
	if len(items) > p.limit {
		items = items[:p.limit]
	}

	_ = sw.run("follow-ups", func(c context.Context) error {
		h.resolveItems(c, items, selfID)
		return c.Err()
	})
	items = dropAnsweringPromises(items)

	var truncated []string
	for i, hs := range hits {
		if len(hs) < searchPageMax*searchPages {
			continue
		}
		what := "messages to you"
		if i == 0 {
			what = "your messages"
		}
		truncated = append(truncated, fmt.Sprintf("%s only since %s", what, oldestHit(hs).Format("02.01 15:04")))
	}
	return h.renderActions(ctx, items, p, selfID, truncated) + sw.note(false), nil
}

// searchPageMax is the most hits one search.messages page returns, and
// searchPages how many pages a query reads: an active operator sends more
// than 100 messages in a week.
const (
	searchPageMax = 100
	searchPages   = 5
)

// isReport reports whether a message is a report — an automated daily
// digest, a status write-up — whose "urgent" and "сегодня" are content,
// not deadlines. A commitment is a few sentences, sometimes with a short
// list; a report runs to many lines or thousands of characters.
func isReport(text string) bool {
	return len([]rune(text)) > 2000 || strings.Count(text, "\n") > 15
}

// oldestHit is the time of the oldest search hit, the real start of the
// window a truncated search covered.
func oldestHit(hits []goslack.SearchMessage) time.Time {
	var oldest time.Time
	for _, m := range hits {
		if t := format.ParseTS(m.Timestamp); !t.IsZero() && (oldest.IsZero() || t.Before(oldest)) {
			oldest = t
		}
	}
	return oldest
}

// classifyHits turns search hits into commitments: own messages that
// promise or request, and messages to the operator that ask. bots are
// the workspace's bot and app users, whose messages and DMs commit
// nobody.
func classifyHits(own, toMe []goslack.SearchMessage, selfID string, bots map[string]bool) []*actionItem {
	var out []*actionItem
	add := func(m goslack.SearchMessage, isOwn bool) {
		if m.User == "" || bots[m.User] {
			return // bots and integrations promise nothing
		}
		dm := isDMHit(m)
		peer := ""
		if dm && !m.Channel.IsMPIM {
			peer = m.Channel.Name
		}
		if peer == selfID || bots[peer] || isReport(m.Text) {
			return // notes to self, bot DMs and reports
		}
		ts, err := strconv.ParseFloat(m.Timestamp, 64)
		if err != nil {
			return
		}
		at := format.ParseTS(m.Timestamp)
		var addressed bool
		switch {
		case isOwn && dm && !m.Channel.IsMPIM:
			addressed = true
		case isOwn:
			addressed = mentionsOther(m.Text, selfID)
		case m.Channel.IsMPIM:
			// A group chat talks to everyone in it; only a mention
			// singles the operator out.
			addressed = strings.Contains(m.Text, "<@"+selfID)
		default:
			addressed = true
		}
		due := actions.Due(m.Text, at)
		kind := actions.Classify(m.Text, isOwn, addressed, due.Strong)
		if kind == actions.None {
			return
		}
		out = append(out, &actionItem{
			kind: kind, hit: m, ts: ts, at: at, root: format.ExtractThreadTS(m), dm: dm, due: due,
		})
	}
	for _, m := range own {
		add(m, true)
	}
	for _, m := range toMe {
		add(m, false)
	}
	return out
}

// botIDs returns the workspace's bot and app users. A failed roster read
// yields none: bot messages are then judged by their text like any other.
func (h *Hub) botIDs(ctx context.Context) map[string]bool {
	users, err := h.Users().List(ctx)
	if err != nil {
		return nil
	}
	out := map[string]bool{"USLACKBOT": true}
	for _, u := range users {
		if u.IsBot || u.IsAppUser {
			out[u.ID] = true
		}
	}
	return out
}

// direct reports whether a commitment was made to the operator alone: a
// one-to-one DM, or a channel message whose only mention is them. A
// question put to five people in a channel is not theirs to answer, so
// it is not listed for a ping when nobody does.
func direct(it *actionItem, selfID string) bool {
	if it.dm {
		return !it.hit.Channel.IsMPIM
	}
	for _, m := range userRefRe.FindAllStringSubmatch(it.hit.Text, -1) {
		if m[1] != selfID {
			return false
		}
	}
	return true
}

func isDMHit(m goslack.SearchMessage) bool {
	return strings.HasPrefix(m.Channel.ID, "D") || m.Channel.IsMPIM
}

var userRefRe = regexp.MustCompile(`<@([UW][A-Z0-9]+)`)

// mentionsOther reports whether text @-mentions anyone but self.
func mentionsOther(text, selfID string) bool {
	for _, m := range userRefRe.FindAllStringSubmatch(text, -1) {
		if m[1] != selfID {
			return true
		}
	}
	return false
}

// resolveItems reads what followed each commitment and judges it. Each
// thread is read once, and each DM once from its oldest commitment on,
// however many commitments they hold.
func (h *Hub) resolveItems(ctx context.Context, items []*actionItem, selfID string) {
	type key struct{ channel, root string }
	threads := map[key]bool{}
	dmOldest := map[string]float64{}
	chanOldest := map[string]float64{}
	for _, it := range items {
		if !it.dm || it.root != it.hit.Timestamp {
			threads[key{it.channel(), it.root}] = true
			if !it.dm && it.root == it.hit.Timestamp && (it.kind == actions.Ask || it.kind == actions.Question) {
				// Asked at a channel's top level, it is often answered
				// there too rather than in a thread.
				if o, ok := chanOldest[it.channel()]; !ok || it.ts < o {
					chanOldest[it.channel()] = it.ts
				}
			}
			continue
		}
		if o, ok := dmOldest[it.channel()]; !ok || it.ts < o {
			dmOldest[it.channel()] = it.ts
		}
	}

	var mu sync.Mutex
	threadMsgs := map[key][]goslack.Message{}
	dmMsgs := map[string][]goslack.Message{}
	chanMsgs := map[string][]goslack.Message{}
	jobs := make(chan func(), len(threads)+len(dmOldest)+len(chanOldest))
	for ch, oldest := range chanOldest {
		ch, oldest := ch, oldest
		jobs <- func() {
			msgs, err := h.Messages().History(ctx, slack.HistoryParams{ChannelID: ch, OldestTS: oldest, Limit: 200})
			if err != nil {
				return
			}
			mu.Lock()
			chanMsgs[ch] = msgs
			mu.Unlock()
		}
	}
	for k := range threads {
		k := k
		jobs <- func() {
			msgs, err := h.Messages().ThreadReplies(ctx, k.channel, k.root)
			if err != nil {
				return
			}
			mu.Lock()
			threadMsgs[k] = msgs
			mu.Unlock()
		}
	}
	for ch, oldest := range dmOldest {
		ch, oldest := ch, oldest
		jobs <- func() {
			// Just before the oldest commitment, so the page holds it
			// and its reactions too.
			msgs, err := h.Messages().History(ctx, slack.HistoryParams{ChannelID: ch, OldestTS: oldest - 0.000001, Limit: 200})
			if err != nil {
				return
			}
			mu.Lock()
			dmMsgs[ch] = msgs
			mu.Unlock()
		}
	}
	close(jobs)
	const workers = 4
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					continue
				}
				job()
			}
		}()
	}
	wg.Wait()

	candidates := nextTurnCandidates(items)
	for _, it := range items {
		var msgs []goslack.Message
		var ok bool
		if it.dm && it.root == it.hit.Timestamp {
			msgs, ok = dmMsgs[it.channel()]
		} else {
			msgs, ok = threadMsgs[key{it.channel(), it.root}]
		}
		if !ok {
			continue
		}
		item := actions.Msg{User: it.hit.User, Text: it.hit.Text, TS: it.ts}
		all := make([]actions.Msg, 0, len(msgs))
		for _, m := range msgs {
			am := toActionMsg(m)
			if m.Timestamp == it.hit.Timestamp {
				item.Reactions = am.Reactions
				continue
			}
			all = append(all, am)
		}
		cut := turnCutoff(it, candidates[it], all, selfID)
		follow := all[:0:0]
		for _, am := range all {
			if cut > 0 && am.TS >= cut {
				continue // the next turn's reply, not this one's
			}
			follow = append(follow, am)
		}
		if !it.dm && it.root == it.hit.Timestamp {
			follow = append(follow, channelAnswers(chanMsgs[it.channel()], it.ts, selfID)...)
		}
		it.outcome = actions.Resolve(it.kind, item, follow, selfID)
		if !direct(it, selfID) {
			// Put to several people, it is answered when any of them
			// answers.
			it.outcome = actions.AnsweredByOther(it.kind, item, follow, selfID, it.outcome)
		}
		it.checked = true
	}
}

// channelAnswerWindow is how soon after a top-level channel question the
// operator's own top-level message in that channel counts as the answer.
// Longer, and an unrelated later post would settle it.
const channelAnswerWindow = 3 * 3600.0

// channelAnswers picks the operator's top-level posts in a channel within
// channelAnswerWindow after ts: "демка на 11:30?" is usually answered in
// the channel, not in a thread, and the thread read alone missed it.
func channelAnswers(msgs []goslack.Message, ts float64, selfID string) []actions.Msg {
	var out []actions.Msg
	for _, m := range msgs {
		am := toActionMsg(m)
		if m.User == selfID && am.TS > ts && am.TS <= ts+channelAnswerWindow && (m.ThreadTimestamp == "" || m.ThreadTimestamp == m.Timestamp) {
			out = append(out, am)
		}
	}
	return out
}

// nextTurnCandidates lists, per commitment, the later commitments that
// may start a new turn: the next items of the same kind from the same
// person in the same DM or thread. Whether one actually does depends on
// what was said in between (turnCutoff).
func nextTurnCandidates(items []*actionItem) map[*actionItem][]float64 {
	type group struct{ channel, root string }
	byGroup := map[group][]*actionItem{}
	for _, it := range items {
		g := group{it.channel(), it.root}
		if it.dm && it.root == it.hit.Timestamp {
			g.root = "" // a DM's top level is one conversation
		}
		byGroup[g] = append(byGroup[g], it)
	}
	out := map[*actionItem][]float64{}
	for _, list := range byGroup {
		sort.Slice(list, func(i, j int) bool { return list[i].ts < list[j].ts })
		for i, it := range list {
			for _, next := range list[i+1:] {
				if next.hit.User == it.hit.User && family(next.kind) == family(it.kind) {
					out[it] = append(out[it], next.ts)
				}
			}
		}
	}
	return out
}

// turnCutoff bounds what may count as the reply to a commitment. A reply
// after the next turn belongs to that turn: "предупредите, когда будет
// свободно" answered today's ask, not yesterday's. But a follow-up from
// the same person before the other side has said anything is the same
// turn continued — a clarification after a question ("мы так и написали,
// надо сказать что-то") must not cut the answer that came after both.
// So the cutoff is the first candidate that the other side answered
// something before; 0 means no cutoff.
func turnCutoff(it *actionItem, candidates []float64, msgs []actions.Msg, selfID string) float64 {
	otherSide := func(user string) bool {
		if user == "" || user == "USLACKBOT" {
			return false
		}
		if it.kind == actions.Ask || it.kind == actions.Question {
			return user == selfID
		}
		return user != selfID
	}
	for _, c := range candidates {
		for _, m := range msgs {
			if m.TS > it.ts && m.TS < c && otherSide(m.User) {
				return c
			}
		}
	}
	return 0
}

// family groups the kinds whose replies look alike: an ask and a
// question both wait on the operator's answer.
func family(k actions.Kind) actions.Kind {
	if k == actions.Question {
		return actions.Ask
	}
	return k
}

func toActionMsg(m goslack.Message) actions.Msg {
	ts, _ := strconv.ParseFloat(m.Timestamp, 64)
	am := actions.Msg{User: m.User, Text: m.Text, TS: ts, Files: len(m.Files) > 0}
	if len(m.Reactions) > 0 {
		am.Reactions = make(map[string][]string, len(m.Reactions))
		for _, r := range m.Reactions {
			am.Reactions[r.Name] = r.Users
		}
	}
	return am
}

// dropAnsweringPromises removes an own promise that is the reply to an
// ask in the list: "will investigate" under "think about X until Friday"
// is the same commitment, and the ask carries its deadline.
func dropAnsweringPromises(items []*actionItem) []*actionItem {
	answers := map[string]bool{}
	for _, it := range items {
		if (it.kind == actions.Ask || it.kind == actions.Question) && it.outcome.At > 0 {
			answers[it.channel()+"/"+strconv.FormatFloat(it.outcome.At, 'f', 6, 64)] = true
		}
	}
	out := items[:0]
	for _, it := range items {
		if it.kind == actions.Promise && answers[it.channel()+"/"+strconv.FormatFloat(it.ts, 'f', 6, 64)] {
			continue
		}
		out = append(out, it)
	}
	return out
}

// actionBucket is where an item is listed.
type actionBucket int

const (
	bucketSkip  actionBucket = iota
	bucketDue                // overdue or due in the window
	bucketPing               // undated, nobody moved it for stale_hours
	bucketLater              // open, outside the window or undated
)

// bucketFor decides whether an open item is listed in this view.
func bucketFor(it *actionItem, p actionParams, selfID string) actionBucket {
	if it.outcome.Status.IsClosed() {
		return bucketSkip
	}
	today := midnightOf(p.now)
	horizon := today
	if p.due == "week" {
		horizon = today.AddDate(0, 0, 7)
	}
	if !it.due.IsZero() {
		// Acknowledged and two days past its date, the moment has passed:
		// "сможешь на мит сегодня?" — "чуть позже" a week ago.
		if it.outcome.Status == actions.Acked && it.due.Day.Before(today.AddDate(0, 0, -2)) {
			return bucketLater
		}
		if !it.due.Day.After(horizon) {
			return bucketDue
		}
		return bucketLater
	}
	stale := actions.Stale(it.at, it.outcome, p.now, time.Duration(p.staleHours)*time.Hour)
	if stale && (it.kind == actions.Waiting || (it.kind != actions.Promise && direct(it, selfID))) {
		return bucketPing
	}
	return bucketLater
}

func midnightOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func (h *Hub) renderActions(ctx context.Context, items []*actionItem, p actionParams, selfID string, truncated []string) string {
	ids := map[string]struct{}{}
	var idList []string
	addID := func(id string) {
		if _, ok := ids[id]; !ok && id != "" {
			ids[id] = struct{}{}
			idList = append(idList, id)
		}
	}
	for _, it := range items {
		addID(it.hit.User)
		addID(it.outcome.By)
		if it.dm && strings.HasPrefix(it.hit.Channel.Name, "U") {
			addID(it.hit.Channel.Name)
		}
		for _, id := range format.CollectRefIDsInText(it.hit.Text + " " + it.outcome.Reply) {
			addID(id)
		}
	}
	names := h.Users().NamesFor(ctx, idList)

	var yours, waiting, later []*actionItem
	closed := 0
	for _, it := range items {
		switch bucketFor(it, p, selfID) {
		case bucketSkip:
			closed++
		case bucketDue, bucketPing:
			if it.kind == actions.Waiting {
				waiting = append(waiting, it)
			} else {
				yours = append(yours, it)
			}
		case bucketLater:
			later = append(later, it)
		}
	}
	order := func(list []*actionItem) {
		sort.SliceStable(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if a.due.Urgent != b.due.Urgent {
				return a.due.Urgent
			}
			if a.due.IsZero() != b.due.IsZero() {
				return !a.due.IsZero()
			}
			if !a.due.Day.Equal(b.due.Day) {
				return a.due.Day.Before(b.due.Day)
			}
			return a.ts < b.ts
		})
	}
	order(yours)
	order(waiting)
	order(later)

	var b strings.Builder
	write := func(title string, list []*actionItem) {
		if len(list) == 0 {
			return
		}
		fmt.Fprintf(&b, "### %s (%d)\n", title, len(list))
		for _, it := range list {
			b.WriteString(actionLine(it, p, names, selfID))
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	write("On you", yours)
	write("Waiting on others — ping", waiting)
	if p.due == "all" {
		write("Open, no date or later", later)
	}

	if b.Len() == 0 {
		b.WriteString("nothing due — no open commitment found for this view\n")
	}
	var notes []string
	if n := len(later); n > 0 && p.due != "all" {
		notes = append(notes, fmt.Sprintf("%d more open without a date or due later (due=all lists them)", n))
	}
	if closed > 0 {
		notes = append(notes, fmt.Sprintf("%d done or answered", closed))
	}
	unchecked := 0
	for _, it := range items {
		if !it.checked {
			unchecked++
		}
	}
	if unchecked > 0 {
		notes = append(notes, fmt.Sprintf("%d not checked for replies (shown as open)", unchecked))
	}
	if len(truncated) > 0 {
		notes = append(notes, fmt.Sprintf("search page limit (%d hits) reached: read %s — older commitments may be missing; lower days to cover the window fully", searchPageMax*searchPages, strings.Join(truncated, ", ")))
	}
	if len(notes) > 0 {
		b.WriteString(strings.Join(notes, " · "))
		b.WriteByte('\n')
	}
	return b.String()
}

// actionLine renders one commitment:
//
//   - ⏰ overdue 1d · asked of you · DM alice: "until tmrw the write-up…" · you replied 12:03: "in process…" · <permalink>
func actionLine(it *actionItem, p actionParams, names map[string]string, selfID string) string {
	var b strings.Builder
	b.WriteString("- ")
	b.WriteString(dueBadge(it, p))
	b.WriteString(" · ")
	b.WriteString(kindLabel(it, names, selfID))
	b.WriteString(" · ")
	b.WriteString(whereLabel(it, names))
	b.WriteString(": \"")
	text := strings.Join(strings.Fields(format.RenderText(it.hit.Text, names)), " ")
	if r := []rune(text); len(r) > 160 {
		text = string(r[:160]) + "…"
	}
	b.WriteString(text)
	b.WriteString("\"")
	if it.due.Phrase != "" {
		fmt.Fprintf(&b, " (“%s”)", it.due.Phrase)
	}
	if o := it.outcome; o.Status == actions.Acked {
		who := "you"
		if o.By != selfID {
			who = actorName(names, o.By)
		}
		reply := strings.Join(strings.Fields(format.RenderText(o.Reply, names)), " ")
		if r := []rune(reply); len(r) > 80 {
			reply = string(r[:80]) + "…"
		}
		fmt.Fprintf(&b, " · %s replied %s: \"%s\" — not marked done", who, format.ClockLabel(format.ParseTS(strconv.FormatFloat(o.At, 'f', 6, 64))), reply)
	}
	if it.hit.Permalink != "" {
		b.WriteString(" · ")
		b.WriteString(it.hit.Permalink)
	}
	return b.String()
}

func dueBadge(it *actionItem, p actionParams) string {
	today := midnightOf(p.now)
	if it.due.IsZero() {
		hours := int(p.now.Sub(it.at).Hours())
		if it.outcome.Status == actions.Open {
			return fmt.Sprintf("💤 no reply %s", ageLabel(hours))
		}
		return fmt.Sprintf("· no date, %s old", ageLabel(hours))
	}
	days := int(today.Sub(it.due.Day).Hours() / 24)
	prefix := ""
	if it.due.Urgent {
		prefix = "🔥 urgent · "
	}
	switch {
	case days > 0:
		return fmt.Sprintf("%s⏰ overdue %dd", prefix, days)
	case days == 0:
		return prefix + "📅 due today"
	case days == -1:
		return prefix + "📅 due tomorrow"
	}
	return prefix + "📅 due " + it.due.Day.Format("Mon 02.01")
}

func ageLabel(hours int) string {
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd", hours/24)
}

func kindLabel(it *actionItem, names map[string]string, selfID string) string {
	switch it.kind {
	case actions.Promise:
		return "you promised"
	case actions.Ask:
		return "asked of you by " + actorName(names, it.hit.User)
	case actions.Question:
		return "asked you by " + actorName(names, it.hit.User)
	case actions.Waiting:
		var who []string
		for _, m := range userRefRe.FindAllStringSubmatch(it.hit.Text, -1) {
			if m[1] != selfID {
				who = append(who, actorName(names, m[1]))
			}
		}
		if len(who) == 0 && it.dm && strings.HasPrefix(it.hit.Channel.Name, "U") {
			who = append(who, actorName(names, it.hit.Channel.Name))
		}
		if len(who) == 0 {
			return "you asked"
		}
		return "you asked " + strings.Join(who, ", ")
	}
	return it.kind.String()
}

func whereLabel(it *actionItem, names map[string]string) string {
	switch {
	case it.hit.Channel.IsMPIM:
		return "group DM"
	case it.dm && strings.HasPrefix(it.hit.Channel.Name, "U"):
		return "DM " + actorName(names, it.hit.Channel.Name)
	case it.dm:
		return "DM"
	case it.hit.Channel.Name != "":
		return "#" + it.hit.Channel.Name
	}
	return it.hit.Channel.ID
}

func actorName(names map[string]string, id string) string {
	if n := names[id]; n != "" {
		return n
	}
	if id == "" {
		return "?"
	}
	return id
}
