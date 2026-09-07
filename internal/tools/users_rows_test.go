package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func rosterUser(id, handle, real, title string) goslack.User {
	u := goslack.User{ID: id, Name: handle, RealName: real}
	u.Profile.Title = title
	return u
}

func TestRenderUserRows_LeadsWithTheSlackID(t *testing.T) {
	// Without the ID, a raw "<@U…>" in a message body has no route back
	// to a person: the roster lists handles, the payload carries IDs.
	got := renderUserRows([]goslack.User{
		rosterUser("U0EXAMPLE01", "handle.one", "First Person", "Engineer"),
	}, nil, false)

	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "handle.one") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("roster row missing; got:\n%s", got)
	}
	if !strings.HasPrefix(line, "U0EXAMPLE01 | handle.one | First Person | Engineer") {
		t.Fatalf("id must lead the row, followed by handle and name; got %q", line)
	}
}

func TestRenderUserRows_KeepsIDInActivityMode(t *testing.T) {
	got := renderUserRows(
		[]goslack.User{rosterUser("U0EXAMPLE02", "handle.two", "Second Person", "")},
		map[string]string{"U0EXAMPLE02": "2026-09-01"}, true)
	if !strings.Contains(got, "U0EXAMPLE02 | handle.two") {
		t.Errorf("id must survive the with_activity variant; got:\n%s", got)
	}
	if !strings.Contains(got, "last_post=2026-09-01") {
		t.Errorf("activity date missing; got:\n%s", got)
	}
}

func TestRenderUserRows_CountsUsers(t *testing.T) {
	got := renderUserRows([]goslack.User{
		rosterUser("U0EXAMPLE03", "a", "A", ""),
		rosterUser("U0EXAMPLE04", "b", "B", ""),
	}, nil, false)
	if !strings.HasPrefix(got, "2 users\n") {
		t.Fatalf("header must count the rendered rows; got:\n%s", got)
	}
}
