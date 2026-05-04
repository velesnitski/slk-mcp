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
	issueIDRe  = regexp.MustCompile(`\b([A-Z]{2,5}-\d{1,5})\b`)
	mrIDRe     = regexp.MustCompile(`!(\d{2,5})\b`)
	branchRe   = regexp.MustCompile(`branch ['"<]?([\w./-]+)['">]?`)
	deployRe   = regexp.MustCompile(`(?i)deploy(?:ing)? to (\w+)`)
	deployOkRe = regexp.MustCompile(`(?i)deploy.* (succeeded|completed)`)
	deployErrRe = regexp.MustCompile(`(?i)deploy.* (failed|aborted)`)
	personRe   = regexp.MustCompile(`\(([a-z][\w.-]+)\)`)
)

type gitAction struct {
	verb string
	ts   string
	by   string
}

type gitWorkflow struct {
	Key     string
	Actions []gitAction
	Actors  map[string]struct{}
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

func extractWorkflowKey(text string) string {
	if m := issueIDRe.FindString(text); m != "" {
		return m
	}
	if m := mrIDRe.FindString(text); m != "" {
		return m
	}
	if m := branchRe.FindStringSubmatch(text); len(m) > 1 {
		return "branch " + m[1]
	}
	if m := deployRe.FindStringSubmatch(text); len(m) > 1 {
		return "deploy:" + m[1]
	}
	return ""
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
