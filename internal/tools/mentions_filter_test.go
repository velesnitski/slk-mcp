package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"
)

func searchMsg(text string) goslack.SearchMessage {
	m := goslack.SearchMessage{}
	m.Text = text
	return m
}

func TestFilterEmptyMentions(t *testing.T) {
	in := []goslack.SearchMessage{
		searchMsg(""),
		searchMsg("   "),
		searchMsg("real text"),
		searchMsg("\n\t"),
		searchMsg("ping <@U001>"),
	}
	out := filterEmptyMentions(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 non-empty kept, got %d: %v", len(out), out)
	}
}

func TestFilterClosingAcks(t *testing.T) {
	cases := []struct {
		body string
		drop bool
	}{
		{"thanks", true},
		{"спасибо", true},
		{"thanks!", true},
		{"+1", true},
		{"OK!", true},
		{"спасибо)", true},
		{"got it.", true},
		{"thanks for the audit, will look into it tomorrow", false},
		{"спасибо за информацию, посмотрю", false},
		{"any updates?", false},
		{"", false}, // empty isn't an ack — filterEmptyMentions handles it
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			in := []goslack.SearchMessage{searchMsg(c.body)}
			out := filterClosingAcks(in)
			dropped := len(out) == 0
			if dropped != c.drop {
				t.Fatalf("body=%q dropped=%v want=%v", c.body, dropped, c.drop)
			}
		})
	}
}

func searchMsgFrom(username, userID, text string) goslack.SearchMessage {
	m := goslack.SearchMessage{}
	m.Username = username
	m.User = userID
	m.Text = text
	return m
}

func TestFilterBotSenders(t *testing.T) {
	in := []goslack.SearchMessage{
		searchMsgFrom("google_calendar", "U0BOT111111", "Today is ..."),
		searchMsgFrom("Google_Calendar", "U1", "case-insensitive"),
		searchMsgFrom("googledrive", "U2", "shared a file"),
		searchMsgFrom("", "USLACKBOT", "invite request"), // slackbot via user id
		searchMsgFrom("slackbot", "U3", "reminder"),
		searchMsgFrom("alice", "U4", "real human mention"),
		searchMsgFrom("bob", "U5", "another human"),
	}
	out := filterBotSenders(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 human mentions kept; got %d: %+v", len(out), out)
	}
	for _, m := range out {
		if m.Username != "alice" && m.Username != "bob" {
			t.Fatalf("unexpected sender survived filter: %q", m.Username)
		}
	}
}

func TestFilterBotSenders_AllHumansPassThrough(t *testing.T) {
	in := []goslack.SearchMessage{
		searchMsgFrom("alice", "U1", "x"),
		searchMsgFrom("bob", "U2", "y"),
	}
	if got := filterBotSenders(in); len(got) != 2 {
		t.Fatalf("human-only input should pass through untouched; got %d", len(got))
	}
}

func TestFilterStrictMentions(t *testing.T) {
	in := []goslack.SearchMessage{
		searchMsg("ping <@U_SELF>"),
		searchMsg("see <@U_SELF|Alex>"),
		searchMsg("ping <@U_OTHER>"),
		searchMsg("plain text without any mention"),
		searchMsg("hello @everyone"),
	}
	out := filterStrictMentions(in, "U_SELF")
	if len(out) != 2 {
		t.Fatalf("expected 2 kept (the two with <@U_SELF...>), got %d: %v", len(out), out)
	}
}

func TestFilterStrictMentions_EmptySelfIDIsNoop(t *testing.T) {
	in := []goslack.SearchMessage{searchMsg("anything"), searchMsg("ping <@U_X>")}
	// strict mention with empty selfID would never match, so all dropped.
	out := filterStrictMentions(in, "")
	if len(out) != 0 {
		t.Fatalf("empty selfID with strict_mention should drop all, got %v", out)
	}
}
