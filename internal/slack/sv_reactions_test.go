package slack

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// ADR 119: the full reactor list comes from reactions.get with full=true.

func svReactionsBody() svJSON {
	return svJSON{"ok": true, "type": "message", "message": svJSON{
		"reactions": []svJSON{
			{"name": "tada", "count": 2, "users": []string{"U1", "U2"}},
			{"name": "heart", "count": 1, "users": []string{"U3"}},
		},
	}}
}

func TestReactions_AsksForTheFullListAndReturnsEveryUser(t *testing.T) {
	f := svServer(t)
	f.reply("reactions.get", svReactionsBody())

	got, err := svMessages(t, f).Reactions(context.Background(), "C1", "100.000100")
	if err != nil {
		t.Fatalf("Reactions: %v", err)
	}
	if len(got) != 2 || got[0].Name != "tada" || len(got[0].Users) != 2 || got[1].Users[0] != "U3" {
		t.Fatalf("got = %+v", got)
	}
	form := f.form(t, "reactions.get", 0)
	if form.Get("channel") != "C1" || form.Get("timestamp") != "100.000100" || form.Get("full") != "true" {
		t.Fatalf("form = %v, want channel, timestamp and full=true", form)
	}
}

func TestReactions_ErrorIsWrappedWithTheMethod(t *testing.T) {
	f := svServer(t)
	f.fail("reactions.get", "message_not_found")

	_, err := svMessages(t, f).Reactions(context.Background(), "C1", "1.0")
	if err == nil || !strings.Contains(err.Error(), "reactions.get") || !strings.Contains(err.Error(), "message_not_found") {
		t.Fatalf("err = %v", err)
	}
	if f.count("reactions.get") != 1 {
		t.Fatalf("calls = %d, want 1 without a second identity", f.count("reactions.get"))
	}
}

func TestReactions_ScopeRefusalRetriesWithTheUserToken(t *testing.T) {
	f := svServer(t)
	bot, user := "xox"+"b-test", "xox"+"p-test"
	var tokens []string
	f.on("reactions.get", func(r *http.Request) svJSON {
		_ = r.ParseForm()
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" {
			tok = r.Form.Get("token")
		}
		tokens = append(tokens, tok)
		if tok == bot {
			return svJSON{"ok": false, "error": "missing_scope"}
		}
		return svReactionsBody()
	})

	got, err := svMessagesWithFallback(t, f, bot, user).Reactions(context.Background(), "C1", "1.0")
	if err != nil {
		t.Fatalf("Reactions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got = %+v", got)
	}
	if len(tokens) != 2 || tokens[0] != bot || tokens[1] != user {
		t.Fatalf("tokens = %v, want bot then user", tokens)
	}
}

func TestReactions_NonAuthFailureIsNotRetried(t *testing.T) {
	f := svServer(t)
	f.fail("reactions.get", "message_not_found")

	_, err := svMessagesWithFallback(t, f, "xox"+"b-test", "xox"+"p-test").Reactions(context.Background(), "C1", "1.0")
	if err == nil {
		t.Fatal("want an error")
	}
	if f.count("reactions.get") != 1 {
		t.Fatalf("calls = %d, want 1 — a missing message must not burn the user token", f.count("reactions.get"))
	}
}
