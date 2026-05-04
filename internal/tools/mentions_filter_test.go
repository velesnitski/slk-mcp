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
