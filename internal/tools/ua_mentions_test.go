package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
)

// uaMentionDefaults mirrors the get_mentions handler's defaults.
func uaMentionDefaults() mentionParams {
	return mentionParams{hours: 72, limit: 30, ctxN: 3, dmHistory: true}
}

func TestRunMentions_MergesBothSearchQueriesNewestFirst(t *testing.T) {
	f := uaNewFakeSlack(t)
	dmTS := uaTS(-600, 100)
	chTS := uaTS(-120, 100)

	f.On("auth.test", uaAuthOK)
	f.uaConversations(t) // the DM backstop finds no conversations
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {uaHit("D1", "U2", dmTS, "U2", "can you approve the invoice?", "")},
		"@alex": {uaHit("C1", "alpha", chTS, "U3", "<@U1> please review", "")},
	})
	h := uaHub(t, f)

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))

	if !strings.Contains(out, "2 mentions (last 72h)") {
		t.Fatalf("both queries should feed one count:\n%s", out)
	}
	if !strings.Contains(out, "can you approve the invoice?") || !strings.Contains(out, "please review") {
		t.Fatalf("both hits must render:\n%s", out)
	}
	if strings.Index(out, "please review") > strings.Index(out, "approve the invoice") {
		t.Fatalf("hits must be ordered newest-first:\n%s", out)
	}
	if f.CountOf("search.messages") != 2 {
		t.Fatalf("to:me and @handle are both required; calls=%v", f.Calls())
	}
}

func TestRunMentions_EmptyResultIsExplicit(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaNoSearch()
	h := uaHub(t, f)

	res := h.runMentions(context.Background(), uaMentionDefaults(), "")
	if res.IsError {
		t.Fatalf("no mentions is not an error: %q", resultText(res))
	}
	if got := resultText(res); got != "no mentions in last 72h" {
		t.Fatalf("empty sweep must read as empty, got %q", got)
	}

	p := uaMentionDefaults()
	p.pendingOnly = true
	got := resultText(h.runMentions(context.Background(), p, ""))
	if !strings.Contains(got, "no pending mentions in last 72h") {
		t.Fatalf("pending_only has its own wording, got %q", got)
	}
}

func TestRunMentions_SearchFailureIsAnError(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.OnError("search.messages", "not_allowed_token_type")
	h := uaHub(t, f)

	res := h.runMentions(context.Background(), uaMentionDefaults(), "")
	if res == nil || !res.IsError {
		t.Fatalf("a failed search must not read as 'no mentions', got %+v", res)
	}
	if !strings.Contains(resultText(res), "not_allowed_token_type") {
		t.Fatalf("error should name the upstream failure, got %q", resultText(res))
	}
}

func TestRunMentions_SummaryModeAggregatesInsteadOfListing(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {
			uaHit("D1", "U2", uaTS(-600, 100), "U2", "first ask", ""),
			uaHit("D1", "U2", uaTS(-500, 100), "U2", "second ask", ""),
		},
		"@alex": {uaHit("C1", "alpha", uaTS(-400, 100), "U3", "<@U1> third ask", "")},
	})
	h := uaHub(t, f)

	p := uaMentionDefaults()
	p.summaryOnly = true
	out := resultText(h.runMentions(context.Background(), p, ""))

	if !strings.Contains(out, "3 mentions (last 72h) — summary") {
		t.Fatalf("summary header missing:\n%s", out)
	}
	if !strings.Contains(out, "split: 2 DM / 1 channel · 2 unique senders") {
		t.Fatalf("DM/channel split wrong:\n%s", out)
	}
	if !strings.Contains(out, "senders: U2×2, U3×1") {
		t.Fatalf("sender counts wrong:\n%s", out)
	}
	if !strings.Contains(out, "channels: #alpha×1") {
		t.Fatalf("channel counts wrong:\n%s", out)
	}
	if strings.Contains(out, "first ask") {
		t.Fatalf("summary mode must not list the bodies:\n%s", out)
	}
}

func TestRunMentions_FiltersRequiringSelfIDFailWhenAuthFails(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*mentionParams)
		want string
	}{
		{"pending_only", func(p *mentionParams) { p.pendingOnly = true }, "pending_only requires auth.test"},
		{"strict_mention", func(p *mentionParams) { p.strictMention = true }, "strict_mention requires auth.test"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := uaNewFakeSlack(t)
			f.OnError("auth.test", "invalid_auth")
			f.uaSearchByQuery(t, map[string][]map[string]any{
				"to:me": {uaHit("D1", "U2", uaTS(-600, 100), "U2", "an ask", "")},
			})
			h := uaHub(t, f)

			p := uaMentionDefaults()
			c.set(&p)
			res := h.runMentions(context.Background(), p, "")
			if res == nil || !res.IsError {
				t.Fatalf("filter without a self id must fail loudly, got %+v", res)
			}
			if !strings.Contains(resultText(res), c.want) {
				t.Fatalf("unexpected error text: %q", resultText(res))
			}
		})
	}
}

func TestRunMentions_PendingOnlyDropsTheAnsweredMention(t *testing.T) {
	f := uaNewFakeSlack(t)
	answeredTS := uaTS(-900, 100)
	pendingTS := uaTS(-800, 100)

	f.On("auth.test", uaAuthOK)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {
			uaHit("D1", "U2", answeredTS, "U2", "already answered ask", ""),
			uaHit("D2", "U3", pendingTS, "U3", "still waiting ask", ""),
		},
	})
	f.uaHistory(t, map[string][]map[string]any{
		// D1: the operator posted a real reply after the mention.
		"D1": {uaMsg(uaTS(-700, 100), "U1", "answered you here", nil)},
		// D2: only the counterpart spoke afterwards.
		"D2": {uaMsg(uaTS(-600, 100), "U3", "bumping this", nil)},
	})
	f.uaReplies(t, map[string][]map[string]any{})
	h := uaHub(t, f)

	p := uaMentionDefaults()
	p.pendingOnly = true
	p.dmHistory = false
	out := resultText(h.runMentions(context.Background(), p, ""))

	if !strings.Contains(out, "pending (no text reply from you)") {
		t.Fatalf("header should state the filter:\n%s", out)
	}
	if strings.Contains(out, "already answered ask") {
		t.Fatalf("an answered mention must be dropped:\n%s", out)
	}
	if !strings.Contains(out, "still waiting ask") {
		t.Fatalf("an unanswered mention must survive:\n%s", out)
	}
	if !strings.Contains(out, "1 mentions (last 72h)") {
		t.Fatalf("count must reflect the filtered set:\n%s", out)
	}
}

func TestRunMentions_PendingOnlyDropsEmptyBodiesAndClosingAcks(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {
			uaHit("D1", "U2", uaTS(-900, 100), "U2", "   ", ""),
			uaHit("D2", "U3", uaTS(-800, 100), "U3", "спасибо", ""),
			uaHit("D3", "U4", uaTS(-700, 100), "U4", "real question here?", ""),
		},
	})
	f.uaHistory(t, map[string][]map[string]any{})
	f.uaReplies(t, map[string][]map[string]any{})
	h := uaHub(t, f)

	p := uaMentionDefaults()
	p.pendingOnly = true
	p.dropAcks = true
	p.dmHistory = false
	out := resultText(h.runMentions(context.Background(), p, ""))

	if strings.Contains(out, "спасибо") {
		t.Fatalf("a closing ack is not a pending ask:\n%s", out)
	}
	if !strings.Contains(out, "real question here?") {
		t.Fatalf("the real ask must survive:\n%s", out)
	}
	if !strings.Contains(out, "1 mentions") {
		t.Fatalf("empty-body and ack hits must be counted out:\n%s", out)
	}
}

func TestRunMentions_StrictMentionKeepsOnlyLiteralTags(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"@alex": {
			uaHit("C1", "alpha", uaTS(-600, 100), "U2", "team, heads up on the rollout", ""),
			uaHit("C2", "beta", uaTS(-500, 100), "U3", "<@U1> your call", ""),
		},
	})
	h := uaHub(t, f)

	p := uaMentionDefaults()
	p.strictMention = true
	out := resultText(h.runMentions(context.Background(), p, ""))

	if strings.Contains(out, "heads up on the rollout") {
		t.Fatalf("a channel-wide hit is not a direct mention:\n%s", out)
	}
	if !strings.Contains(out, "your call") {
		t.Fatalf("the literal tag must survive:\n%s", out)
	}
}

func TestRunMentions_DropsAutomationSendersAndOwnMessages(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	bot := uaHit("D1", "U2", uaTS(-600, 100), "B1", "<@U1> Today is a busy day", "")
	bot["username"] = "google calendar"
	mine := uaHit("C1", "alpha", uaTS(-500, 100), "U1", "I said this myself", "")
	human := uaHit("C1", "alpha", uaTS(-400, 100), "U2", "<@U1> a real ask", "")
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"@alex": {bot, mine, human},
	})
	h := uaHub(t, f)

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))

	if strings.Contains(out, "Today is a busy day") {
		t.Fatalf("automation senders must be dropped:\n%s", out)
	}
	if strings.Contains(out, "I said this myself") {
		t.Fatalf("the operator's own message is not a mention of them:\n%s", out)
	}
	if !strings.Contains(out, "1 mentions") || !strings.Contains(out, "a real ask") {
		t.Fatalf("the human mention must survive alone:\n%s", out)
	}
}

func TestRunMentions_DMHistoryBackstopSurfacesAnUnindexedDM(t *testing.T) {
	f := uaNewFakeSlack(t)
	freshTS := uaTS(-120, 100)

	f.On("auth.test", uaAuthOK)
	f.uaNoSearch() // Slack's index has not caught up yet
	f.uaConversations(t, uaChannel("D1", "", map[string]any{"is_im": true, "user": "U2"}))
	f.uaHistory(t, map[string][]map[string]any{
		"D1": {
			uaMsg(freshTS, "U2", "just sent, not yet indexed", nil),
			uaMsg(uaTS(-130, 100), "U1", "my own earlier line", nil),
		},
	})
	f.uaReplies(t, map[string][]map[string]any{})
	h := uaHub(t, f)

	withBackstop := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))
	if !strings.Contains(withBackstop, "just sent, not yet indexed") {
		t.Fatalf("the DM backstop must surface the fresh message:\n%s", withBackstop)
	}
	if strings.Contains(withBackstop, "my own earlier line") {
		t.Fatalf("the operator's own DM line is not a mention:\n%s", withBackstop)
	}

	p := uaMentionDefaults()
	p.dmHistory = false
	if got := resultText(h.runMentions(context.Background(), p, "")); got != "no mentions in last 72h" {
		t.Fatalf("dm_history=false should fall back to search-only, got %q", got)
	}
}

func TestRunMentions_WithContextInlinesSurroundingMessages(t *testing.T) {
	f := uaNewFakeSlack(t)
	pivot := uaTS(-600, 100)

	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{"U2": "Sam", "U3": "Pat"})
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"@alex": {uaHit("C1", "alpha", pivot, "U2", "<@U1> the question", "")},
	})
	f.uaHistory(t, map[string][]map[string]any{
		"C1": {
			uaMsg(uaTS(-500, 100), "U3", "a line after the ask", nil),
			uaMsg(pivot, "U2", "the question", nil),
			uaMsg(uaTS(-700, 100), "U3", "a line before the ask", nil),
		},
	})
	h := uaHub(t, f)

	p := uaMentionDefaults()
	p.withContext = true
	p.dmHistory = false
	out := resultText(h.runMentions(context.Background(), p, ""))

	if !strings.Contains(out, "↳ ") || !strings.Contains(out, "a line before the ask") {
		t.Fatalf("preceding context missing:\n%s", out)
	}
	if !strings.Contains(out, "↪ ") || !strings.Contains(out, "a line after the ask") {
		t.Fatalf("following context missing:\n%s", out)
	}
	if !strings.Contains(out, "Pat") {
		t.Fatalf("context authors should be resolved to names:\n%s", out)
	}
}

func TestRunMentions_LimitCapsTheMergedSet(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {
			uaHit("D1", "U2", uaTS(-300, 100), "U2", "newest ask", ""),
			uaHit("D1", "U2", uaTS(-400, 100), "U2", "middle ask", ""),
		},
		"@alex": {uaHit("C1", "alpha", uaTS(-500, 100), "U3", "oldest ask", "")},
	})
	h := uaHub(t, f)

	p := uaMentionDefaults()
	p.limit = 2
	out := resultText(h.runMentions(context.Background(), p, ""))

	if !strings.Contains(out, "2 mentions") {
		t.Fatalf("limit bounds the merged union:\n%s", out)
	}
	if strings.Contains(out, "oldest ask") {
		t.Fatalf("the cap must drop the oldest:\n%s", out)
	}
	if !strings.Contains(out, "newest ask") || !strings.Contains(out, "middle ask") {
		t.Fatalf("the newest hits must be kept:\n%s", out)
	}
}

func TestRunMentions_MultiWorkspaceSectionsAndPerWorkspaceEmptiness(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"@alex": {uaHit("C1", "alpha", uaTS(-300, 100), "U2", "<@U1> shared ask", "")},
	})
	h := uaMultiHub(t, f)

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))
	if !strings.Contains(out, "# Mentions — 2 workspaces") {
		t.Fatalf("multi-workspace header missing:\n%s", out)
	}
	if strings.Count(out, "shared ask") != 2 {
		t.Fatalf("each workspace renders its own section:\n%s", out)
	}
	if !strings.Contains(out, "## [primary]") || !strings.Contains(out, "## [secondary]") {
		t.Fatalf("both labels expected:\n%s", out)
	}
}

func TestRunMentions_MultiWorkspaceEmptyAndErrorSectionsAreLabelled(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.OnError("search.messages", "ratelimited_search")
	h := uaMultiHub(t, f)

	res := h.runMentions(context.Background(), uaMentionDefaults(), "")
	out := resultText(res)
	if res.IsError {
		t.Fatalf("multi-workspace mode reports per-section errors: %q", out)
	}
	if strings.Count(out, "_error: ") != 2 {
		t.Fatalf("each failing workspace needs its own error line:\n%s", out)
	}
}

func TestRunMentions_MultiWorkspaceSkipsTokenlessWorkspace(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaConversations(t)
	f.uaNoSearch()
	h := uaMultiHub(t, f, func(c *config.Config) {
		c.UserToken = ""
		c.BotToken = "xoxb-test"
	})

	out := resultText(h.runMentions(context.Background(), uaMentionDefaults(), ""))
	if !strings.Contains(out, "## [secondary]\n_skipped: no user token for this workspace_") {
		t.Fatalf("a token-less workspace must say so:\n%s", out)
	}
	if !strings.Contains(out, "## [primary]\nno mentions in last 72h") {
		t.Fatalf("the token-carrying workspace still reports emptiness:\n%s", out)
	}
}

func TestRunMentions_UnknownWorkspaceNamesTheConfiguredOnes(t *testing.T) {
	f := uaNewFakeSlack(t)
	h := uaMultiHub(t, f)

	res := h.runMentions(context.Background(), uaMentionDefaults(), "ghost")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace must be an error, got %+v", res)
	}
	if !strings.Contains(resultText(res), "configured: primary, secondary") {
		t.Fatalf("error should name the labels, got %q", resultText(res))
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("routing must fail before any Slack call, got %v", f.Calls())
	}
}

func TestRunMentions_DMBackstopFailureStillReturnsSearchHits(t *testing.T) {
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.OnError("users.conversations", "ratelimited_list") // the backstop cannot list DMs
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"@alex": {uaHit("C1", "alpha", uaTS(-300, 100), "U2", "<@U1> the indexed ask", "")},
	})
	h := uaHub(t, f)

	res := h.runMentions(context.Background(), uaMentionDefaults(), "")
	out := resultText(res)
	if res.IsError {
		t.Fatalf("a best-effort backstop failure must not fail the tool: %q", out)
	}
	if !strings.Contains(out, "the indexed ask") {
		t.Fatalf("the search hits must still be returned:\n%s", out)
	}
}
