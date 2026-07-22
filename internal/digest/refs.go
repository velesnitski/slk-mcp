package digest

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

var (
	refIssueRe  = regexp.MustCompile(`\b([A-Z]{2,5}-\d{1,5})\b`)
	refMRRe     = regexp.MustCompile(`!(\d{2,5})\b`)
	refBranchRe = regexp.MustCompile(`(?:branch|to branch) ([\w./-]+?)(?:\s+(?:of|from|to)\b|$)`)
)

// References is a deduplicated extract of issue IDs / MR numbers /
// branch names referenced anywhere in the digest. Emitted as a
// footer to give downstream MCP orchestration (yt-mcp, gl-mcp,
// jira-mcp, linear-mcp, …) a clean batchable list instead of having
// to re-parse the prose.
type References struct {
	Issues   []string // e.g. FOO-123, BAR-456
	MRs      []string // e.g. !100
	Branches []string // e.g. release-x, feature/foo-bar
}

func CollectReferences(results []*slack.ChannelUnread) References {
	issues := map[string]struct{}{}
	mrs := map[string]struct{}{}
	branches := map[string]struct{}{}

	scan := func(text string) {
		clean := stripSlackLinks(text)
		for _, m := range refIssueRe.FindAllString(clean, -1) {
			issues[m] = struct{}{}
		}
		for _, m := range refMRRe.FindAllStringSubmatch(clean, -1) {
			if len(m) > 1 {
				mrs[m[1]] = struct{}{}
			}
		}
		for _, m := range refBranchRe.FindAllStringSubmatch(clean, -1) {
			if len(m) > 1 && m[1] != "" {
				branches[m[1]] = struct{}{}
			}
		}
	}

	for _, r := range results {
		for _, m := range r.Messages {
			scan(m.Text)
		}
		for _, rs := range r.Replies {
			for _, rm := range rs {
				scan(rm.Text)
			}
		}
	}

	return References{
		Issues:   sortedKeys(issues),
		MRs:      sortedKeys(mrs),
		Branches: sortedKeys(branches),
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RenderReferences returns a compact footer for the digest. Empty
// when no references were found.
func RenderReferences(r References) string {
	if len(r.Issues) == 0 && len(r.MRs) == 0 && len(r.Branches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## References\n")
	if len(r.Issues) > 0 {
		fmt.Fprintf(&b, "- issues: %s\n", strings.Join(r.Issues, ", "))
	}
	if len(r.MRs) > 0 {
		fmt.Fprintf(&b, "- MRs: %s\n", joinPrefix("!", r.MRs))
	}
	if len(r.Branches) > 0 {
		fmt.Fprintf(&b, "- branches: %s\n", strings.Join(r.Branches, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

func joinPrefix(prefix string, items []string) string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = prefix + s
	}
	return strings.Join(out, ", ")
}
