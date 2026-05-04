package tools

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
	issueIDRe   = regexp.MustCompile(`\b([A-Z]{2,5}-\d{1,5})\b`)
	mrIDRe      = regexp.MustCompile(`!(\d{2,5})\b`)
	branchRe    = regexp.MustCompile(`(?:branch|to branch) ([\w./-]+?)(?:\s+(?:of|from|to)\b|$)`)
	deployRe    = regexp.MustCompile(`(?i)deploy(?:ing)? to (\w+)`)
	deployOkRe  = regexp.MustCompile(`(?i)deploy.* (succeeded|completed)`)
	deployErrRe = regexp.MustCompile(`(?i)deploy.* (failed|aborted)`)
	personRe    = regexp.MustCompile(`\(([a-z][\w.-]+)\)`)
	pipelineOkRe = regexp.MustCompile(`(?i)Pipeline #?\d+ has passed`)
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

type gitAction struct {
	verb string
	ts   string
	by   string
}

type gitWorkflow struct {
	Key      string
	Actions  []gitAction
	Actors   map[string]struct{}
	Commits  []string
}

// detectGitChannel reports whether a channel is a CI / git-bot feed
// — a stricter form of log channel where messages collate into
// per-issue workflow stories rather than per-severity histograms.
func detectGitChannel(cu *slack.ChannelUnread) bool {
	if !detectLogChannel(cu) {
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

func extractWorkflowKey(text string) string {
	clean := stripSlackLinks(text)
	repo := extractRepo(clean)
	prefix := ""
	if repo != "" {
		prefix = repo + " · "
	}
	if m := issueIDRe.FindString(clean); m != "" {
		return prefix + m
	}
	if m := mrIDRe.FindString(clean); m != "" {
		return prefix + m
	}
	if m := branchRe.FindStringSubmatch(clean); len(m) > 1 && m[1] != "" {
		return prefix + "branch " + m[1]
	}
	if m := deployRe.FindStringSubmatch(clean); len(m) > 1 {
		return "deploy:" + m[1]
	}
	if repo != "" {
		return repo
	}
	return ""
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

// groupGitWorkflows collates git/CI channel messages by issue ID,
// MR ID, branch name, or deploy target. Messages that yield no key
// are dropped from the workflow view (caller can still surface them
// via a fallback).
func groupGitWorkflows(messages []goslack.Message) ([]gitWorkflow, []goslack.Message) {
	byKey := map[string]*gitWorkflow{}
	var order []string
	var orphans []goslack.Message

	for _, m := range messages {
		key := extractWorkflowKey(m.Text)
		if key == "" {
			orphans = append(orphans, m)
			continue
		}
		w, ok := byKey[key]
		if !ok {
			w = &gitWorkflow{Key: key, Actors: map[string]struct{}{}}
			byKey[key] = w
			order = append(order, key)
		}
		verb := extractVerb(m.Text)
		actor := extractActor(m.Text)
		if actor != "" {
			w.Actors[actor] = struct{}{}
		}
		if verb != "" {
			w.Actions = append(w.Actions, gitAction{verb: verb, ts: m.Timestamp, by: actor})
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

// renderGitChannel produces a compact per-workflow digest for a
// git/CI channel.
func renderGitChannel(channelLabel string, total int, workflows []gitWorkflow, orphans []goslack.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s [GIT MODE — %d msgs]\n", channelLabel, total)
	if len(workflows) == 0 && len(orphans) == 0 {
		b.WriteString("(no workflows)")
		return b.String()
	}

	for _, w := range workflows {
		actorList := make([]string, 0, len(w.Actors))
		for a := range w.Actors {
			actorList = append(actorList, a)
		}
		sort.Strings(actorList)

		verbs := dedupeKeepingOrder(w.Actions)
		first, last := timeRange(w.Actions)
		actors := strings.Join(actorList, "/")
		if actors == "" {
			actors = "—"
		}

		when := formatTimeRange(first, last)
		fmt.Fprintf(&b, "- %s [%s]: %s — %s\n",
			w.Key, when, strings.Join(verbs, " → "), actors)
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
