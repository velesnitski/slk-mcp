package tools

import (
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func refMsg(text string) goslack.Message {
	m := goslack.Message{}
	m.Text = text
	return m
}

func TestCollectReferences_DedupesAndCategorises(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{
			refMsg("see FOO-100 and BAR-200 then merge !50"),
			refMsg("FOO-100 again, also BAZ-7"),
			refMsg("pushed to branch release-9 of repo X"),
		},
		map[string][]goslack.Message{
			"1": {
				refMsg("approved !51 for QUX-300"),
				refMsg("removed branch feature/A-99 from X"),
			},
		})

	r := collectReferences([]*slack.ChannelUnread{cu})

	wantIssues := []string{"BAR-200", "BAZ-7", "FOO-100", "QUX-300"}
	if len(r.Issues) != len(wantIssues) {
		t.Fatalf("issues = %v; want %v", r.Issues, wantIssues)
	}
	for i, w := range wantIssues {
		if r.Issues[i] != w {
			t.Errorf("issues[%d] = %q; want %q", i, r.Issues[i], w)
		}
	}

	wantMRs := []string{"50", "51"}
	for i, w := range wantMRs {
		if r.MRs[i] != w {
			t.Errorf("mrs[%d] = %q; want %q", i, r.MRs[i], w)
		}
	}

	wantBranches := []string{"feature/A-99", "release-9"}
	for i, w := range wantBranches {
		if r.Branches[i] != w {
			t.Errorf("branches[%d] = %q; want %q", i, r.Branches[i], w)
		}
	}
}

func TestRenderReferences_EmptyWhenNoRefs(t *testing.T) {
	if got := renderReferences(References{}); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestRenderReferences_RendersAllSections(t *testing.T) {
	out := renderReferences(References{
		Issues:   []string{"FOO-1", "BAR-2"},
		MRs:      []string{"100"},
		Branches: []string{"main"},
	})
	for _, want := range []string{
		"## References",
		"issues: FOO-1, BAR-2",
		"MRs: !100",
		"branches: main",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

func TestCollectReferences_StripsSlackLinkMarkup(t *testing.T) {
	// IDs hidden inside <url|label> markup must still be detected
	// because stripSlackLinks runs first.
	cu := mkChannelUnread("a",
		[]goslack.Message{
			refMsg("see <https://example.test/issue/FOO-1|FOO-1> for the spec"),
		}, nil)
	r := collectReferences([]*slack.ChannelUnread{cu})
	if len(r.Issues) != 1 || r.Issues[0] != "FOO-1" {
		t.Fatalf("expected [FOO-1], got %v", r.Issues)
	}
}
