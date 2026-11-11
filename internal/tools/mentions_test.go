package tools

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func mnUser(id, handle, display, real string) goslack.User {
	u := goslack.User{ID: id, Name: handle, RealName: real}
	u.Profile.DisplayName = display
	u.Profile.RealName = real
	return u
}

var mnRoster = []goslack.User{
	mnUser("U01", "sam.lee", "Sam", "Sam Lee"),
	mnUser("U02", "pat", "", "Pat Novak"),
	mnUser("U03", "kim.o", "Kim", "Kim Ortega"),
	mnUser("U04", "kim.r", "Kim", "Kim Ross"),
	mnUser("U05", "ivanova.m", "", "Мария Иванова"),
	mnUser("U06", "samuel", "", "Samuel Hart"),
}

func TestResolveMentions(t *testing.T) {
	cases := []struct {
		name, in, want string
		unresolved     []string
	}{
		{"handle", "hi @pat", "hi <@U02>", nil},
		{"handle with a dot", "@sam.lee please", "<@U01> please", nil},
		{"real name with a space", "@Pat Novak, ok?", "<@U02>, ok?", nil},
		{"case-insensitive", "@PAT NOVAK", "<@U02>", nil},
		{"cyrillic real name", "@мария иванова глянь", "<@U05> глянь", nil},
		{"longest name wins", "@Samuel Hart and @Sam", "<@U06> and <@U01>", nil},
		{"no match inside a longer word", "@samuelx", "@samuelx", []string{"@samuelx"}},
		{"sentence dot ends the name", "thanks @pat.", "thanks <@U02>.", nil},
		{"dot inside a handle is not an end", "@pat.x", "@pat.x", []string{"@pat.x"}},
		{"list of handles", "@pat @sam.lee @kim.o", "<@U02> <@U01> <@U03>", nil},
		{"e-mail is not a mention", "mail pat@example.com", "mail pat@example.com", nil},
		{"code span untouched", "run `@pat` now", "run `@pat` now", nil},
		{"code block untouched", "```\n@pat\n```", "```\n@pat\n```", nil},
		{"existing mention untouched", "<@U02> and <mailto:a@b.c|a@b.c>", "<@U02> and <mailto:a@b.c|a@b.c>", nil},
		{"broadcast words untouched", "@here @channel @everyone", "@here @channel @everyone", nil},
		{"unknown reported once", "@ghost and @Ghost", "@ghost and @Ghost", []string{"@ghost"}},
		{"bare at sign", "a @ b", "a @ b", nil},
		{"no at sign", "plain text", "plain text", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveMentions(c.in, mnRoster)
			if got.text != c.want {
				t.Fatalf("text: got %q, want %q", got.text, c.want)
			}
			if !reflect.DeepEqual(got.unresolved, c.unresolved) {
				t.Fatalf("unresolved: got %v, want %v", got.unresolved, c.unresolved)
			}
		})
	}
}

func TestResolveMentions_SharedNameIsNotGuessed(t *testing.T) {
	got := resolveMentions("@Kim can you check", mnRoster)
	if got.text != "@Kim can you check" {
		t.Fatalf("a name two people share must stay as typed, got %q", got.text)
	}
	if len(got.ambiguous) != 1 || !strings.Contains(got.ambiguous[0], "Kim Ortega") || !strings.Contains(got.ambiguous[0], "Kim Ross") {
		t.Fatalf("both candidates must be named, got %v", got.ambiguous)
	}
	// The full name still picks one person.
	if got := resolveMentions("@Kim Ross", mnRoster); got.text != "<@U04>" {
		t.Fatalf("a unique full name must resolve, got %q", got.text)
	}
}

func TestResolveMentions_ReportsWhoWasTagged(t *testing.T) {
	note := mentionNote(resolveMentions("@pat @ghost", mnRoster))
	for _, want := range []string{"tagged: @pat → Pat Novak", "no such person: @ghost"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note %q lacks %q", note, want)
		}
	}
}

// End to end: the text Slack receives carries <@U…> tokens, and the
// result says who was tagged.
func TestPostMessage_ResolvesMentions(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.On("users.list", `{"ok":true,"members":[`+
		`{"id":"U01","name":"sam.lee","real_name":"Sam Lee","profile":{"display_name":"Sam","real_name":"Sam Lee"}},`+
		`{"id":"U02","name":"pat","real_name":"Pat Novak","profile":{"real_name":"Pat Novak"}}`+
		`],"response_metadata":{"next_cursor":""}}`)
	var gotText string
	f.OnFunc("chat.postMessage", func(r *http.Request) string {
		gotText = r.Form.Get("text")
		return `{"ok":true,"channel":"C1","ts":"1700000500.000600"}`
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "post_message", map[string]any{
		"channel": "alpha", "text": "meeting at 10:00 @sam.lee @Pat Novak @ghost",
	}))

	if gotText != "meeting at 10:00 <@U01> <@U02> @ghost" {
		t.Fatalf("Slack got %q", gotText)
	}
	tmWant(t, out, "posted to #alpha", "tagged: @sam.lee → Sam Lee, @Pat Novak → Pat Novak", "no such person: @ghost")
}

func TestPostMessage_MentionsCanBeTurnedOff(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	var gotText string
	f.OnFunc("chat.postMessage", func(r *http.Request) string {
		gotText = r.Form.Get("text")
		return `{"ok":true,"channel":"C1","ts":"1700000500.000600"}`
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	tmCall(t, s, "post_message", map[string]any{"channel": "alpha", "text": "@pat hi", "resolve_mentions": false})

	if gotText != "@pat hi" || f.Called("users.list") {
		t.Fatalf("resolve_mentions=false must send the text as typed without reading the roster: %q calls=%v", gotText, f.Calls())
	}
}

// A roster that can't be read must not cost the message.
func TestPostMessage_RosterFailureStillPosts(t *testing.T) {
	f := newFakeSlack(t)
	tmChannelList(f, map[string]string{"alpha": "C1"})
	f.OnError("users.list", "missing_scope")
	var gotText string
	f.OnFunc("chat.postMessage", func(r *http.Request) string {
		gotText = r.Form.Get("text")
		return `{"ok":true,"channel":"C1","ts":"1700000500.000600"}`
	})
	s := tmThreadServer(t, newFakeHub(t, f))

	out := resultText(tmCall(t, s, "post_message", map[string]any{"channel": "alpha", "text": "@pat hi"}))

	if gotText != "@pat hi" {
		t.Fatalf("the text must go out as typed, got %q", gotText)
	}
	tmWant(t, out, "posted to #alpha", "mentions not resolved", "missing_scope")
}
