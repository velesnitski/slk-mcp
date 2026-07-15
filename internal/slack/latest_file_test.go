package slack

import (
	"testing"

	goslack "github.com/slack-go/slack"
)

// audioFile is a small helper: a message carrying one file of the given
// mimetype, authored by user.
func msgWithFile(user, mimetype string) goslack.Message {
	m := goslack.Message{}
	m.User = user
	m.Files = []goslack.File{{Mimetype: mimetype}}
	return m
}

func acceptAudio(f goslack.File) bool {
	return len(f.Mimetype) >= 6 && f.Mimetype[:6] == "audio/"
}

func TestSelectLatestFileMessage_NewestFirstWins(t *testing.T) {
	// History is newest-first, so the first accepted attachment is the
	// latest. Two audio notes: the earlier-in-slice (newer) one wins.
	newer := msgWithFile("U1", "audio/mp4")
	newer.Timestamp = "200.0"
	older := msgWithFile("U1", "audio/mp4")
	older.Timestamp = "100.0"
	got := selectLatestFileMessage([]goslack.Message{newer, older}, acceptAudio, "")
	if got == nil || got.Timestamp != "200.0" {
		t.Fatalf("want newest (200.0); got %+v", got)
	}
}

func TestSelectLatestFileMessage_SkipsRejectedTypes(t *testing.T) {
	// A newer image must be skipped in favour of an older audio note
	// when accept only admits audio.
	image := msgWithFile("U1", "image/png")
	image.Timestamp = "300.0"
	audio := msgWithFile("U1", "audio/mp4")
	audio.Timestamp = "200.0"
	got := selectLatestFileMessage([]goslack.Message{image, audio}, acceptAudio, "")
	if got == nil || got.Timestamp != "200.0" {
		t.Fatalf("want the audio note (200.0); got %+v", got)
	}
}

func TestSelectLatestFileMessage_FromFilter(t *testing.T) {
	// With fromUserID set, a newer note from someone else is skipped so
	// "my last voice note" resolves to the caller's own message.
	theirs := msgWithFile("U_OTHER", "audio/mp4")
	theirs.Timestamp = "300.0"
	mine := msgWithFile("U_ME", "audio/mp4")
	mine.Timestamp = "200.0"
	got := selectLatestFileMessage([]goslack.Message{theirs, mine}, acceptAudio, "U_ME")
	if got == nil || got.Timestamp != "200.0" {
		t.Fatalf("want my note (200.0); got %+v", got)
	}
}

func TestSelectLatestFileMessage_NoMatchReturnsNil(t *testing.T) {
	image := msgWithFile("U1", "image/png")
	if got := selectLatestFileMessage([]goslack.Message{image}, acceptAudio, ""); got != nil {
		t.Fatalf("want nil when nothing accepted; got %+v", got)
	}
	// Author filter with no matching author also yields nil.
	audio := msgWithFile("U_OTHER", "audio/mp4")
	if got := selectLatestFileMessage([]goslack.Message{audio}, acceptAudio, "U_ME"); got != nil {
		t.Fatalf("want nil when no message from U_ME; got %+v", got)
	}
}

func TestSelectLastFileMessage_NewestReplyWins(t *testing.T) {
	// conversations.replies is oldest-first (parent, then replies), so
	// the LAST matching message is the newest — the voice note reply.
	root := msgWithFile("U1", "text/plain") // parent, no audio
	root.Files = nil
	root.Timestamp = "100.0"
	older := msgWithFile("U1", "audio/mp4")
	older.Timestamp = "200.0"
	newer := msgWithFile("U2", "audio/mp4")
	newer.Timestamp = "300.0"
	got := selectLastFileMessage([]goslack.Message{root, older, newer}, acceptAudio)
	if got == nil || got.Timestamp != "300.0" {
		t.Fatalf("want newest reply (300.0); got %+v", got)
	}
}

func TestSelectLastFileMessage_SkipsNonMatching(t *testing.T) {
	// A newer image reply is skipped for an older audio reply.
	audio := msgWithFile("U1", "audio/mp4")
	audio.Timestamp = "200.0"
	image := msgWithFile("U1", "image/png")
	image.Timestamp = "300.0"
	got := selectLastFileMessage([]goslack.Message{audio, image}, acceptAudio)
	if got == nil || got.Timestamp != "200.0" {
		t.Fatalf("want the audio reply (200.0); got %+v", got)
	}
}

func TestSelectLastFileMessage_NoMatchReturnsNil(t *testing.T) {
	text := goslack.Message{}
	text.Timestamp = "100.0"
	image := msgWithFile("U1", "image/png")
	if got := selectLastFileMessage([]goslack.Message{text, image}, acceptAudio); got != nil {
		t.Fatalf("want nil when no audio in thread; got %+v", got)
	}
	if got := selectLastFileMessage(nil, acceptAudio); got != nil {
		t.Fatalf("want nil for empty thread; got %+v", got)
	}
}

func TestMatchHandle(t *testing.T) {
	users := []goslack.User{
		{ID: "U1", Name: "jbravo", RealName: "Johnny Bravo"},
		{ID: "U2", Name: "asmith", Profile: goslack.UserProfile{DisplayName: "Alice S"}},
	}
	cases := []struct {
		in     string
		wantID string
		wantOK bool
	}{
		{"@jbravo", "U1", true},      // leading @ stripped
		{"jbravo", "U1", true},       // bare handle
		{"JBRAVO", "U1", true},       // case-insensitive
		{"Johnny Bravo", "U1", true}, // real-name fallback
		{"Alice S", "U2", true},      // display-name fallback
		{"asmith", "U2", true},       // username
		{"nobody", "", false},        // no match
		{"@", "", false},             // empty after strip
		{"", "", false},
	}
	for _, c := range cases {
		gotID, gotOK := matchHandle(users, c.in)
		if gotID != c.wantID || gotOK != c.wantOK {
			t.Errorf("matchHandle(%q) = (%q, %v); want (%q, %v)", c.in, gotID, gotOK, c.wantID, c.wantOK)
		}
	}
}

func TestMatchHandle_UsernamePreferredOverDisplayCollision(t *testing.T) {
	// One user's display name equals another's username. The username
	// match must win — a display-name collision can't shadow the real
	// @handle.
	users := []goslack.User{
		{ID: "U_DISPLAY", Profile: goslack.UserProfile{DisplayName: "raven"}},
		{ID: "U_HANDLE", Name: "raven"},
	}
	got, ok := matchHandle(users, "@raven")
	if !ok || got != "U_HANDLE" {
		t.Fatalf("username match must win; got (%q, %v)", got, ok)
	}
}
