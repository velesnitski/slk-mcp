package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func gitMsg(ts, text string) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = ts
	m.Text = text
	m.User = "U_BOT"
	return m
}

func TestExtractWorkflowKey(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"FOO-123 - add answers to search", "FOO-123"},
		{"merged !100", "!100"},
		{"removed branch feature/foo from Repo", "branch feature/foo"},
		{"Starting deploy to stage", "deploy:stage"},
		{"random text without anything", ""},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			if got := extractWorkflowKey(c.text); got != c.want {
				t.Fatalf("extractWorkflowKey(%q) = %q; want %q", c.text, got, c.want)
			}
		})
	}
}

func TestExtractVerb(t *testing.T) {
	cases := []struct {
		text string
		want string
	}{
		{"Alice approved merge request !100", "approved"},
		{"Alice merged merge request !100", "merged"},
		{"Bob removed branch foo from repo", "branch rm"},
		{"Alice pushed to branch stage", "push"},
		{"Bob pushed new branch foo to repo", "branch new"},
		{"Bob opened merge request !101", "MR open"},
		{"Starting deploy to stage", "deploy →"},
		{"Deploy to stage succeeded", "deploy ✓"},
		{"Deploy to prod failed", "deploy ✗"},
		{"closed merge request !100", "MR closed"},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			if got := extractVerb(c.text); got != c.want {
				t.Fatalf("extractVerb(%q) = %q; want %q", c.text, got, c.want)
			}
		})
	}
}

func TestGroupGitWorkflows_CollatesByIssueID(t *testing.T) {
	msgs := []goslack.Message{
		gitMsg("1714000000.000000", "Alice Smith (alice) approved merge request !100 FOO-123 - add answers to search"),
		gitMsg("1714000010.000000", "Alice Smith (alice) merged merge request !100 FOO-123"),
		gitMsg("1714000020.000000", "Bob Jones removed branch feature/FOO-123-bar"),
		gitMsg("1714000030.000000", "Alice Smith (alice) pushed to branch stage FOO-123"),
		gitMsg("1714000040.000000", "Starting deploy to stage"),
		gitMsg("1714000050.000000", "Deploy to stage succeeded"),
	}
	workflows, orphans := groupGitWorkflows(msgs)

	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans, got %d", len(orphans))
	}

	// Expect at least one workflow keyed on FOO-123 with multiple verbs
	var wpFound bool
	for _, w := range workflows {
		if w.Key != "FOO-123" {
			continue
		}
		wpFound = true
		verbs := dedupeKeepingOrder(w.Actions)
		joined := strings.Join(verbs, " → ")
		for _, v := range []string{"approved", "merged", "push"} {
			if !strings.Contains(joined, v) {
				t.Errorf("FOO-123 workflow missing %q in: %s", v, joined)
			}
		}
		if _, ok := w.Actors["alice"]; !ok {
			t.Errorf("expected alice in actors, got %v", w.Actors)
		}
	}
	if !wpFound {
		t.Fatalf("FOO-123 workflow not found; got %v", workflows)
	}
}

func TestGroupGitWorkflows_DeployFlagged(t *testing.T) {
	msgs := []goslack.Message{
		gitMsg("1714000040.000000", "Starting deploy to stage"),
		gitMsg("1714000050.000000", "Deploy to stage succeeded"),
	}
	workflows, _ := groupGitWorkflows(msgs)
	if len(workflows) == 0 {
		t.Fatal("expected workflow for deploy")
	}
	if workflows[0].Key != "deploy:stage" {
		t.Fatalf("key = %q; want deploy:stage", workflows[0].Key)
	}
	verbs := dedupeKeepingOrder(workflows[0].Actions)
	joined := strings.Join(verbs, " → ")
	if !strings.Contains(joined, "deploy ✓") {
		t.Errorf("expected deploy ✓ in %q", joined)
	}
}
