package tools

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/config"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func TestSelfTargets_FollowsTheSweepUnlessAllIsAsked(t *testing.T) {
	h := &Hub{registry: []slack.Workspace{{Name: "primary"}, {Name: "secondary"}, {Name: "third"}}}
	h.cfg = &config.Config{SweepExclude: []string{"third"}}

	apply, skipped := h.selfTargets("")
	if strings.Join(workspaceOrder(apply), ",") != "primary,secondary" || strings.Join(workspaceOrder(skipped), ",") != "third" {
		t.Fatalf("default: apply=%v skipped=%v", workspaceOrder(apply), workspaceOrder(skipped))
	}
	for _, all := range []string{"all", " ALL ", "*"} {
		if apply, skipped := h.selfTargets(all); len(apply) != 3 || skipped != nil {
			t.Fatalf("%q must reach every workspace", all)
		}
	}
	if apply, skipped := h.selfTargets("third"); len(apply) != 1 || skipped != nil {
		t.Fatal("a named workspace is reached even when excluded")
	}
	if apply, _ := h.selfTargets("ghost"); apply != nil {
		t.Fatal("unknown workspace must be nil")
	}
}

func TestSelfSkippedLines_NamesTheWayToReachIt(t *testing.T) {
	h := &Hub{registry: []slack.Workspace{{Name: "primary"}, {Name: "third"}}}
	h.cfg = &config.Config{}
	got := strings.Join(h.selfSkippedLines([]slack.Workspace{{Name: "third"}}), "\n")
	if !strings.Contains(got, "unchanged") || !strings.Contains(got, "workspace=third") || !strings.Contains(got, "workspace=all") {
		t.Fatalf("got %q", got)
	}
}

func TestPermalinkArg_AcceptsURLAndLink(t *testing.T) {
	mk := func(args map[string]any) mcp.CallToolRequest {
		var r mcp.CallToolRequest
		r.Params.Arguments = args
		return r
	}
	const link = "https://example.invalid/archives/C1/p1700000000000000"
	for _, key := range []string{"permalink", "url", "link"} {
		if got := permalinkArg(mk(map[string]any{key: " " + link + " "})); got != link {
			t.Fatalf("%s: got %q", key, got)
		}
	}
	if got := permalinkArg(mk(map[string]any{"permalink": link, "url": "other"})); got != link {
		t.Fatalf("permalink must win, got %q", got)
	}
	if permalinkArg(mk(map[string]any{})) != "" {
		t.Fatal("no arg → empty")
	}
}

func TestMergeThreadMentions_CarriesOwnThreads(t *testing.T) {
	base := []*slack.ChannelUnread{{Channel: goslack.Channel{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C1"}}}}}
	extra := []*slack.ChannelUnread{{
		Channel:    goslack.Channel{GroupConversation: goslack.GroupConversation{Conversation: goslack.Conversation{ID: "C1"}}},
		OwnThreads: map[string]bool{"1700000000.000100": true},
	}}
	got := mergeThreadMentions(base, extra)
	if len(got) != 1 || !got[0].OwnThreads["1700000000.000100"] {
		t.Fatalf("own thread lost in merge: %+v", got[0].OwnThreads)
	}
}
