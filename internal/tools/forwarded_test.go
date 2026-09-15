package tools

import (
	"encoding/json"
	"testing"

	goslack "github.com/slack-go/slack"
)

func forwardAttachment(ts string) goslack.Attachment {
	return goslack.Attachment{
		Fallback:   "[April 30th, 2024 12:00 PM] example.person: File",
		AuthorName: "example.person",
		Ts:         json.Number(ts),
	}
}

// A forward carries the original's ts in an attachment and no files of
// its own — that is the shape the file tools used to report as "no
// attachment", which is true of this message and false of what the user
// sees on screen.
func TestForwardedOriginTS_detectsForward(t *testing.T) {
	var msg goslack.Message
	msg.Attachments = []goslack.Attachment{forwardAttachment("1714492800.000100")}

	if got := forwardedOriginTS(&msg); got != "1714492800.000100" {
		t.Fatalf("want the original ts, got %q", got)
	}
}

// A message that carries its own file is not a forward, even when it
// also has attachments — the file is reachable and must be fetched.
func TestForwardedOriginTS_ownFileWins(t *testing.T) {
	var msg goslack.Message
	msg.Files = []goslack.File{{ID: "F0EXAMPLE01", Name: "example.png"}}
	msg.Attachments = []goslack.Attachment{forwardAttachment("1714492800.000100")}

	if got := forwardedOriginTS(&msg); got != "" {
		t.Fatalf("a message with its own file must not be treated as a forward, got %q", got)
	}
}

// A plain attachment with no ts (a link unfurl, a bot notice) is not a
// forward and must not produce a misleading "open the original" hint.
func TestForwardedOriginTS_plainAttachmentIsNotAForward(t *testing.T) {
	var msg goslack.Message
	msg.Attachments = []goslack.Attachment{{Text: "nightly job finished"}}

	if got := forwardedOriginTS(&msg); got != "" {
		t.Fatalf("a plain attachment must not read as a forward, got %q", got)
	}
}

// The caller hands fetchFiles a pointer that can be nil on an error
// path; the predicate must not panic there.
func TestForwardedOriginTS_nilMessage(t *testing.T) {
	if got := forwardedOriginTS(nil); got != "" {
		t.Fatalf("nil message must not read as a forward, got %q", got)
	}
}
