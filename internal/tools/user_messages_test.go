package tools

import "testing"

func TestBuildUserMessagesQuery_userOnly(t *testing.T) {
	got := buildUserMessagesQuery("alice", "", "", "")
	want := "from:@alice"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildUserMessagesQuery_stripsLeadingHash(t *testing.T) {
	got := buildUserMessagesQuery("alice", "#status-channel", "", "")
	want := "from:@alice in:#status-channel"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildUserMessagesQuery_sinceAndUntil(t *testing.T) {
	got := buildUserMessagesQuery("alice", "status-channel", "2026-05-10", "2026-05-12")
	want := "from:@alice in:#status-channel after:2026-05-10 before:2026-05-12"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildUserMessagesQuery_sinceOnly(t *testing.T) {
	got := buildUserMessagesQuery("alice", "", "2026-05-10", "")
	want := "from:@alice after:2026-05-10"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildUserMessagesQuery_untilOnly(t *testing.T) {
	got := buildUserMessagesQuery("alice", "", "", "2026-05-12")
	want := "from:@alice before:2026-05-12"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
