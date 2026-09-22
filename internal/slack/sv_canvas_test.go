package slack

import (
	"context"
	"net/http"
	"sync"
	"testing"
)

func TestChannelCanvas_ReturnsTheAttachedCanvasFromThePrimaryIdentity(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
		"properties": svJSON{"canvas": svJSON{"file_id": "F1", "is_empty": false}},
	})})

	id, empty, err := svCanvas(t, f, "xoxb-test", "").ChannelCanvas(context.Background(), "C1")
	if err != nil {
		t.Fatalf("ChannelCanvas: %v", err)
	}
	if id != "F1" || empty {
		t.Fatalf("id = %q empty = %v, want F1/false", id, empty)
	}
	if n := f.count("conversations.info"); n != 1 {
		t.Fatalf("conversations.info called %d times, want 1 — the primary already answered", n)
	}
}

func TestChannelCanvas_ReportsAnEmptyCanvasAsPresentButEmpty(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
		"properties": svJSON{"canvas": svJSON{"file_id": "F1", "is_empty": true}},
	})})

	id, empty, err := svCanvas(t, f, "xoxb-test", "").ChannelCanvas(context.Background(), "C1")
	if err != nil {
		t.Fatalf("ChannelCanvas: %v", err)
	}
	if id != "F1" || !empty {
		t.Fatalf("id = %q empty = %v, want F1/true", id, empty)
	}
}

func TestChannelCanvas_FallsBackToTheUserIdentityWhenTheBotSeesNoProperties(t *testing.T) {
	f := svServer(t)
	f.on("conversations.info", func(r *http.Request) svJSON {
		// A bot that is not a full member gets the channel without
		// properties at all.
		if r.FormValue("token") == "xoxb-test" {
			return svJSON{"ok": true, "channel": svChan("C1", "alpha", nil)}
		}
		return svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
			"properties": svJSON{"canvas": svJSON{"file_id": "F1"}},
		})}
	})

	id, _, err := svCanvas(t, f, "xoxb-test", "xoxp-test").ChannelCanvas(context.Background(), "C1")
	if err != nil {
		t.Fatalf("ChannelCanvas: %v", err)
	}
	if id != "F1" {
		t.Fatalf("id = %q, want F1 from the user identity", id)
	}
	if n := f.count("conversations.info"); n != 2 {
		t.Fatalf("conversations.info called %d times, want 2 (both identities)", n)
	}
}

func TestChannelCanvas_FallsBackWhenThePrimaryCallFails(t *testing.T) {
	f := svServer(t)
	f.on("conversations.info", func(r *http.Request) svJSON {
		if r.FormValue("token") == "xoxb-test" {
			return svJSON{"ok": false, "error": "channel_not_found"}
		}
		return svJSON{"ok": true, "channel": svChan("C1", "alpha", svJSON{
			"properties": svJSON{"canvas": svJSON{"file_id": "F1"}},
		})}
	})

	id, _, err := svCanvas(t, f, "xoxb-test", "xoxp-test").ChannelCanvas(context.Background(), "C1")
	if err != nil {
		t.Fatalf("ChannelCanvas: %v", err)
	}
	if id != "F1" {
		t.Fatalf("id = %q", id)
	}
}

func TestChannelCanvas_NoCanvasIsNotAnError(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.info", svJSON{"ok": true, "channel": svChan("C1", "alpha", nil)})

	id, empty, err := svCanvas(t, f, "xoxb-test", "").ChannelCanvas(context.Background(), "C1")
	if err != nil {
		t.Fatalf("ChannelCanvas: %v", err)
	}
	if id != "" || empty {
		t.Fatalf("id = %q empty = %v, want the absent-canvas answer", id, empty)
	}
}

func TestChannelCanvas_EveryIdentityFailingIsAnError(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.info", "not_in_channel")

	id, _, err := svCanvas(t, f, "xoxb-test", "xoxp-test").ChannelCanvas(context.Background(), "C1")
	if err == nil {
		t.Fatal("ChannelCanvas returned nil error although both identities were refused")
	}
	if id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
	if !svHasSubstr(err.Error(), "conversations.info") {
		t.Fatalf("err = %q", err)
	}
}

func TestChannelCanvas_SharedPrimaryAndUserClientIsTriedOnlyOnce(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.info", "not_in_channel")

	// Same token for both identities: newCanvasService still holds two
	// distinct clients, but the de-duplication in clients() is keyed on
	// pointer identity, so pass the same one.
	api := svAPI(f, "xoxp-test")
	svc := newCanvasService(api, api, "xoxp-test", svLogger())
	if _, _, err := svc.ChannelCanvas(context.Background(), "C1"); err == nil {
		t.Fatal("ChannelCanvas returned nil error")
	}
	if n := f.count("conversations.info"); n != 1 {
		t.Fatalf("conversations.info called %d times, want 1", n)
	}
}

func TestCanvasFiles_TriesTheUserIdentityFirst(t *testing.T) {
	f := svServer(t)
	var mu sync.Mutex
	var firstToken string
	f.on("files.list", func(r *http.Request) svJSON {
		mu.Lock()
		if firstToken == "" {
			firstToken = r.FormValue("token")
		}
		mu.Unlock()
		return svJSON{"ok": true, "files": []svJSON{{"id": "F1", "filetype": "canvas"}}}
	})

	got, err := svCanvas(t, f, "xoxb-test", "xoxp-test").CanvasFiles(context.Background(), "C1")
	if err != nil {
		t.Fatalf("CanvasFiles: %v", err)
	}
	if len(got) != 1 || got[0].ID != "F1" {
		t.Fatalf("got = %+v", got)
	}
	mu.Lock()
	seen := firstToken
	mu.Unlock()
	if seen != "xoxp-test" {
		t.Fatalf("first identity used %q, want the user token", seen)
	}
	if n := f.count("files.list"); n != 1 {
		t.Fatalf("files.list called %d times, want 1", n)
	}
}

func TestCanvasFiles_SendsTheChannelAndTheCanvasType(t *testing.T) {
	f := svServer(t)
	f.reply("files.list", svJSON{"ok": true, "files": []svJSON{{"id": "F1"}}})

	if _, err := svCanvas(t, f, "xoxb-test", "").CanvasFiles(context.Background(), "C1"); err != nil {
		t.Fatalf("CanvasFiles: %v", err)
	}
	form := f.form(t, "files.list", 0)
	if form.Get("channel") != "C1" {
		t.Fatalf("channel = %q", form.Get("channel"))
	}
	if form.Get("types") != "canvas" {
		t.Fatalf("types = %q, want canvas", form.Get("types"))
	}
	if form.Get("count") != "20" {
		t.Fatalf("count = %q, want 20", form.Get("count"))
	}
}

func TestCanvasFiles_FallsBackToTheSecondIdentityWhenTheFirstSeesNothing(t *testing.T) {
	f := svServer(t)
	f.on("files.list", func(r *http.Request) svJSON {
		if r.FormValue("token") == "xoxp-test" {
			return svJSON{"ok": true, "files": []svJSON{}}
		}
		return svJSON{"ok": true, "files": []svJSON{{"id": "F1"}}}
	})

	got, err := svCanvas(t, f, "xoxb-test", "xoxp-test").CanvasFiles(context.Background(), "C1")
	if err != nil {
		t.Fatalf("CanvasFiles: %v", err)
	}
	if len(got) != 1 || got[0].ID != "F1" {
		t.Fatalf("got = %+v, want the primary's result", got)
	}
	if n := f.count("files.list"); n != 2 {
		t.Fatalf("files.list called %d times, want 2", n)
	}
}

func TestCanvasFiles_NoneFoundIsAnEmptyResultNotAnError(t *testing.T) {
	f := svServer(t)
	f.reply("files.list", svJSON{"ok": true, "files": []svJSON{}})

	got, err := svCanvas(t, f, "xoxb-test", "").CanvasFiles(context.Background(), "C1")
	if err != nil {
		t.Fatalf("CanvasFiles: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got = %+v, want empty", got)
	}
}

func TestCanvasFiles_EveryIdentityFailingSurfacesTheError(t *testing.T) {
	f := svServer(t)
	f.fail("files.list", "missing_scope")

	got, err := svCanvas(t, f, "xoxb-test", "xoxp-test").CanvasFiles(context.Background(), "C1")
	if err == nil {
		t.Fatal("CanvasFiles returned nil error although both identities were refused")
	}
	if got != nil {
		t.Fatalf("got = %+v, want nil", got)
	}
	if n := f.count("files.list"); n != 2 {
		t.Fatalf("files.list called %d times, want 2", n)
	}
}

// A refusal on one identity is not erased by an empty (but successful)
// answer from the other: the caller still learns something went wrong
// rather than being told "no canvases here".
func TestCanvasFiles_PartialRefusalIsNotReportedAsAnEmptyChannel(t *testing.T) {
	f := svServer(t)
	f.on("files.list", func(r *http.Request) svJSON {
		if r.FormValue("token") == "xoxp-test" {
			return svJSON{"ok": false, "error": "missing_scope"}
		}
		return svJSON{"ok": true, "files": []svJSON{}}
	})

	got, err := svCanvas(t, f, "xoxb-test", "xoxp-test").CanvasFiles(context.Background(), "C1")
	if err == nil {
		t.Fatalf("CanvasFiles returned %+v with a nil error after a refusal", got)
	}
}
