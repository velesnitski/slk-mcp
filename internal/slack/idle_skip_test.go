package slack

import (
	"context"
	"strconv"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// ADR 132: a conversation whose newest message is read and older than the
// thread lookback is not read again — the page could hold nothing the
// unread pass keeps.
func TestReadIdle(t *testing.T) {
	now := svNow
	old := strconv.FormatInt(now-13*3600, 10) + ".000000"
	recent := strconv.FormatInt(now-3600, 10) + ".000000"
	oldF, _ := strconv.ParseFloat(old, 64)
	recentF, _ := strconv.ParseFloat(recent, 64)

	cases := []struct {
		name     string
		latest   *goslack.Message
		lastRead float64
		want     bool
	}{
		{"no latest from Slack", nil, oldF, false},
		{"read and old", &goslack.Message{Msg: goslack.Msg{Timestamp: old}}, oldF, true},
		{"read long ago, marker later", &goslack.Message{Msg: goslack.Msg{Timestamp: old}}, recentF, true},
		{"unread", &goslack.Message{Msg: goslack.Msg{Timestamp: old}}, oldF - 1, false},
		{"read but recent", &goslack.Message{Msg: goslack.Msg{Timestamp: recent}}, recentF, false},
		{"read and old, but a thread", &goslack.Message{Msg: goslack.Msg{Timestamp: old, ReplyCount: 2}}, oldF, false},
		{"read and old, latest reply set", &goslack.Message{Msg: goslack.Msg{Timestamp: old, LatestReply: recent}}, oldF, false},
		{"unparsable ts", &goslack.Message{Msg: goslack.Msg{Timestamp: "x"}}, oldF, false},
	}
	for _, c := range cases {
		if got := readIdle(c.latest, c.lastRead, now); got != c.want {
			t.Errorf("%s: readIdle = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUnreadAll_SkipsIdleConversations(t *testing.T) {
	f := svServer(t)
	svFreezeClock(t, svNow)
	old := strconv.FormatInt(svNow-13*3600, 10) + ".000000"
	f.reply("users.conversations", svJSON{"ok": true, "channels": []svJSON{svChan("C1", "quiet", nil)}})
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "quiet", svJSON{
		"last_read": old, "latest": svMsg(old, "U2", "old news", nil),
	})})
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{}})

	var st UnreadStats
	if _, err := svUnread(t, f, nil).UnreadAllWith(context.Background(), 5, UnreadOpts{Stats: &st}); err != nil {
		t.Fatalf("UnreadAllWith: %v", err)
	}
	if n := f.count("conversations.history"); n != 0 {
		t.Fatalf("conversations.history called %d times for an idle conversation, want 0", n)
	}
	if st.Channels != 1 || st.SkippedIdle != 1 || st.HistoryReads != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

// ADR 132: DMs are read first, so a pass the deadline cuts has read the
// conversations written to the operator.
func TestUnreadAll_ReadsDMsFirst(t *testing.T) {
	got := dmsFirst([]goslack.Channel{
		{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C1"}}},
		{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "D1", IsIM: true}}},
		{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C2"}}},
		{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "D2", IsIM: true}}},
	})
	var ids []string
	for _, ch := range got {
		ids = append(ids, ch.ID)
	}
	if want := "D1 D2 C1 C2"; strings.Join(ids, " ") != want {
		t.Fatalf("order = %s, want %s", strings.Join(ids, " "), want)
	}
}
