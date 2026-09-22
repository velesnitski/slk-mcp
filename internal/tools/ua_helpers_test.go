package tools

import (
	"context"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// uaMsgOf builds a goslack.Message from the fields these helpers read.
func uaMsgOf(ts, user, text string) goslack.Message {
	var m goslack.Message
	m.Timestamp = ts
	m.User = user
	m.Text = text
	return m
}

// uaCU builds a ChannelUnread with an explicit channel id/name.
func uaCU(id, name string, msgs []goslack.Message, replies map[string][]goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{Messages: msgs, Replies: replies}
	cu.Channel.ID = id
	cu.Channel.Name = name
	return cu
}

// ------------------------------------------------------ merge helpers

func TestMergeDMOverride_SkipsNilAndIDLessEntries(t *testing.T) {
	base := []*slack.ChannelUnread{
		nil,
		uaCU("C1", "alpha", []goslack.Message{uaMsgOf("1.0", "U2", "kept")}, nil),
		uaCU("D1", "", []goslack.Message{uaMsgOf("2.0", "U2", "stale view")}, nil),
	}
	override := []*slack.ChannelUnread{
		nil,
		uaCU("", "", nil, nil), // no id: cannot index, must not replace anything
		uaCU("D1", "", []goslack.Message{uaMsgOf("3.0", "U2", "fresh view")}, nil),
		uaCU("D2", "", []goslack.Message{uaMsgOf("4.0", "U3", "new dm")}, nil),
	}

	got := mergeDMOverride(base, override)
	if len(got) != 4 {
		t.Fatalf("want 4 entries (2 kept + 1 replaced + 1 appended), got %d", len(got))
	}
	if got[1].Messages[0].Text != "fresh view" {
		t.Fatalf("the override must replace the stale DM view, got %q", got[1].Messages[0].Text)
	}
	if got[0].Messages[0].Text != "kept" {
		t.Fatalf("non-DM entries must pass through untouched")
	}
	if got[3].Channel.ID != "D2" {
		t.Fatalf("a DM only the override knows about must be appended, got %q", got[3].Channel.ID)
	}
}

func TestMergeDMOverride_EmptyOverrideIsIdentity(t *testing.T) {
	base := []*slack.ChannelUnread{uaCU("C1", "alpha", nil, nil)}
	if got := mergeDMOverride(base, nil); len(got) != 1 || got[0] != base[0] {
		t.Fatalf("an empty override must return base unchanged")
	}
}

func TestMergeThreadMentions_AugmentsWithoutReplacing(t *testing.T) {
	existing := uaCU("C1", "alpha",
		[]goslack.Message{uaMsgOf("1.0", "U2", "already swept")},
		nil)
	base := []*slack.ChannelUnread{nil, uaCU("", "", nil, nil), existing}

	mentions := []*slack.ChannelUnread{
		nil,
		uaCU("", "", nil, nil), // id-less hit is skipped
		uaCU("C1", "alpha",
			[]goslack.Message{
				uaMsgOf("1.0", "U2", "already swept"), // duplicate ts → dropped
				uaMsgOf("2.0", "U3", "new top-level"),
			},
			map[string][]goslack.Message{"1.0": {uaMsgOf("1.5", "U3", "new reply")}}),
		uaCU("C9", "gamma", []goslack.Message{uaMsgOf("3.0", "U3", "unswept channel")}, nil),
	}

	got := mergeThreadMentions(base, mentions)
	if len(got) != 4 {
		t.Fatalf("base keeps its entries and gains the new channel, got %d", len(got))
	}
	if len(existing.Messages) != 2 {
		t.Fatalf("duplicate timestamps must not pile up, got %d messages", len(existing.Messages))
	}
	if existing.Replies == nil || len(existing.Replies["1.0"]) != 1 {
		t.Fatalf("a nil Replies map must be created to hold the mention, got %+v", existing.Replies)
	}

	// Re-running the same merge must be idempotent.
	mergeThreadMentions(got, mentions)
	if len(existing.Messages) != 2 || len(existing.Replies["1.0"]) != 1 {
		t.Fatalf("a repeated sweep must not duplicate messages or replies")
	}
}

func TestMergeThreadMentions_EmptyMentionsIsIdentity(t *testing.T) {
	base := []*slack.ChannelUnread{uaCU("C1", "alpha", nil, nil)}
	if got := mergeThreadMentions(base, nil); len(got) != 1 {
		t.Fatalf("no mentions must return base unchanged, got %d", len(got))
	}
}

func TestBudgetAppend_CapDropsRatherThanTruncates(t *testing.T) {
	var b strings.Builder
	if !budgetAppend(&b, "abcd", 0) {
		t.Fatal("maxChars=0 must disable the cap")
	}
	if b.String() != "abcd\n\n" {
		t.Fatalf("separator missing, got %q", b.String())
	}

	var tight strings.Builder
	// 4 chars + the 2-char separator is exactly the budget → fits.
	if !budgetAppend(&tight, "abcd", 6) {
		t.Fatal("a channel that exactly fits must be emitted")
	}
	// Nothing more fits, and the partial content must not be written.
	before := tight.Len()
	if budgetAppend(&tight, "e", 6) {
		t.Fatal("a channel past the cap must be reported as dropped")
	}
	if tight.Len() != before {
		t.Fatalf("a dropped channel must not leave a partial write: %q", tight.String())
	}
}

// ------------------------------------------------------ cursor helpers

func TestNewestTS_IgnoresUnparseableTimestamps(t *testing.T) {
	results := []*slack.ChannelUnread{
		uaCU("C1", "alpha",
			[]goslack.Message{uaMsgOf("not-a-ts", "U2", "x"), uaMsgOf("1700000001.000000", "U2", "y")},
			map[string][]goslack.Message{"1700000001.000000": {uaMsgOf("1700000009.000000", "U3", "z")}}),
	}
	if got := newestTS(results); got != "1700000009.000000" {
		t.Fatalf("newestTS = %q; want the newest reply", got)
	}
	if got := newestTS([]*slack.ChannelUnread{uaCU("C1", "alpha", nil, nil)}); got != "" {
		t.Fatalf("nothing placeable in time must yield an empty cursor, got %q", got)
	}
}

func TestFilterAfter_KeepsChannelsSurvivingOnRepliesOnly(t *testing.T) {
	cu := uaCU("C1", "alpha",
		[]goslack.Message{uaMsgOf("1700000000.000000", "U2", "old parent")},
		map[string][]goslack.Message{
			"1700000000.000000": {uaMsgOf("1700000900.000000", "U3", "fresh reply")},
			"1699999000.000000": {uaMsgOf("1699999100.000000", "U3", "stale reply")},
		})
	got := filterAfter([]*slack.ChannelUnread{cu}, "1700000500.000000")

	if len(got) != 1 {
		t.Fatalf("a channel alive only through a reply must be kept, got %d", len(got))
	}
	if len(got[0].Messages) != 0 {
		t.Fatalf("pre-cursor top-level messages must be dropped, got %d", len(got[0].Messages))
	}
	if _, stale := got[0].Replies["1699999000.000000"]; stale {
		t.Fatal("a thread whose replies are all pre-cursor must be removed entirely")
	}
	if len(got[0].Replies["1700000000.000000"]) != 1 {
		t.Fatal("the fresh reply must survive")
	}
}

func TestFilterAfter_EmptyCursorIsANoOp(t *testing.T) {
	in := []*slack.ChannelUnread{uaCU("C1", "alpha", []goslack.Message{uaMsgOf("1.0", "U2", "x")}, nil)}
	if got := filterAfter(in, "   "); len(got) != 1 || len(got[0].Messages) != 1 {
		t.Fatalf("a blank cursor must not filter anything, got %+v", got)
	}
}

// --------------------------------------------------- answered-DM probe

func TestIsAnsweredDM_GuardsNilAndNonDM(t *testing.T) {
	window := []goslack.Message{uaMsgOf("2.0", "U1", "my reply")}
	if isAnsweredDM(nil, window, "U1") {
		t.Fatal("a nil conversation is never answered")
	}
	if isAnsweredDM(uaCU("D1", "", nil, nil), nil, "U1") {
		t.Fatal("an empty window is never answered")
	}
	if isAnsweredDM(uaCU("D1", "", nil, nil), window, "") {
		t.Fatal("without a self id nothing can be judged answered")
	}
	if isAnsweredDM(uaCU("C1", "alpha", nil, nil), window, "U1") {
		t.Fatal("a regular channel is out of scope for the answered probe")
	}
}

func TestIsAnsweredDM_OperatorAbsentFromWindowIsNotAnswered(t *testing.T) {
	dm := uaCU("D1", "", nil, nil)
	// Nothing but the counterpart's acks; the operator never spoke in the
	// window, so there is no reply to hide the conversation behind.
	window := []goslack.Message{
		uaMsgOf("3.0", "U2", "спасибо"),
		uaMsgOf("2.0", "U2", "ok"),
	}
	if isAnsweredDM(dm, window, "U1") {
		t.Fatal("an all-ack window with no operator message must stay visible")
	}
}

func TestIsAnsweredDM_LiveThreadKeepsTheDMVisible(t *testing.T) {
	dm := uaCU("D1", "", nil, map[string][]goslack.Message{
		"1.0": {
			uaMsgOf("1.1", "U1", "my thread reply"),
			uaMsgOf("1.2", "U2", "but what about the amount?"),
		},
	})
	window := []goslack.Message{uaMsgOf("9.0", "U1", "unrelated newer top-level line")}
	if isAnsweredDM(dm, window, "U1") {
		t.Fatal("a question still open in a thread lane must keep the DM visible")
	}
}

func TestDropAnsweredDMs_SkipsNilAndFailsOpenOnProbeError(t *testing.T) {
	boom := func(context.Context, string) ([]goslack.Message, error) {
		return nil, errString("history unavailable")
	}
	dm := uaCU("D1", "", []goslack.Message{uaMsgOf("1.0", "U2", "a live question")}, nil)
	kept, answered := dropAnsweredDMs(context.Background(), boom, "U1",
		[]*slack.ChannelUnread{nil, dm})

	if len(answered) != 0 {
		t.Fatalf("a failed probe must never hide a DM, got %d hidden", len(answered))
	}
	if len(kept) != 1 || kept[0] != dm {
		t.Fatalf("nil entries are skipped and the DM is kept, got %+v", kept)
	}
}

// ------------------------------------------------------ mention render

func TestSummarizeMentions_FallsBackToIDsWhenNamesAreMissing(t *testing.T) {
	var anon goslack.SearchMessage
	anon.Timestamp = "1.0"
	anon.Channel.ID = "C5" // no name → the id is the label

	var named goslack.SearchMessage
	named.Timestamp = "2.0"
	named.Username = "Sam"
	named.Channel.ID = "C5"
	named.Channel.Name = "alpha"

	out := summarizeMentions([]goslack.SearchMessage{anon, named}, 24, true)

	if !strings.Contains(out, "2 mentions (last 24h) — pending only — summary") {
		t.Fatalf("pending-only summary header wrong:\n%s", out)
	}
	if !strings.Contains(out, "(unknown)×1") {
		t.Fatalf("an unattributable sender must be labelled, not dropped:\n%s", out)
	}
	if !strings.Contains(out, "#C5×1") || !strings.Contains(out, "#alpha×1") {
		t.Fatalf("channel labels should fall back to the id:\n%s", out)
	}
}

func TestSummarizeMentions_EmptySetStillRenders(t *testing.T) {
	out := summarizeMentions(nil, 12, false)
	if !strings.Contains(out, "0 mentions (last 12h) — summary") {
		t.Fatalf("an empty summary must still state zero:\n%s", out)
	}
	if !strings.Contains(out, "split: 0 DM / 0 channel · 0 unique senders") {
		t.Fatalf("the split line must be present even at zero:\n%s", out)
	}
	if strings.Contains(out, "senders:") || strings.Contains(out, "channels:") {
		t.Fatalf("empty count maps must render no list at all:\n%s", out)
	}
}

func TestDMActivityToHits_RefusesWithoutSelfIDAndSkipsOwnLines(t *testing.T) {
	cus := []*slack.ChannelUnread{
		nil,
		uaCU("", "", []goslack.Message{uaMsgOf("1.0", "U2", "no channel id")}, nil),
		uaCU("D1", "", []goslack.Message{
			uaMsgOf("2.0", "U1", "my own line"),
			uaMsgOf("3.0", "", "no author"),
			uaMsgOf("4.0", "U2", "their line"),
		}, map[string][]goslack.Message{"4.0": {uaMsgOf("5.0", "U2", "their reply")}}),
	}

	if got := dmActivityToHits(cus, ""); got != nil {
		t.Fatalf("without a self id the backstop must refuse, got %d hits", len(got))
	}

	got := dmActivityToHits(cus, "U1")
	if len(got) != 2 {
		t.Fatalf("only the counterpart's messages become hits, got %d: %+v", len(got), got)
	}
	var reply goslack.SearchMessage
	for _, h := range got {
		if h.Timestamp == "5.0" {
			reply = h
		}
	}
	if !strings.Contains(reply.Permalink, "?thread_ts=4.0") {
		t.Fatalf("a reply hit must carry its thread root in the permalink, got %q", reply.Permalink)
	}
}

func TestWriteContextLines_SkipsDuplicatesAndContentlessMessages(t *testing.T) {
	var b strings.Builder
	shown := map[string]struct{}{"C1|1.0": {}}
	msgs := []goslack.Message{
		uaMsgOf("1.0", "U2", "already shown above"),
		uaMsgOf("2.0", "U2", ""), // no body, no files, no reactions
		uaMsgOf("3.0", "U2", "real context"),
		uaMsgOf("3.0", "U2", "same ts, second copy"),
	}
	writeContextLines(&b, "    ↳ ", msgs, map[string]string{"U2": "Sam"}, "C1", shown)

	out := b.String()
	if strings.Contains(out, "already shown above") {
		t.Fatalf("a message already rendered for this channel must be skipped:\n%s", out)
	}
	if strings.Contains(out, "same ts, second copy") {
		t.Fatalf("dedup is by channel+ts:\n%s", out)
	}
	if strings.Count(out, "    ↳ ") != 1 || !strings.Contains(out, "real context") {
		t.Fatalf("exactly the one content-carrying line should render:\n%s", out)
	}
	if !strings.Contains(out, "Sam") {
		t.Fatalf("the author name should be resolved:\n%s", out)
	}
}

// ------------------------------------------------ reference collection

func TestCollectChannelIDsWithReplies_DedupesAcrossMessagesAndReplies(t *testing.T) {
	cu := uaCU("C1", "alpha",
		[]goslack.Message{
			uaMsgOf("1.0", "U2", "see <#C0AAAAAAA|alpha> and <#C0BBBBBBB>"),
			uaMsgOf("2.0", "U2", "again <#C0AAAAAAA|alpha>"),
		},
		map[string][]goslack.Message{
			"1.0": {uaMsgOf("1.5", "U3", "also <#C0CCCCCCC> and <#C0BBBBBBB>")},
		})

	got := collectChannelIDsWithReplies(cu)
	if len(got) != 3 {
		t.Fatalf("want 3 unique channel refs, got %d: %v", len(got), got)
	}
	if got[0] != "C0AAAAAAA" || got[1] != "C0BBBBBBB" || got[2] != "C0CCCCCCC" {
		t.Fatalf("order should follow first sight (messages then replies), got %v", got)
	}
	if len(collectChannelIDsWithReplies(uaCU("C1", "alpha", nil, nil))) != 0 {
		t.Fatal("a channel with no refs must yield none")
	}
}

func TestCollectUserIDsWithReplies_IncludesAuthorsAndMentionedIDs(t *testing.T) {
	cu := uaCU("C1", "alpha",
		[]goslack.Message{
			uaMsgOf("1.0", "U2", "hello <@U9>"),
			uaMsgOf("2.0", "", "no author"),
			uaMsgOf("3.0", "U2", "again <@U9>"),
		},
		map[string][]goslack.Message{
			"1.0": {uaMsgOf("1.5", "U3", "and <@U8>")},
		})

	got := collectUserIDsWithReplies(cu)
	want := map[string]bool{"U2": true, "U3": true, "U9": true, "U8": true}
	if len(got) != len(want) {
		t.Fatalf("want %d unique ids, got %d: %v", len(want), len(got), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected id %q in %v", id, got)
		}
	}
}

// ----------------------------------------- hub-backed helper behaviour

func TestResolveRefsWithReplies_MergesUserAndChannelNames(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.uaUsers(map[string]string{"U2": "Sam", "U3": "Pat"})
	f.uaInfo(t, map[string]map[string]any{
		"C0AAAAAAA": uaChannel("C0AAAAAAA", "beta", nil),
	})
	h := uaHub(t, f)

	cu := uaCU("C1", "alpha",
		[]goslack.Message{uaMsgOf("1.0", "U2", "look at <#C0AAAAAAA>")},
		map[string][]goslack.Message{"1.0": {uaMsgOf("1.5", "U3", "agreed")}})

	got := h.resolveRefsWithReplies(context.Background(), cu)
	if got["U2"] != "Sam" || got["U3"] != "Pat" {
		t.Fatalf("author names should resolve, got %+v", got)
	}
	if got["C0AAAAAAA"] == "" {
		t.Fatalf("channel refs should resolve into the same map, got %+v", got)
	}
}

func TestOperatorRepliedSince_GuardsAndErrorPaths(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaHub(t, f)
	ctx := context.Background()

	mk := func(channelID, ts, permalink string) goslack.SearchMessage {
		var m goslack.SearchMessage
		m.Channel.ID = channelID
		m.Timestamp = ts
		m.Permalink = permalink
		return m
	}

	if operatorRepliedSince(ctx, h.Messages(), h.Log(), mk("C1", "not-a-ts", ""), "U1") {
		t.Fatal("an unplaceable mention cannot be judged replied-to")
	}
	if operatorRepliedSince(ctx, h.Messages(), h.Log(), mk("", "1700000000.000000", ""), "U1") {
		t.Fatal("no channel id → cannot check")
	}
	if operatorRepliedSince(ctx, h.Messages(), h.Log(), mk("C1", "1700000000.000000", ""), "") {
		t.Fatal("no self id → cannot check")
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("the guards must short-circuit before any Slack call, got %v", f.Calls())
	}

	// Both lookups failing must read as "not replied", not as "replied".
	f.OnError("conversations.history", "channel_not_found")
	f.OnError("conversations.replies", "thread_not_found")
	if operatorRepliedSince(ctx, h.Messages(), h.Log(), mk("C1", "1700000000.000000", ""), "U1") {
		t.Fatal("upstream failures must not be read as an operator reply")
	}
	if !f.Called("conversations.history") || !f.Called("conversations.replies") {
		t.Fatalf("both lookups should have been attempted, got %v", f.Calls())
	}
}

func TestOperatorRepliedSince_FindsTheReplyInsideAThread(t *testing.T) {
	f := uaNewFakeSlack(t)
	root := uaTS(-900, 100)
	mention := uaTS(-800, 100)

	f.uaHistory(t, map[string][]map[string]any{}) // top level has nothing newer
	f.uaReplies(t, map[string][]map[string]any{
		root: {
			uaMsg(mention, "U2", "the ask", nil),
			uaMsg(uaTS(-700, 100), "U1", "answered in-thread", nil),
		},
	})
	h := uaHub(t, f)

	var m goslack.SearchMessage
	m.Channel.ID = "C1"
	m.Timestamp = mention
	m.Permalink = "https://example.slack.com/archives/C1/p1?thread_ts=" + root

	if !operatorRepliedSince(context.Background(), h.Messages(), h.Log(), m, "U1") {
		t.Fatal("a reply that only exists inside the thread must still count")
	}
}

func TestFetchMentionContext_DefaultsAndFailuresStayBestEffort(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.OnError("conversations.history", "channel_not_found")
	h := uaHub(t, f)

	before, after := h.fetchMentionContext(context.Background(), "C1", "1700000000.000000", 0)
	if len(before) != 0 || len(after) != 0 {
		t.Fatalf("a failed history read must return empty context, got %d/%d", len(before), len(after))
	}
	if !f.Called("conversations.history") {
		t.Fatal("the before-window should still have been attempted")
	}
}

func TestFetchMentionContext_UnplaceablePivotSkipsTheAfterWindow(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {uaMsg("1700000000.000000", "U2", "some line", nil)},
	})
	h := uaHub(t, f)

	before, after := h.fetchMentionContext(context.Background(), "C1", "not-a-ts", 2)
	if len(after) != 0 {
		t.Fatalf("an unparseable pivot must skip the forward fetch, got %d", len(after))
	}
	if len(before) != 1 {
		t.Fatalf("the backward window still applies, got %d", len(before))
	}
	if f.CountOf("conversations.history") != 1 {
		t.Fatalf("only one history call should be made, got %d", f.CountOf("conversations.history"))
	}
}

func TestFetchMentionContext_OrdersBothWindowsOldestFirst(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg("1700000400.000000", "U2", "after-2", nil),
			uaMsg("1700000300.000000", "U2", "after-1", nil),
			uaMsg("1700000200.000000", "U2", "pivot", nil),
			uaMsg("1700000100.000000", "U2", "before-2", nil),
			uaMsg("1700000000.000000", "U2", "before-1", nil),
		},
	})
	h := uaHub(t, f)

	before, after := h.fetchMentionContext(context.Background(), "C1", "1700000200.000000", 2)
	if len(before) != 2 || before[0].Text != "before-1" || before[1].Text != "before-2" {
		t.Fatalf("before window should read oldest→newest, got %+v", before)
	}
	if len(after) != 2 || after[0].Text != "after-1" || after[1].Text != "after-2" {
		t.Fatalf("after window should read oldest→newest, got %+v", after)
	}
}

func TestChannelDisplayLabel_FallsBackWhenNothingIsNamed(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaHub(t, f)
	ctx := context.Background()

	var im goslack.Channel
	im.IsIM = true
	if got := channelDisplayLabel(ctx, im, h.Users()); got != "@?" {
		t.Fatalf("a DM with no counterpart id should read %q, got %q", "@?", got)
	}

	var mpim goslack.Channel
	mpim.IsMpIM = true
	if got := channelDisplayLabel(ctx, mpim, h.Users()); got != "mpdm-?" {
		t.Fatalf("an unnamed group DM should read %q, got %q", "mpdm-?", got)
	}

	if got := channelDisplayLabel(ctx, goslack.Channel{}, h.Users()); got != "#?" {
		t.Fatalf("an unnamed channel should read %q, got %q", "#?", got)
	}
}
