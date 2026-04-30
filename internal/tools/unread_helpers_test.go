package tools

import (
	"reflect"
	"sort"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
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
	if !channelMentions(cu, "U_SELF") {
		t.Fatal("expected mention in top-level message")
	}
}

func TestChannelMentions_ReplyOnly(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "thread")},
		map[string][]goslack.Message{
			"1.0": {mkMsg("1.5", "U3", "ping <@U_SELF> deep in thread")},
		})
	if !channelMentions(cu, "U_SELF") {
		t.Fatal("expected mention via thread reply")
	}
}

func TestChannelMentions_None(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "no mentions here")},
		map[string][]goslack.Message{
			"1.0": {mkMsg("1.5", "U3", "still none")},
		})
	if channelMentions(cu, "U_SELF") {
		t.Fatal("expected no mention")
	}
}

func TestChannelMentions_EmptySelfID(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{mkMsg("1.0", "U2", "ping <@U_SELF>")}, nil)
	if channelMentions(cu, "") {
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

	if rankUnread(busy, "U_SELF", time.Time{}, urgencyOpts{}) >= rankUnread(tagged, "U_SELF", time.Time{}, urgencyOpts{}) {
		t.Fatalf("tagged channel must outrank a much busier non-tagged one")
	}
}

func TestRankUnread_VolumeBreaksTies(t *testing.T) {
	smaller := mkChannelUnread("s",
		[]goslack.Message{mkMsg("1.0", "U2", "x"), mkMsg("1.1", "U2", "y")}, nil)
	bigger := mkChannelUnread("b", nil, nil)
	for i := 0; i < 5; i++ {
		bigger.Messages = append(bigger.Messages, mkMsg("1.0", "U2", "x"))
	}
	if rankUnread(bigger, "U_SELF", time.Time{}, urgencyOpts{}) <= rankUnread(smaller, "U_SELF", time.Time{}, urgencyOpts{}) {
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
	if rankUnread(withReplies, "", time.Time{}, urgencyOpts{}) <= rankUnread(withoutReplies, "", time.Time{}, urgencyOpts{}) {
		t.Fatalf("replies should add to the volume score")
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
