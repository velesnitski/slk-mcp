package slack

import (
	"testing"

	goslack "github.com/slack-go/slack"
)

func threadMsg(user, ts string) goslack.Message {
	m := goslack.Message{}
	m.User = user
	m.Timestamp = ts
	return m
}

func TestUnseenAfterMine_RepliesAfterMyLast(t *testing.T) {
	// I started the thread (100) and replied (400); others replied at 200,
	// 300 (before my last, already seen) and 500 (after — unseen).
	thread := []goslack.Message{
		threadMsg("ME", "100.0"),
		threadMsg("OTHER", "200.0"),
		threadMsg("OTHER", "300.0"),
		threadMsg("ME", "400.0"),
		threadMsg("OTHER", "500.0"),
	}
	got := unseenAfterMine(thread, "ME")
	if len(got) != 1 || got[0].Timestamp != "500.0" {
		t.Fatalf("want only the 500.0 reply (after my last at 400); got %+v", got)
	}
}

func TestUnseenAfterMine_ExcludesMyOwnLaterMessages(t *testing.T) {
	// My own message is the newest — nothing from others after it.
	thread := []goslack.Message{
		threadMsg("ME", "100.0"),
		threadMsg("OTHER", "200.0"),
		threadMsg("ME", "300.0"),
	}
	if got := unseenAfterMine(thread, "ME"); len(got) != 0 {
		t.Fatalf("no others' replies after my last (300); got %+v", got)
	}
}

func TestUnseenAfterMine_NotAParticipantReturnsNil(t *testing.T) {
	// I never posted in this thread — nothing is "mine to have missed".
	thread := []goslack.Message{
		threadMsg("A", "100.0"),
		threadMsg("B", "200.0"),
	}
	if got := unseenAfterMine(thread, "ME"); got != nil {
		t.Fatalf("want nil when I'm not a participant; got %+v", got)
	}
}

func TestUnseenAfterMine_ParentOnlyAuthorSurfacesAllReplies(t *testing.T) {
	// I authored the parent (100) and never replied again; every later
	// reply from others is unseen.
	thread := []goslack.Message{
		threadMsg("ME", "100.0"),
		threadMsg("OTHER", "200.0"),
		threadMsg("OTHER2", "300.0"),
	}
	got := unseenAfterMine(thread, "ME")
	if len(got) != 2 {
		t.Fatalf("want both later replies; got %+v", got)
	}
}
