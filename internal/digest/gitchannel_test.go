package digest

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
		// Single signal — straightforward.
		{"FOO-123 - add answers to search", "FOO-123"},
		{"merged !100", "!100"},
		{"removed branch feature/foo from Repo", "branch feature/foo"},
		{"Starting deploy to stage", "deploy:stage"},
		{"random text without anything", ""},
		// Mixed signals — MR-iid wins over issue ID, branch only loses to MR.
		{"merged !55 FOO-99 thing", "!55"},
		{"opened merge request !66 from branch feature/BAR-12", "!66"},
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

func TestRoleForVerb(t *testing.T) {
	cases := map[string]string{
		"MR open":  roleAuthor,
		"approved": roleReviewer,
		"merged":   roleMerger,
		// Plain-actor verbs have no implied role.
		"push":      "",
		"branch rm": "",
		"comment":   "",
		"deploy ✓":  "",
	}
	for verb, want := range cases {
		if got := roleForVerb(verb); got != want {
			t.Errorf("roleForVerb(%q) = %q; want %q", verb, got, want)
		}
	}
}

func TestGroupGitWorkflows_PrefersMRIidOverIssue(t *testing.T) {
	// When a message references both !55 and FOO-99, the MR-iid is the
	// canonical identity — issue IDs are often inherited from branch
	// names and may not match the MR title. The two events here must
	// coalesce under !55.
	msgs := []goslack.Message{
		gitMsg("100", "Alice (alice) approved merge request !55 FOO-99 thing"),
		gitMsg("110", "Alice (alice) merged merge request !55 FOO-99 thing"),
	}
	workflows, orphans := GroupGitWorkflows(msgs)
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans, got %d", len(orphans))
	}
	if len(workflows) != 1 {
		t.Fatalf("expected 1 workflow keyed by MR-iid, got %d: %+v", len(workflows), workflows)
	}
	if workflows[0].Key != "!55" {
		t.Fatalf("key=%q; want !55", workflows[0].Key)
	}
}

func TestGroupGitWorkflows_BranchAliasesToMR(t *testing.T) {
	// Branch lifecycle events ("opened MR from branch X", "branch X
	// removed") must collate with the MR they belong to once any single
	// message has linked branch ↔ MR.
	msgs := []goslack.Message{
		gitMsg("100", "Alice (alice) opened merge request !66 from branch feature/BAR-12-x"),
		gitMsg("110", "Bob (bob) removed branch feature/BAR-12-x from Repo"),
	}
	workflows, _ := GroupGitWorkflows(msgs)
	if len(workflows) != 1 {
		t.Fatalf("expected 1 workflow (branch should alias to !66), got %d: %+v", len(workflows), workflows)
	}
	if workflows[0].Key != "!66" {
		t.Fatalf("key=%q; want !66", workflows[0].Key)
	}
	verbs := dedupeKeepingOrder(workflows[0].Actions)
	if !strings.Contains(strings.Join(verbs, " "), "MR open") || !strings.Contains(strings.Join(verbs, " "), "branch rm") {
		t.Errorf("expected MR open and branch rm in: %v", verbs)
	}
}

func TestGroupGitWorkflows_TracksActorRoles(t *testing.T) {
	// Author, reviewer, and merger must remain distinguishable even
	// when the same person plays two roles (author + merger here).
	msgs := []goslack.Message{
		gitMsg("100", "Alice (alice) opened merge request !77"),
		gitMsg("110", "Bob (bob) approved merge request !77"),
		gitMsg("120", "Alice (alice) merged merge request !77"),
	}
	workflows, _ := GroupGitWorkflows(msgs)
	if len(workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(workflows))
	}
	w := workflows[0]
	if _, ok := w.Roles["alice"][roleAuthor]; !ok {
		t.Errorf("alice should be tagged author; got %v", w.Roles["alice"])
	}
	if _, ok := w.Roles["alice"][roleMerger]; !ok {
		t.Errorf("alice should be tagged merger; got %v", w.Roles["alice"])
	}
	if _, ok := w.Roles["bob"][roleReviewer]; !ok {
		t.Errorf("bob should be tagged reviewer; got %v", w.Roles["bob"])
	}

	rendered := renderActors(w)
	if !strings.Contains(rendered, "alice(author/merger)") {
		t.Errorf("expected alice(author/merger) in render, got %q", rendered)
	}
	if !strings.Contains(rendered, "bob(reviewer)") {
		t.Errorf("expected bob(reviewer) in render, got %q", rendered)
	}
}

func TestGroupGitWorkflows_MRWithoutIssueIDStillGroups(t *testing.T) {
	// MR titles without a ticket prefix (refactors, hotfixes named
	// purely for the change) used to fall through and either get
	// keyed off branch name or land in orphans. They must now group
	// cleanly under their MR-iid.
	msgs := []goslack.Message{
		gitMsg("100", "Alice (alice) opened merge request !88 — refactor logic"),
		gitMsg("110", "Alice (alice) merged merge request !88"),
	}
	workflows, orphans := GroupGitWorkflows(msgs)
	if len(orphans) != 0 {
		t.Errorf("expected 0 orphans, got %d", len(orphans))
	}
	if len(workflows) != 1 || workflows[0].Key != "!88" {
		t.Fatalf("expected single !88 workflow; got %+v", workflows)
	}
}

func TestGroupGitWorkflows_DeployFlagged(t *testing.T) {
	msgs := []goslack.Message{
		gitMsg("100", "Starting deploy to stage"),
		gitMsg("110", "Deploy to stage succeeded"),
	}
	workflows, _ := GroupGitWorkflows(msgs)
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

func TestJoinVerbs_ElidesArrowAfterArrowVerb(t *testing.T) {
	// "deploy →" should not be followed by another " → " separator,
	// otherwise we render "deploy → → deploy ✓" which is ugly and
	// wastes tokens.
	cases := []struct {
		verbs []string
		want  string
	}{
		{[]string{"deploy →", "deploy ✓"}, "deploy → deploy ✓"},
		{[]string{"deploy →", "deploy ✗"}, "deploy → deploy ✗"},
		{[]string{"MR open", "approved", "merged"}, "MR open → approved → merged"},
		{[]string{"push"}, "push"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := joinVerbs(c.verbs); got != c.want {
			t.Errorf("joinVerbs(%v) = %q; want %q", c.verbs, got, c.want)
		}
	}
}

func TestRenderGitChannel_DropsTrailingDashWhenNoActors(t *testing.T) {
	// Deploy events frequently have no parseable (handle) — we should
	// not render "— —" at the end of the line.
	msgs := []goslack.Message{
		gitMsg("100", "Starting deploy to production"),
		gitMsg("110", "Deploy to production succeeded"),
	}
	workflows, _ := GroupGitWorkflows(msgs)
	out := RenderGitChannel("#git-deploy", len(msgs), workflows, nil)

	if strings.Contains(out, "— —") {
		t.Errorf("rendered output contains stray '— —' suffix:\n%s", out)
	}
	if !strings.Contains(out, "deploy:production") {
		t.Errorf("expected deploy:production workflow line in:\n%s", out)
	}
}

func TestRenderGitChannel_KeepsActorsWhenPresent(t *testing.T) {
	// When actors ARE present, the trailing " — actors" segment must
	// stay — we only drop the segment in the "no actors" case.
	msgs := []goslack.Message{
		gitMsg("100", "Alice (alice) opened merge request !99"),
		gitMsg("110", "Alice (alice) merged merge request !99"),
	}
	workflows, _ := GroupGitWorkflows(msgs)
	out := RenderGitChannel("#git-test", len(msgs), workflows, nil)

	if !strings.Contains(out, "— alice") {
		t.Errorf("expected '— alice' (with role tag) in:\n%s", out)
	}
}

func TestRenderActors_NoRolesStaysBare(t *testing.T) {
	// Plain-actor verbs (push, branch new/rm, deploy, pipeline) must
	// not get spurious role tags like "alice(author)".
	w := gitWorkflow{
		Key:    "branch stage",
		Actors: map[string]struct{}{"alice": {}, "bob": {}},
		Roles:  map[string]map[string]struct{}{},
	}
	got := renderActors(w)
	if got != "alice bob" {
		t.Fatalf("renderActors=%q; want %q", got, "alice bob")
	}
}
