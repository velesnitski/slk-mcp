package digest

import (
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

func TestRankUnread_BotFeedNeverEvictsHumanChannel(t *testing.T) {
	// The reported failure: a feed of 50 content-less bot lines
	// outranked every human channel and evicted them under max_chars,
	// because raw volume was the base of the rank and an error-shaped
	// vocabulary is what such a feed is made of.
	feed := mkChannelUnread("crm-alerts", nil, nil)
	for i := 0; i < 50; i++ {
		m := goslack.Message{}
		m.BotID = "B1"
		m.Text = "error: task failed"
		feed.Messages = append(feed.Messages, m)
	}
	human := mkChannelUnread("devops-main",
		[]goslack.Message{msgWithText("has anyone looked at the staging rollout")}, nil)

	if RankUnread(feed, "", time.Time{}, UrgencyOpts{}) >= RankUnread(human, "", time.Time{}, UrgencyOpts{}) {
		t.Fatal("a 50-message bot feed must rank below a 1-message human channel")
	}
}

func TestRankUnread_MentionLiftsAFeedBackOut(t *testing.T) {
	// The demotion is a tier, not a mute: being pinged inside a bot
	// channel still has to reach you.
	feed := mkChannelUnread("git-deploy", nil, nil)
	for i := 0; i < 10; i++ {
		m := goslack.Message{}
		m.BotID = "B1"
		m.Text = "deploy failed"
		feed.Messages = append(feed.Messages, m)
	}
	quiet := mkChannelUnread("dev-backend",
		[]goslack.Message{msgWithText("morning, starting on the migration today")}, nil)

	pinged := mkChannelUnread("git-deploy", nil, nil)
	for i := 0; i < 10; i++ {
		m := goslack.Message{}
		m.BotID = "B1"
		m.Text = "deploy failed"
		pinged.Messages = append(pinged.Messages, m)
	}
	ping := goslack.Message{}
	ping.BotID = "B1"
	ping.Text = "deploy failed, <@USELF> please look"
	pinged.Messages = append(pinged.Messages, ping)

	if RankUnread(feed, "USELF", time.Time{}, UrgencyOpts{}) >= RankUnread(quiet, "USELF", time.Time{}, UrgencyOpts{}) {
		t.Fatal("an unmentioning feed must stay below a quiet human channel")
	}
	if RankUnread(pinged, "USELF", time.Time{}, UrgencyOpts{}) <= RankUnread(quiet, "USELF", time.Time{}, UrgencyOpts{}) {
		t.Fatal("a feed that mentions you must climb back above an ordinary channel")
	}
}

func TestIsBotFeed_MatchesTheRenderersNotion(t *testing.T) {
	// The ranker and the renderer must agree on what a feed is, or a
	// channel gets collapsed to a histogram while ranked as a
	// conversation.
	git := mkChannelUnread("git-frontend", nil, nil)
	for i := 0; i < 6; i++ {
		m := goslack.Message{}
		m.BotID = "B1"
		m.Text = "pipeline passed on branch main"
		git.Messages = append(git.Messages, m)
	}
	if !IsBotFeed(git) {
		t.Error("git feed must count as a bot feed")
	}
	checkins := mkChannelUnread("checkins", []goslack.Message{msgWithText("+")}, nil)
	if !IsBotFeed(checkins) {
		t.Error("a name-flagged low-signal channel must count as a bot feed")
	}
	human := mkChannelUnread("product-marketing",
		[]goslack.Message{msgWithText("agenda for the product call is up, please review")}, nil)
	if IsBotFeed(human) {
		t.Error("a human channel must not be demoted")
	}
}
