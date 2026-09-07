package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// externalFile builds the shape Slack returns for a third-party
// document linked into a conversation: full metadata, and a url_private
// that points at the other service rather than at Slack.
func externalFile(name, host, url string) goslack.File {
	f := docFile(name, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx")
	f.IsExternal = true
	f.ExternalType = host
	f.URLPrivate = url
	return f
}

func TestIsExternalFile(t *testing.T) {
	if !isExternalFile(externalFile("plan.xlsx", "gdrive", "https://example.invalid/d/1")) {
		t.Error("a linked document must be recognised as external")
	}
	// Either signal alone is enough — Slack does not always set both.
	only := docFile("plan.xlsx", "application/octet-stream", "xlsx")
	only.IsExternal = true
	if !isExternalFile(only) {
		t.Error("is_external alone must be enough")
	}
	typed := docFile("plan.xlsx", "application/octet-stream", "xlsx")
	typed.ExternalType = "dropbox"
	if !isExternalFile(typed) {
		t.Error("external_type alone must be enough")
	}
	if isExternalFile(docFile("plan.xlsx", "application/octet-stream", "xlsx")) {
		t.Error("a real upload must not be called external")
	}
}

func TestExternalFileNote_ExplainsAndLinks(t *testing.T) {
	// The failure this replaces was a bare 401, which reads as a Slack
	// permissions problem and sends the reader after tokens and channel
	// membership — neither of which is the cause.
	got := externalFileNote(externalFile("plan.xlsx", "gdrive", "https://example.invalid/d/1"))
	for _, want := range []string{"plan.xlsx", "not stored in Slack", "gdrive", "https://example.invalid/d/1"} {
		if !strings.Contains(got, want) {
			t.Errorf("note missing %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "401") {
		t.Error("the note must explain the cause, not echo the HTTP status")
	}
}

func TestExternalFileNote_SurvivesMissingFields(t *testing.T) {
	f := docFile("plan.xlsx", "application/octet-stream", "xlsx")
	f.IsExternal = true // no external_type, no url
	got := externalFileNote(f)
	if !strings.Contains(got, "plan.xlsx") || !strings.Contains(got, "external") {
		t.Fatalf("note must degrade gracefully; got %q", got)
	}
	if strings.Contains(got, "Open it where it lives") {
		t.Error("no link must be offered when Slack gave none")
	}
}

func TestRenderDocumentList_MarksExternalEntries(t *testing.T) {
	// A listing that shows an external file exactly like a readable one
	// invites a read that cannot succeed.
	got := renderDocumentList([]docCandidate{
		{File: externalFile("shared.xlsx", "gdrive", "https://example.invalid/d/2"), TS: "1788000000.000100"},
		{File: docFile("local.txt", "text/plain", "text"), TS: "1788000000.000200"},
	}, "")
	if !strings.Contains(got, "linked gdrive document") {
		t.Errorf("external entry must be marked; got:\n%s", got)
	}
	line := ""
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "local.txt") {
			line = l
		}
	}
	if strings.Contains(line, "linked") {
		t.Errorf("a real upload must not be marked external; got %q", line)
	}
}
