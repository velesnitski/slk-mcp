package slack

import (
	"context"
	"testing"
)

func TestSearchMessages_ReturnsTheMatchesAndSortsNewestFirst(t *testing.T) {
	f := svServer(t)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{
		{"ts": "200.000000", "text": "newer", "channel": svJSON{"id": "C1", "name": "alpha"}},
		{"ts": "100.000000", "text": "older", "channel": svJSON{"id": "C1", "name": "alpha"}},
	}}})

	got, err := svSearch(t, f).Messages(context.Background(), "from:me", 50)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(got) != 2 || got[0].Text != "newer" {
		t.Fatalf("got = %+v", got)
	}
	form := f.form(t, "search.messages", 0)
	if form.Get("query") != "from:me" {
		t.Fatalf("query = %q", form.Get("query"))
	}
	if form.Get("sort") != "timestamp" {
		t.Fatalf("sort = %q, want timestamp", form.Get("sort"))
	}
	if form.Get("count") != "50" {
		t.Fatalf("count = %q", form.Get("count"))
	}
}

// A non-positive count means "use the default", and slack-go omits a
// count equal to its own default — so nothing goes on the wire.
func TestSearchMessages_NonPositiveCountFallsBackToTheDefault(t *testing.T) {
	f := svServer(t)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{}}})

	if _, err := svSearch(t, f).Messages(context.Background(), "alpha", 0); err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := f.form(t, "search.messages", 0).Get("count"); got != "" {
		t.Fatalf("count = %q, want it left at the API default", got)
	}
}

// DEFECT PIN — SearchService.Messages leaves SearchParameters.Page at
// its Go zero value. slack-go only omits Page when it equals its own
// default of 1, so every search this server issues goes out with
// `page=0`, which is not a valid 1-indexed page number. Recorded, not
// fixed.
func TestSearchMessages_SendsPageZeroOnTheWire(t *testing.T) {
	f := svServer(t)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{}}})

	if _, err := svSearch(t, f).Messages(context.Background(), "alpha", 10); err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if got := f.form(t, "search.messages", 0).Get("page"); got != "0" {
		t.Fatalf("page = %q, want the current (invalid) 0 — update this pin if it was fixed", got)
	}
}

func TestSearchMessages_SlackErrorIsWrappedNotReturnedAsNoResults(t *testing.T) {
	f := svServer(t)
	f.fail("search.messages", "not_allowed_token_type")

	got, err := svSearch(t, f).Messages(context.Background(), "alpha", 10)
	if err == nil {
		t.Fatal("Messages returned nil error — a refused search must not read as 'nothing found'")
	}
	if got != nil {
		t.Fatalf("got = %+v, want nil", got)
	}
	if !svHasSubstr(err.Error(), "search.messages") || !svHasSubstr(err.Error(), "not_allowed_token_type") {
		t.Fatalf("err = %q", err)
	}
}

func TestSearchMessages_NoMatchesIsAnEmptyResult(t *testing.T) {
	f := svServer(t)
	f.reply("search.messages", svJSON{"ok": true, "messages": svJSON{"matches": []svJSON{}}})

	got, err := svSearch(t, f).Messages(context.Background(), "alpha", 10)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want empty", got)
	}
}
