package digest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/format"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

var (
	issueIDRe     = regexp.MustCompile(`\b([A-Z]{2,5}-\d{1,5})\b`)
	mrIDRe        = regexp.MustCompile(`!(\d{2,5})\b`)
	branchRe      = regexp.MustCompile(`(?:branch|to branch) ([\w./-]+?)(?:\s+(?:of|from|to)\b|$)`)
	deployRe      = regexp.MustCompile(`(?i)deploy(?:ing)? to (\w+)`)
	deployOkRe    = regexp.MustCompile(`(?i)deploy.* (succeeded|completed)`)
	deployErrRe   = regexp.MustCompile(`(?i)deploy.* (failed|aborted)`)
	personRe      = regexp.MustCompile(`\(([a-z][\w.-]+)\)`)
	pipelineOkRe  = regexp.MustCompile(`(?i)Pipeline #?\d+ has passed`)
	pipelineErrRe = regexp.MustCompile(`(?i)Pipeline #?\d+ has failed`)
	// "of REPO / SUB / NAME" — last segment is the repo identity.
	// Slack renders <url|label> as just the label after stripping markup.
	repoRe = regexp.MustCompile(`(?:of|in) ([A-Z][\w. -]*?(?: ?/ ?[\w. -]+){1,4})`)
	// Commit subject: "<sha>: subject - author" or "<sha>: subject".
	commitRe = regexp.MustCompile(`\b[0-9a-f]{7,}: ([^-\n]{3,80}?)(?: - |$)`)
	// Slack URL markup: <url|label> → label; <url> → "".
	slackLinkRe = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]+)>`)
	bareLinkRe  = regexp.MustCompile(`<https?://[^>]+>`)
)

// Actor roles inferred from the verb. Empty role means "plain actor"
// (push, branch new/rm, comment, pipeline, deploy) — those don't get
// labelled in the rendered output.
const (
	roleAuthor   = "author"
	roleReviewer = "reviewer"
	roleMerger   = "merger"
)

type gitAction struct {
	verb string
	role string
	ts   string
	by   string
}

type gitWorkflow struct {
	Key     string
	Actions []gitAction
	Actors  map[string]struct{}
	Commits []string
	// Roles maps actor handle -> set of roles observed (author, reviewer,
	// merger). Plain-actor verbs leave the entry empty so renderers can
	// keep those names un-tagged.
	Roles map[string]map[string]struct{}
}

// gitFacts is the set of references parsed out of one bot message.
// Builds the alias map (branch -> MR-iid) and the workflow key.
type gitFacts struct {
	issues []string
	mr     string // "!iid" or ""
	branch string
	deploy string
	repo   string
}

// DetectGitChannel reports whether a channel is a CI / git-bot feed
// — a stricter form of log channel where messages collate into
// per-issue workflow stories rather than per-severity histograms.
func DetectGitChannel(cu *slack.ChannelUnread) bool {
	if !DetectLogChannel(cu) {
		return false
	}
	name := strings.ToLower(cu.Channel.Name)
	return strings.Contains(name, "git-") || strings.HasPrefix(name, "ci-") || strings.Contains(name, "ci/") || strings.Contains(name, "deploy")
}

// stripSlackLinks replaces <url|label> with label and drops bare <url>
// so downstream regexes operate on human-readable text only.
func stripSlackLinks(text string) string {
	text = slackLinkRe.ReplaceAllString(text, "$2")
	text = bareLinkRe.ReplaceAllString(text, "")
	return text
}

// extractGitFacts parses everything we want to know about one bot
// message in a single sweep. Cheap and idempotent.
func extractGitFacts(text string) gitFacts {
	clean := stripSlackLinks(text)
	f := gitFacts{repo: extractRepo(clean)}

	// All issue IDs (deduped, original order).
	if hits := issueIDRe.FindAllString(clean, -1); len(hits) > 0 {
		seen := map[string]struct{}{}
		for _, h := range hits {
			if _, ok := seen[h]; ok {
				continue
			}
			seen[h] = struct{}{}
			f.issues = append(f.issues, h)
		}
	}

	if m := mrIDRe.FindStringSubmatch(clean); len(m) > 1 {
		f.mr = "!" + m[1]
	}
	if m := branchRe.FindStringSubmatch(clean); len(m) > 1 {
		f.branch = m[1]
	}
	if m := deployRe.FindStringSubmatch(clean); len(m) > 1 {
		f.deploy = m[1]
	}
	return f
}

// chooseWorkflowKey picks the most informative grouping key for a
// message, in priority order:
//
//  1. MR-iid in the message text.
//  2. Branch name that an earlier message has already linked to an
//     MR-iid (consulting branchAliases).
//  3. Issue ID.
//  4. Raw branch name.
//  5. Deploy target.
//  6. Repo only.
//
// branchAliases may be nil (e.g. when called from extractWorkflowKey
// in a context-free unit test).
func chooseWorkflowKey(f gitFacts, branchAliases map[string]string) string {
	prefix := ""
	if f.repo != "" {
		prefix = f.repo + " · "
	}

	if f.mr != "" {
		return prefix + f.mr
	}
	if f.branch != "" {
		if mr, ok := branchAliases[f.branch]; ok {
			return prefix + mr
		}
	}
	if len(f.issues) > 0 {
		return prefix + f.issues[0]
	}
	if f.branch != "" {
		return prefix + "branch " + f.branch
	}
	if f.deploy != "" {
		return "deploy:" + f.deploy
	}
	if f.repo != "" {
		return f.repo
	}
	return ""
}

// extractWorkflowKey is kept for callers (and tests) that classify a
// single message in isolation. Production grouping uses
// GroupGitWorkflows which builds an alias map across the whole batch.
func extractWorkflowKey(text string) string {
	return chooseWorkflowKey(extractGitFacts(text), nil)
}

func extractRepo(text string) string {
	m := repoRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	parts := strings.Split(m[1], "/")
	last := strings.TrimSpace(parts[len(parts)-1])
	if len(last) > 40 {
		last = last[:40]
	}
	return last
}

// extractCommitSubject pulls the subject line from a "sha: subject"
// pattern, common in GitLab push notifications.
func extractCommitSubject(text string) string {
	m := commitRe.FindStringSubmatch(text)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func extractActor(text string) string {
	if m := personRe.FindStringSubmatch(text); len(m) > 1 {
		return m[1]
	}
	return ""
}

func extractVerb(text string) string {
	lower := strings.ToLower(text)
	switch {
	case pipelineErrRe.MatchString(text):
		return "pipeline ✗"
	case pipelineOkRe.MatchString(text):
		return "pipeline ✓"
	case deployErrRe.MatchString(text):
		return "deploy ✗"
	case deployOkRe.MatchString(text):
		return "deploy ✓"
	case strings.Contains(lower, "starting deploy"):
		return "deploy →"
	case strings.Contains(lower, "merged merge request"):
		return "merged"
	case strings.Contains(lower, "merged"):
		return "merged"
	case strings.Contains(lower, "approved"):
		return "approved"
	case strings.Contains(lower, "removed branch"):
		return "branch rm"
	case strings.Contains(lower, "opened merge request"):
		return "MR open"
	case strings.Contains(lower, "closed merge request"):
		return "MR closed"
	case strings.Contains(lower, "pushed new branch"):
		return "branch new"
	case strings.Contains(lower, "pushed"):
		return "push"
	case strings.Contains(lower, "comment"):
		return "comment"
	}
	return ""
}

// roleForVerb returns the actor role implied by a verb, or "" when
// the verb does not imply a structured role (e.g. push, comment,
// pipeline, deploy).
func roleForVerb(verb string) string {
	switch verb {
	case "MR open":
		return roleAuthor
	case "approved":
		return roleReviewer
	case "merged":
		return roleMerger
	}
	return ""
}

// buildBranchAliases walks all messages once to discover branch ↔ MR-iid
// pairs that co-occur in any single bot message. The result lets the
// second pass canonicalise events about the same branch under the MR
// they belong to (so "branch new" and "branch rm" no longer appear as
// separate workflows from the MR they belong to).
func buildBranchAliases(messages []goslack.Message) map[string]string {
	aliases := map[string]string{}
	for _, m := range messages {
		f := extractGitFacts(m.Text)
		if f.mr != "" && f.branch != "" {
			aliases[f.branch] = f.mr
		}
	}
	return aliases
}

// GroupGitWorkflows collates git/CI channel messages into per-MR /
// per-branch / per-deploy stories. Two passes:
//
//  1. Build a branch ↔ MR-iid alias map by scanning all messages.
//  2. Choose a canonical key per message (MR-iid wins; branch falls
//     through to its aliased MR; issue ID is a fallback) and bucket
//     events under it. Track actor roles inferred from the verb so
//     authors, reviewers, and mergers stay distinguishable.
//
// Messages that yield no key become orphans (caller can render them
// inline or as a count).
func GroupGitWorkflows(messages []goslack.Message) ([]gitWorkflow, []goslack.Message) {
	aliases := buildBranchAliases(messages)

	byKey := map[string]*gitWorkflow{}
	var order []string
	var orphans []goslack.Message

	for _, m := range messages {
		f := extractGitFacts(m.Text)
		key := chooseWorkflowKey(f, aliases)
		if key == "" {
			orphans = append(orphans, m)
			continue
		}
		w, ok := byKey[key]
		if !ok {
			w = &gitWorkflow{
				Key:    key,
				Actors: map[string]struct{}{},
				Roles:  map[string]map[string]struct{}{},
			}
			byKey[key] = w
			order = append(order, key)
		}
		verb := extractVerb(m.Text)
		actor := extractActor(m.Text)
		role := roleForVerb(verb)

		if actor != "" {
			w.Actors[actor] = struct{}{}
			if role != "" {
				if w.Roles[actor] == nil {
					w.Roles[actor] = map[string]struct{}{}
				}
				w.Roles[actor][role] = struct{}{}
			}
		}
		if verb != "" {
			w.Actions = append(w.Actions, gitAction{
				verb: verb, role: role, ts: m.Timestamp, by: actor,
			})
		}
		if subject := extractCommitSubject(stripSlackLinks(m.Text)); subject != "" {
			w.Commits = appendUnique(w.Commits, subject)
		}
	}

	out := make([]gitWorkflow, 0, len(order))
	for _, k := range order {
		w := byKey[k]
		sort.Slice(w.Actions, func(i, j int) bool { return w.Actions[i].ts < w.Actions[j].ts })
		out = append(out, *w)
	}
	return out, orphans
}

// renderActors formats a workflow's actor list with role tags
// ("alice(author/merger)") for actors whose role is known, and bare
// names for everyone else. Stable alphabetical order keeps prompt
// caches warm.
func renderActors(w gitWorkflow) string {
	if len(w.Actors) == 0 {
		return "—"
	}
	out := make([]string, 0, len(w.Actors))
	for a := range w.Actors {
		roles := w.Roles[a]
		if len(roles) == 0 {
			out = append(out, a)
			continue
		}
		// Stable role order: author, reviewer, merger.
		var rs []string
		for _, r := range []string{roleAuthor, roleReviewer, roleMerger} {
			if _, ok := roles[r]; ok {
				rs = append(rs, r)
			}
		}
		out = append(out, fmt.Sprintf("%s(%s)", a, strings.Join(rs, "/")))
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// RenderGitChannel produces a compact per-workflow digest for a
// git/CI channel.
func RenderGitChannel(channelLabel string, total int, workflows []gitWorkflow, orphans []goslack.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s [GIT MODE — %d msgs]\n", channelLabel, total)
	if len(workflows) == 0 && len(orphans) == 0 {
		b.WriteString("(no workflows)")
		return b.String()
	}

	for _, w := range workflows {
		verbs := dedupeKeepingOrder(w.Actions)
		first, last := timeRange(w.Actions)
		when := formatTimeRange(first, last)
		actors := renderActors(w)
		joined := joinVerbs(verbs)
		if actors == "—" {
			// Drop the trailing " — —" segment when there are no actors
			// to show — saves a few tokens and avoids reading like a typo.
			fmt.Fprintf(&b, "- %s [%s]: %s\n", w.Key, when, joined)
		} else {
			fmt.Fprintf(&b, "- %s [%s]: %s — %s\n",
				w.Key, when, joined, actors)
		}
		for i, c := range w.Commits {
			if i >= 3 {
				fmt.Fprintf(&b, "    · +%d more commits\n", len(w.Commits)-3)
				break
			}
			fmt.Fprintf(&b, "    · %s\n", c)
		}
	}

	if len(orphans) > 0 {
		fmt.Fprintf(&b, "+ %d uncategorized event(s)\n", len(orphans))
	}
	return strings.TrimRight(b.String(), "\n")
}

// joinVerbs renders the verb chain with " → " between entries, but
// elides the separator when the previous verb already ends in an
// arrow (e.g. "deploy →"). Without this we'd get "deploy → → deploy ✓"
// — readable but ugly and wasteful of tokens.
func joinVerbs(verbs []string) string {
	if len(verbs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, v := range verbs {
		if i == 0 {
			b.WriteString(v)
			continue
		}
		if strings.HasSuffix(verbs[i-1], "→") {
			b.WriteByte(' ')
		} else {
			b.WriteString(" → ")
		}
		b.WriteString(v)
	}
	return b.String()
}

func dedupeKeepingOrder(actions []gitAction) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, a := range actions {
		if _, ok := seen[a.verb]; ok {
			continue
		}
		seen[a.verb] = struct{}{}
		out = append(out, a.verb)
	}
	return out
}

func appendUnique(slice []string, s string) []string {
	for _, x := range slice {
		if x == s {
			return slice
		}
	}
	return append(slice, s)
}

func timeRange(actions []gitAction) (string, string) {
	if len(actions) == 0 {
		return "", ""
	}
	first := actions[0].ts
	last := actions[len(actions)-1].ts
	return first, last
}

func formatTimeRange(firstTS, lastTS string) string {
	first := format.ParseTS(firstTS)
	last := format.ParseTS(lastTS)
	if first.IsZero() {
		return "?"
	}
	if last.IsZero() || first.Equal(last) {
		return first.Format("15:04")
	}
	return first.Format("15:04") + "–" + last.Format("15:04")
}
