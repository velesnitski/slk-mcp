package slack

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// ADR 126: a thread mention the operator answered afterwards in the same
// thread is not pending.

func tmHit(channel, user, ts, threadTS, text string) map[string]any {
	link := "https://x.slack.com/archives/" + channel + "/p" + strings.ReplaceAll(ts, ".", "")
	if threadTS != "" {
		link += "?thread_ts=" + threadTS
	}
	return map[string]any{
		"type": "message", "channel": map[string]any{"id": channel, "name": "alpha"},
		"user": user, "text": text, "ts": ts, "permalink": link,
	}
}

func TestUnreadThreadMentions_DropsMentionsAnsweredLaterInTheThread(t *testing.T) {
	f := newFakeSlack(t)
	prev := nowUnixFn
	nowUnixFn = func() int64 { return 1700100000 }
	t.Cleanup(func() { nowUnixFn = prev })

	const answered, open = "1700090000.000100", "1700091000.000100"
	f.on("search.messages", func(r *http.Request) any {
		_ = r.ParseForm()
		var matches []map[string]any
		if strings.Contains(r.Form.Get("query"), "from:me") {
			matches = []map[string]any{
				// The operator replied after the first mention…
				tmHit("C1", "U_SELF", "1700099100.000100", answered, "on it"),
				// …and before the second one.
				tmHit("C1", "U_SELF", "1700098000.000100", open, "earlier note"),
				// Another author in a from:me result never counts.
				tmHit("C1", "U9", "1700099900.000100", open, "not mine"),
			}
		} else {
			matches = []map[string]any{
				tmHit("C1", "U2", "1700099000.000100", answered, "<@U_SELF> can you check?"),
				tmHit("C1", "U3", "1700099500.000100", open, "<@U_SELF> and this one?"),
			}
		}
		return map[string]any{"ok": true, "messages": map[string]any{"total": len(matches), "matches": matches}}
	})

	out, err := newTestUnreadServiceWithSearch(t, f).UnreadThreadMentions(context.Background(), 24)
	if err != nil {
		t.Fatalf("UnreadThreadMentions: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want one channel, got %d", len(out))
	}
	if n := len(out[0].Replies[answered]); n != 0 {
		t.Fatalf("the answered mention is still shown: %+v", out[0].Replies[answered])
	}
	if n := len(out[0].Replies[open]); n != 1 {
		t.Fatalf("the open mention must stay: %+v", out[0].Replies)
	}
}

func TestUnreadThreadMentions_OwnPostsLookupFailingShowsEveryMention(t *testing.T) {
	f := newFakeSlack(t)
	prev := nowUnixFn
	nowUnixFn = func() int64 { return 1700100000 }
	t.Cleanup(func() { nowUnixFn = prev })

	f.on("search.messages", func(r *http.Request) any {
		_ = r.ParseForm()
		if strings.Contains(r.Form.Get("query"), "from:me") {
			return map[string]any{"ok": false, "error": "ratelimited"}
		}
		return map[string]any{"ok": true, "messages": map[string]any{"total": 1, "matches": []map[string]any{
			tmHit("C1", "U2", "1700099000.000100", "1700090000.000100", "<@U_SELF> can you check?"),
		}}}
	})

	out, err := newTestUnreadServiceWithSearch(t, f).UnreadThreadMentions(context.Background(), 24)
	if err != nil {
		t.Fatalf("UnreadThreadMentions: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("a failed lookup must not hide mentions: %+v", out)
	}
}

// ADR 127: a top-level mention the operator posted after in the same
// channel is kept and annotated, never dropped.
func TestUnreadThreadMentions_AnnotatesATopLevelMentionFollowedByAnOwnPost(t *testing.T) {
	f := newFakeSlack(t)
	prev := nowUnixFn
	nowUnixFn = func() int64 { return 1700100000 }
	t.Cleanup(func() { nowUnixFn = prev })

	const followed, earlier, other = "1700090000.000100", "1700095000.000100", "1700096000.000100"
	f.on("search.messages", func(r *http.Request) any {
		_ = r.ParseForm()
		var matches []map[string]any
		if strings.Contains(r.Form.Get("query"), "from:me") {
			matches = []map[string]any{
				tmHit("C1", "U_SELF", "1700094000.000100", "", "good morning, both teams are on it"),
				// A thread reply is not a channel follow-up.
				tmHit("C2", "U_SELF", "1700099000.000100", "1700000000.000100", "in a thread"),
			}
		} else {
			matches = []map[string]any{
				tmHit("C1", "U2", followed, "", "<@U_SELF> have you seen this?"),
				tmHit("C1", "U3", earlier, "", "<@U_SELF> and this?"),
				tmHit("C2", "U4", other, "", "<@U_SELF> elsewhere"),
			}
		}
		return map[string]any{"ok": true, "messages": map[string]any{"total": len(matches), "matches": matches}}
	})

	out, err := newTestUnreadServiceWithSearch(t, f).UnreadThreadMentions(context.Background(), 24)
	if err != nil {
		t.Fatalf("UnreadThreadMentions: %v", err)
	}
	byID := map[string]*ChannelUnread{}
	for _, cu := range out {
		byID[cu.Channel.ID] = cu
	}
	c1 := byID["C1"]
	if c1 == nil || len(c1.Messages) != 2 {
		t.Fatalf("both C1 mentions must be kept: %+v", c1)
	}
	if c1.FollowUps[followed] != "1700094000.000100" {
		t.Fatalf("the followed-up mention is not annotated: %v", c1.FollowUps)
	}
	if _, ok := c1.FollowUps[earlier]; ok {
		t.Fatalf("a mention newer than the own post must not be annotated: %v", c1.FollowUps)
	}
	if c2 := byID["C2"]; c2 == nil || len(c2.FollowUps) != 0 {
		t.Fatalf("a thread reply is not a channel follow-up: %+v", c2)
	}
}
