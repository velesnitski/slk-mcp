package tools

import (
	"reflect"
	"sort"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/digest"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func mkMsg(ts, user, text string) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = ts
	m.User = user
	m.Text = text
	return m
}

func mkChannelUnread(name string, msgs []goslack.Message, replies map[string][]goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{
		LastRead: "1700000000.000000",
		Messages: msgs,
		Replies:  replies,
	}
	cu.Channel.Name = name
	cu.Channel.ID = "C_" + name
	return cu
}

// ----------------------- channelMentions -----------------------

func TestChannelMentions_DirectMessage(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "ping <@U_SELF> please")}, nil)
	if !digest.ChannelMentions(cu, "U_SELF") {
		t.Fatal("expected mention in top-level message")
	}
}

func TestChannelMentions_ReplyOnly(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "thread")},
		map[string][]goslack.Message{
			"1.0": {mkMsg("1.5", "U3", "ping <@U_SELF> deep in thread")},
		})
	if !digest.ChannelMentions(cu, "U_SELF") {
		t.Fatal("expected mention via thread reply")
	}
}

func TestChannelMentions_None(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "no mentions here")},
		map[string][]goslack.Message{
			"1.0": {mkMsg("1.5", "U3", "still none")},
		})
	if digest.ChannelMentions(cu, "U_SELF") {
		t.Fatal("expected no mention")
	}
}

func TestChannelMentions_EmptySelfID(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "ping <@U_SELF>")}, nil)
	if digest.ChannelMentions(cu, "") {
		t.Fatal("empty selfID must report no mention")
	}
}

// ----------------------- filterMentions -----------------------

func TestFilterMentions_KeepsOnlyMentioning(t *testing.T) {
	a := mkChannelUnread("a", []goslack.Message{mkMsg("1", "U2", "plain")}, nil)
	b := mkChannelUnread("b", []goslack.Message{mkMsg("1", "U2", "ping <@U_SELF>")}, nil)
	c := mkChannelUnread("c",
		[]goslack.Message{mkMsg("1", "U2", "thread")},
		map[string][]goslack.Message{"1": {mkMsg("2", "U3", "<@U_SELF> in reply")}})

	in := []*slack.ChannelUnread{a, b, c}
	got := filterMentions(in, "U_SELF")

	gotNames := []string{}
	for _, r := range got {
		gotNames = append(gotNames, r.Channel.Name)
	}
	sort.Strings(gotNames)
	want := []string{"b", "c"}
	if !reflect.DeepEqual(gotNames, want) {
		t.Fatalf("filterMentions kept %v; want %v", gotNames, want)
	}
}

func TestFilterMentions_EmptySelfIDIsNoOp(t *testing.T) {
	a := mkChannelUnread("a", []goslack.Message{mkMsg("1", "U2", "x")}, nil)
	in := []*slack.ChannelUnread{a}
	got := filterMentions(in, "")
	if len(got) != 1 || got[0] != a {
		t.Fatalf("empty selfID should be no-op; got %v", got)
	}
}

// ----------------------- rankUnread -----------------------

func TestRankUnread_MentionsOutrankVolume(t *testing.T) {
	busy := mkChannelUnread("busy", []goslack.Message{}, nil)
	for i := 0; i < 200; i++ {
		busy.Messages = append(busy.Messages, mkMsg("1.0", "U2", "noise"))
	}
	tagged := mkChannelUnread("tagged",
		[]goslack.Message{mkMsg("1.0", "U2", "ping <@U_SELF>")}, nil)

	if digest.RankUnread(busy, "U_SELF", time.Time{}, digest.UrgencyOpts{}) >= digest.RankUnread(tagged, "U_SELF", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("tagged channel must outrank a much busier non-tagged one")
	}
}

func TestRankUnread_VolumeBreaksTies(t *testing.T) {
	// Full-length bodies on purpose: five one-character messages are
	// exactly what DetectLowSignalChannel is for, and the resulting
	// tier demotion has nothing to do with the tie-break under test.
	const body = "picking this up now, will report back shortly"
	smaller := mkChannelUnread("s",
		[]goslack.Message{mkMsg("1.0", "U2", body), mkMsg("1.1", "U2", body)}, nil)
	bigger := mkChannelUnread("b", nil, nil)
	for i := 0; i < 5; i++ {
		bigger.Messages = append(bigger.Messages, mkMsg("1.0", "U2", body))
	}
	if digest.RankUnread(bigger, "U_SELF", time.Time{}, digest.UrgencyOpts{}) <= digest.RankUnread(smaller, "U_SELF", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("among non-tagged channels, busier must rank higher")
	}
}

func TestRankUnread_RepliesCountTowardVolume(t *testing.T) {
	withReplies := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "x")},
		map[string][]goslack.Message{
			"1.0": {mkMsg("1.1", "U3", "r1"), mkMsg("1.2", "U3", "r2")},
		})
	withoutReplies := mkChannelUnread("b",
		[]goslack.Message{mkMsg("1.0", "U2", "x")}, nil)
	if digest.RankUnread(withReplies, "", time.Time{}, digest.UrgencyOpts{}) <= digest.RankUnread(withoutReplies, "", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("replies should add to the volume score")
	}
}

// mkDM builds a 1:1 DM ChannelUnread. The IsIM flag is set so it is
// detected as a DM regardless of channel-ID prefix.
func mkDM(name string, msgs []goslack.Message) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{LastRead: "1700000000.000000", Messages: msgs}
	cu.Channel.ID = "D_" + name
	cu.Channel.Name = name
	cu.Channel.IsIM = true
	return cu
}

// TestRankUnread_DMOutranksNoisyChannel is the v0.4.18 guarantee: a
// quiet 1:1 DM must outrank a high-volume non-mention channel (the
// log/git feed case), so the max_chars cap never drops the DM in
// favour of a noisy bot channel.
func TestRankUnread_DMOutranksNoisyChannel(t *testing.T) {
	dm := mkDM("peer-1", []goslack.Message{mkMsg("1.0", "U2", "quick question")})

	noisy := mkChannelUnread("alerts-feed", nil, nil)
	for i := 0; i < 200; i++ {
		// Worst case: every message carries an urgency keyword too.
		noisy.Messages = append(noisy.Messages, mkMsg("1.0", "UBOT", "error error failed alert"))
	}

	if digest.RankUnread(dm, "U_SELF", time.Time{}, digest.UrgencyOpts{}) <=
		digest.RankUnread(noisy, "U_SELF", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("a 1:1 DM must outrank a high-volume non-mention channel")
	}
}

// TestRankUnread_MentionOutranksDM keeps the tier order: an explicit
// @-mention (even in a non-DM channel) still beats a plain DM.
func TestRankUnread_MentionOutranksDM(t *testing.T) {
	dm := mkDM("peer", []goslack.Message{mkMsg("1.0", "U2", "hi")})
	mentioned := mkChannelUnread("incidents",
		[]goslack.Message{mkMsg("1.0", "U2", "ping <@U_SELF>")}, nil)

	if digest.RankUnread(mentioned, "U_SELF", time.Time{}, digest.UrgencyOpts{}) <=
		digest.RankUnread(dm, "U_SELF", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("an explicit mention must outrank a plain DM")
	}
}

// TestRankUnread_DMPlusMentionTopsAll: a DM that also mentions the
// operator stacks both tiers and sits above a DM-only and a
// mention-only channel.
func TestRankUnread_DMPlusMentionTopsAll(t *testing.T) {
	dmMention := mkDM("vip", []goslack.Message{mkMsg("1.0", "U2", "<@U_SELF> urgent")})
	dmOnly := mkDM("peer", []goslack.Message{mkMsg("1.0", "U2", "hi")})
	mentionOnly := mkChannelUnread("ch",
		[]goslack.Message{mkMsg("1.0", "U2", "<@U_SELF>")}, nil)

	top := digest.RankUnread(dmMention, "U_SELF", time.Time{}, digest.UrgencyOpts{})
	if top <= digest.RankUnread(dmOnly, "U_SELF", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("DM+mention must outrank a DM-only channel")
	}
	if top <= digest.RankUnread(mentionOnly, "U_SELF", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("DM+mention must outrank a mention-only channel")
	}
}

// TestRankUnread_FlagMissingDMStillTiered guards the v0.4.12 lesson:
// Slack sometimes omits IsIM on stale-listing DMs. The dmBonus must
// still apply via the channel-ID prefix fallback, otherwise the
// exact DMs most at risk of being dropped wouldn't get the tier.
func TestRankUnread_FlagMissingDMStillTiered(t *testing.T) {
	// D-prefix ID but IsIM deliberately false — the stale-listing case.
	flagless := &slack.ChannelUnread{LastRead: "1700000000.000000",
		Messages: []goslack.Message{mkMsg("1.0", "U2", "ping")}}
	flagless.Channel.ID = "D0STALE1234"
	flagless.Channel.IsIM = false

	noisy := mkChannelUnread("logfeed", nil, nil)
	for i := 0; i < 100; i++ {
		noisy.Messages = append(noisy.Messages, mkMsg("1.0", "UBOT", "error failed"))
	}

	if digest.RankUnread(flagless, "U_SELF", time.Time{}, digest.UrgencyOpts{}) <=
		digest.RankUnread(noisy, "U_SELF", time.Time{}, digest.UrgencyOpts{}) {
		t.Fatalf("flag-missing DM (D-prefix) must still get the DM tier")
	}
}

// ----------------------- collectUserIDsWithReplies -----------------------

func TestCollectUserIDsWithReplies_Dedupes(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "x"), mkMsg("1.1", "U3", "y")},
		map[string][]goslack.Message{
			"1.0": {mkMsg("1.2", "U2", "self-reply"), mkMsg("1.3", "U4", "new")},
		})
	got := collectUserIDsWithReplies(cu)
	sort.Strings(got)
	want := []string{"U2", "U3", "U4"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectUserIDsWithReplies = %v; want %v", got, want)
	}
}

func TestCollectUserIDsWithReplies_SkipsEmptyUser(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{
			mkMsg("1.0", "", "system message no user"),
			mkMsg("1.1", "U2", "real"),
		}, nil)
	got := collectUserIDsWithReplies(cu)
	if len(got) != 1 || got[0] != "U2" {
		t.Fatalf("expected only [U2], got %v", got)
	}
}
