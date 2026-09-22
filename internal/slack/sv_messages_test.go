package slack

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	goslack "github.com/slack-go/slack"
)

// svAudio accepts a file the way the voice-note tools do.
func svAudio(f goslack.File) bool { return strings.HasPrefix(f.Mimetype, "audio/") }

// svFileMsg is a message carrying one attachment.
func svFileMsg(ts, user, mimetype string, extra svJSON) svJSON {
	return svMsg(ts, user, "", mergeSV(svJSON{
		"files": []svJSON{{"id": "F1", "mimetype": mimetype, "url_private": "https://example.invalid/f"}},
	}, extra))
}

func mergeSV(base, extra svJSON) svJSON {
	out := svJSON{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func TestHistory_SendsBoundsAndReturnsMessages(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("200.000000", "U1", "later", nil),
		svMsg("100.000000", "U2", "earlier", nil),
	}})

	got, err := svMessages(t, f).History(context.Background(), HistoryParams{
		ChannelID: "C1", OldestTS: 100, LatestTS: 300, Limit: 7,
	})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(got) != 2 || got[0].Text != "later" {
		t.Fatalf("got = %+v, want newest-first pair", got)
	}

	form := f.form(t, "conversations.history", 0)
	if form.Get("channel") != "C1" {
		t.Fatalf("channel = %q", form.Get("channel"))
	}
	if form.Get("oldest") != "100.000000" {
		t.Fatalf("oldest = %q, want 100.000000", form.Get("oldest"))
	}
	if form.Get("latest") != "300.000000" {
		t.Fatalf("latest = %q, want 300.000000", form.Get("latest"))
	}
	if form.Get("inclusive") != "1" {
		t.Fatalf("inclusive = %q, want 1 when a latest bound is set", form.Get("inclusive"))
	}
	if form.Get("limit") != "7" {
		t.Fatalf("limit = %q, want 7", form.Get("limit"))
	}
}

func TestHistory_NoBoundsOmitsOldestAndLatestAndDefaultsTheLimit(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{}})

	if _, err := svMessages(t, f).History(context.Background(), HistoryParams{ChannelID: "C1"}); err != nil {
		t.Fatalf("History: %v", err)
	}
	form := f.form(t, "conversations.history", 0)
	if form.Has("oldest") || form.Has("latest") {
		t.Fatalf("unbounded fetch still sent oldest=%q latest=%q", form.Get("oldest"), form.Get("latest"))
	}
	if form.Get("inclusive") != "0" {
		t.Fatalf("inclusive = %q, want 0", form.Get("inclusive"))
	}
	if form.Get("limit") != "200" {
		t.Fatalf("limit = %q, want the 200 default", form.Get("limit"))
	}
}

func TestHistory_SlackErrorIsWrappedNotSwallowed(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.history", "not_in_channel")

	got, err := svMessages(t, f).History(context.Background(), HistoryParams{ChannelID: "C1"})
	if err == nil {
		t.Fatal("History returned nil error")
	}
	if got != nil {
		t.Fatalf("got = %v, want nil", got)
	}
	if !svHasSubstr(err.Error(), "conversations.history") || !svHasSubstr(err.Error(), "not_in_channel") {
		t.Fatalf("err = %q", err)
	}
}

func TestThreadReplies_SendsTheRootTSAndReturnsTheThread(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svMsg("100.000000", "U1", "root", svJSON{"thread_ts": "100.000000"}),
		svMsg("101.000000", "U2", "reply", svJSON{"thread_ts": "100.000000"}),
	}})

	got, err := svMessages(t, f).ThreadReplies(context.Background(), "C1", "100.000000")
	if err != nil {
		t.Fatalf("ThreadReplies: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2", len(got))
	}
	form := f.form(t, "conversations.replies", 0)
	if form.Get("ts") != "100.000000" {
		t.Fatalf("ts = %q", form.Get("ts"))
	}
	if form.Get("limit") != "200" {
		t.Fatalf("limit = %q, want 200", form.Get("limit"))
	}
}

func TestThreadReplies_SlackErrorIsWrapped(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.replies", "thread_not_found")

	if _, err := svMessages(t, f).ThreadReplies(context.Background(), "C1", "1.0"); err == nil {
		t.Fatal("ThreadReplies returned nil error")
	}
}

func TestPost_ReturnsTheTimestampAndSendsTheText(t *testing.T) {
	f := svServer(t)
	f.reply("chat.postMessage", svJSON{"ok": true, "channel": "C1", "ts": "500.000100"})

	ts, err := svMessages(t, f).Post(context.Background(), "C1", "hello alpha", "")
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if ts != "500.000100" {
		t.Fatalf("ts = %q", ts)
	}
	form := f.form(t, "chat.postMessage", 0)
	if form.Get("channel") != "C1" || form.Get("text") != "hello alpha" {
		t.Fatalf("form = %v", form)
	}
	if form.Has("thread_ts") {
		t.Fatalf("thread_ts = %q, want absent for a top-level post", form.Get("thread_ts"))
	}
}

func TestPost_ThreadTSTurnsThePostIntoAReply(t *testing.T) {
	f := svServer(t)
	f.reply("chat.postMessage", svJSON{"ok": true, "channel": "C1", "ts": "500.000200"})

	if _, err := svMessages(t, f).Post(context.Background(), "C1", "in thread", "400.000000"); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got := f.form(t, "chat.postMessage", 0).Get("thread_ts"); got != "400.000000" {
		t.Fatalf("thread_ts = %q", got)
	}
}

func TestPost_SlackErrorIsWrappedAndNoTimestampIsInvented(t *testing.T) {
	f := svServer(t)
	f.fail("chat.postMessage", "channel_not_found")

	ts, err := svMessages(t, f).Post(context.Background(), "C1", "x", "")
	if err == nil {
		t.Fatal("Post returned nil error")
	}
	if ts != "" {
		t.Fatalf("ts = %q, want empty", ts)
	}
	if !svHasSubstr(err.Error(), "chat.postMessage") {
		t.Fatalf("err = %q", err)
	}
}

func TestDelete_CallsChatDeleteWithTheTimestamp(t *testing.T) {
	f := svServer(t)
	f.reply("chat.delete", svJSON{"ok": true, "channel": "C1", "ts": "500.000100"})

	if err := svMessages(t, f).Delete(context.Background(), "C1", "500.000100"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	form := f.form(t, "chat.delete", 0)
	if form.Get("channel") != "C1" || form.Get("ts") != "500.000100" {
		t.Fatalf("form = %v", form)
	}
}

func TestDelete_SlackRefusalIsSurfaced(t *testing.T) {
	f := svServer(t)
	f.fail("chat.delete", "cant_delete_message")

	err := svMessages(t, f).Delete(context.Background(), "C1", "1.0")
	if err == nil {
		t.Fatal("Delete returned nil error")
	}
	if !svHasSubstr(err.Error(), "cant_delete_message") {
		t.Fatalf("err = %q, want the server's refusal verbatim", err)
	}
}

func TestMessageAt_TopLevelHitNeedsNoThreadLookup(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("300.000100", "U1", "found me", nil),
	}})

	got, err := svMessages(t, f).MessageAt(context.Background(), "C1", "300.000100")
	if err != nil {
		t.Fatalf("MessageAt: %v", err)
	}
	if got.Text != "found me" {
		t.Fatalf("text = %q", got.Text)
	}
	form := f.form(t, "conversations.history", 0)
	if form.Get("latest") != "300.000100" || form.Get("inclusive") != "1" || form.Get("limit") != "1" {
		t.Fatalf("point lookup form = %v", form)
	}
	if n := f.count("conversations.replies"); n != 0 {
		t.Fatalf("conversations.replies called %d times, want 0", n)
	}
}

func TestMessageAt_ThreadReplyIsFoundThroughTheFallback(t *testing.T) {
	f := svServer(t)
	// A reply never appears in channel history; Slack answers the point
	// lookup with the nearest top-level message instead.
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("299.000000", "U1", "the parent", nil),
	}})
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svMsg("299.000000", "U1", "the parent", svJSON{"thread_ts": "299.000000"}),
		svMsg("300.000100", "U2", "the reply", svJSON{"thread_ts": "299.000000"}),
	}})

	got, err := svMessages(t, f).MessageAt(context.Background(), "C1", "300.000100")
	if err != nil {
		t.Fatalf("MessageAt: %v", err)
	}
	if got.Text != "the reply" {
		t.Fatalf("text = %q, want the reply", got.Text)
	}
}

func TestMessageAt_MissingEverywhereIsAnError(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{}})
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svMsg("299.000000", "U1", "unrelated", nil),
	}})

	got, err := svMessages(t, f).MessageAt(context.Background(), "C1", "300.000100")
	if err == nil {
		t.Fatalf("MessageAt returned %+v with a nil error", got)
	}
	if got != nil {
		t.Fatalf("got = %+v, want nil", got)
	}
	if !svHasSubstr(err.Error(), "no message found at ts") {
		t.Fatalf("err = %q", err)
	}
}

func TestMessageAt_ThreadLookupFailureIsReportedAlongsideTheMiss(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{}})
	f.fail("conversations.replies", "thread_not_found")

	_, err := svMessages(t, f).MessageAt(context.Background(), "C1", "300.000100")
	if err == nil {
		t.Fatal("MessageAt returned nil error")
	}
	if !svHasSubstr(err.Error(), "thread lookup failed") || !svHasSubstr(err.Error(), "thread_not_found") {
		t.Fatalf("err = %q", err)
	}
}

func TestMessageAt_HistoryFailureIsNotMaskedByTheThreadFallback(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.history", "invalid_auth")

	_, err := svMessages(t, f).MessageAt(context.Background(), "C1", "1.0")
	if err == nil {
		t.Fatal("MessageAt returned nil error")
	}
	if !svHasSubstr(err.Error(), "invalid_auth") {
		t.Fatalf("err = %q", err)
	}
	if n := f.count("conversations.replies"); n != 0 {
		t.Fatalf("conversations.replies called %d times after a history failure, want 0", n)
	}
}

func TestLatestFileMessage_TopLevelMatchWinsAndNoThreadIsOpened(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svFileMsg("300.000000", "U1", "audio/mp4", nil),
		svFileMsg("200.000000", "U1", "audio/mp4", nil),
	}})

	got, err := svMessages(t, f).LatestFileMessage(context.Background(), "C1", svAudio, "")
	if err != nil {
		t.Fatalf("LatestFileMessage: %v", err)
	}
	if got.Timestamp != "300.000000" {
		t.Fatalf("ts = %q, want the newest", got.Timestamp)
	}
	if n := f.count("conversations.replies"); n != 0 {
		t.Fatalf("threads were scanned (%d calls) despite a top-level hit", n)
	}
}

func TestLatestFileMessage_ScansThreadsAndKeepsTheNewestReply(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("300.000000", "U1", "younger thread", svJSON{"thread_ts": "300.000000", "reply_count": 1}),
		svMsg("200.000000", "U1", "older thread", svJSON{"thread_ts": "200.000000", "reply_count": 1}),
	}})
	f.on("conversations.replies", func(r *http.Request) svJSON {
		switch r.FormValue("ts") {
		case "300.000000":
			// The younger thread's reply is the OLDER attachment.
			return svJSON{"ok": true, "messages": []svJSON{
				svFileMsg("301.000000", "U2", "audio/mp4", svJSON{"thread_ts": "300.000000", "text": "younger thread reply"}),
			}}
		case "200.000000":
			return svJSON{"ok": true, "messages": []svJSON{
				svFileMsg("400.000000", "U2", "audio/mp4", svJSON{"thread_ts": "200.000000", "text": "older thread reply"}),
			}}
		}
		return svJSON{"ok": false, "error": "thread_not_found"}
	})

	got, err := svMessages(t, f).LatestFileMessage(context.Background(), "C1", svAudio, "")
	if err != nil {
		t.Fatalf("LatestFileMessage: %v", err)
	}
	if got.Timestamp != "400.000000" {
		t.Fatalf("ts = %q, want 400.000000 — the newest reply, not the first thread scanned", got.Timestamp)
	}
}

func TestLatestFileMessage_UnreadableThreadIsSkippedNotFatal(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("300.000000", "U1", "broken", svJSON{"thread_ts": "300.000000", "reply_count": 1}),
		svMsg("200.000000", "U1", "fine", svJSON{"thread_ts": "200.000000", "reply_count": 1}),
	}})
	f.on("conversations.replies", func(r *http.Request) svJSON {
		if r.FormValue("ts") == "200.000000" {
			return svJSON{"ok": true, "messages": []svJSON{
				svFileMsg("210.000000", "U2", "audio/mp4", svJSON{"thread_ts": "200.000000"}),
			}}
		}
		return svJSON{"ok": false, "error": "fetch_failed"}
	})

	got, err := svMessages(t, f).LatestFileMessage(context.Background(), "C1", svAudio, "")
	if err != nil {
		t.Fatalf("LatestFileMessage: %v", err)
	}
	if got.Timestamp != "210.000000" {
		t.Fatalf("ts = %q", got.Timestamp)
	}
}

func TestLatestFileMessage_AuthorFilterSkipsTheOtherParty(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svFileMsg("300.000000", "U2", "audio/mp4", nil),
		svFileMsg("200.000000", "U1", "audio/mp4", nil),
	}})

	got, err := svMessages(t, f).LatestFileMessage(context.Background(), "C1", svAudio, "U1")
	if err != nil {
		t.Fatalf("LatestFileMessage: %v", err)
	}
	if got.Timestamp != "200.000000" {
		t.Fatalf("ts = %q, want U1's own message", got.Timestamp)
	}
}

func TestLatestFileMessage_NothingQualifyingIsAnError(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("300.000000", "U1", "just text", nil),
	}})

	got, err := svMessages(t, f).LatestFileMessage(context.Background(), "C1", svAudio, "")
	if err == nil {
		t.Fatalf("LatestFileMessage returned %+v with a nil error", got)
	}
	if got != nil {
		t.Fatalf("got = %+v, want nil", got)
	}
}

func TestLatestFileMessage_HistoryFailurePropagates(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.history", "not_in_channel")

	if _, err := svMessages(t, f).LatestFileMessage(context.Background(), "C1", svAudio, ""); err == nil {
		t.Fatal("LatestFileMessage returned nil error")
	}
}

func TestRecentFileMessages_MergesTopLevelAndThreadsNewestFirstWithoutDuplicates(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svFileMsg("300.000000", "U1", "audio/mp4", svJSON{"thread_ts": "300.000000", "reply_count": 1}),
		svFileMsg("100.000000", "U1", "audio/mp4", nil),
	}})
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		// conversations.replies repeats the parent; it must not be
		// counted twice.
		svFileMsg("300.000000", "U1", "audio/mp4", svJSON{"thread_ts": "300.000000"}),
		svFileMsg("350.000000", "U2", "audio/mp4", svJSON{"thread_ts": "300.000000"}),
	}})

	got, err := svMessages(t, f).RecentFileMessages(context.Background(), "C1", svAudio, "", 0)
	if err != nil {
		t.Fatalf("RecentFileMessages: %v", err)
	}
	var order []string
	for _, m := range got {
		order = append(order, m.Timestamp)
	}
	if strings.Join(order, ",") != "350.000000,300.000000,100.000000" {
		t.Fatalf("order = %v, want newest-first and de-duplicated", order)
	}
}

func TestRecentFileMessages_HonoursTheLimit(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svFileMsg("300.000000", "U1", "audio/mp4", nil),
		svFileMsg("200.000000", "U1", "audio/mp4", nil),
		svFileMsg("100.000000", "U1", "audio/mp4", nil),
	}})

	got, err := svMessages(t, f).RecentFileMessages(context.Background(), "C1", svAudio, "", 2)
	if err != nil {
		t.Fatalf("RecentFileMessages: %v", err)
	}
	if len(got) != 2 || got[0].Timestamp != "300.000000" {
		t.Fatalf("got = %d items starting at %q", len(got), got[0].Timestamp)
	}
}

func TestRecentFileMessages_UnreadableThreadIsSkipped(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svFileMsg("300.000000", "U1", "audio/mp4", svJSON{"thread_ts": "300.000000", "reply_count": 1}),
	}})
	f.fail("conversations.replies", "fetch_failed")

	got, err := svMessages(t, f).RecentFileMessages(context.Background(), "C1", svAudio, "", 0)
	if err != nil {
		t.Fatalf("RecentFileMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want the top-level hit", len(got))
	}
}

func TestRecentFileMessages_NothingQualifyingIsAnError(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.history", svJSON{"ok": true, "messages": []svJSON{
		svMsg("300.000000", "U1", "text only", nil),
	}})

	if _, err := svMessages(t, f).RecentFileMessages(context.Background(), "C1", svAudio, "", 0); err == nil {
		t.Fatal("RecentFileMessages returned nil error for an empty result")
	}
}

func TestRecentFileMessages_HistoryFailurePropagates(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.history", "invalid_auth")

	if _, err := svMessages(t, f).RecentFileMessages(context.Background(), "C1", svAudio, "", 0); err == nil {
		t.Fatal("RecentFileMessages returned nil error")
	}
}

func TestLatestFileInThread_NewestQualifyingReplyWins(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svFileMsg("100.000000", "U1", "audio/mp4", svJSON{"thread_ts": "100.000000", "text": "first"}),
		svMsg("110.000000", "U2", "no file", svJSON{"thread_ts": "100.000000"}),
		svFileMsg("120.000000", "U2", "audio/mp4", svJSON{"thread_ts": "100.000000", "text": "last"}),
	}})

	got, err := svMessages(t, f).LatestFileInThread(context.Background(), "C1", "100.000000", svAudio)
	if err != nil {
		t.Fatalf("LatestFileInThread: %v", err)
	}
	if got.Timestamp != "120.000000" {
		t.Fatalf("ts = %q, want the last qualifying reply", got.Timestamp)
	}
}

func TestLatestFileInThread_NoMatchIsAnError(t *testing.T) {
	f := svServer(t)
	f.reply("conversations.replies", svJSON{"ok": true, "messages": []svJSON{
		svMsg("100.000000", "U1", "text", svJSON{"thread_ts": "100.000000"}),
	}})

	if _, err := svMessages(t, f).LatestFileInThread(context.Background(), "C1", "100.000000", svAudio); err == nil {
		t.Fatal("LatestFileInThread returned nil error")
	}
}

func TestLatestFileInThread_RepliesFailurePropagates(t *testing.T) {
	f := svServer(t)
	f.fail("conversations.replies", "thread_not_found")

	if _, err := svMessages(t, f).LatestFileInThread(context.Background(), "C1", "1.0", svAudio); err == nil {
		t.Fatal("LatestFileInThread returned nil error")
	}
}

func TestFileInfo_ReturnsTheFileWithItsDownloadURL(t *testing.T) {
	f := svServer(t)
	f.reply("files.info", svJSON{"ok": true, "file": svJSON{
		"id": "F1", "mimetype": "audio/mp4", "url_private_download": "https://example.invalid/d",
	}})

	got, err := svMessages(t, f).FileInfo(context.Background(), "F1")
	if err != nil {
		t.Fatalf("FileInfo: %v", err)
	}
	if got.ID != "F1" || got.URLPrivateDownload != "https://example.invalid/d" {
		t.Fatalf("got = %+v", got)
	}
	if v := f.form(t, "files.info", 0).Get("file"); v != "F1" {
		t.Fatalf("file = %q", v)
	}
}

func TestFileInfo_SlackErrorIsWrapped(t *testing.T) {
	f := svServer(t)
	f.fail("files.info", "file_not_found")

	got, err := svMessages(t, f).FileInfo(context.Background(), "F1")
	if err == nil {
		t.Fatal("FileInfo returned nil error")
	}
	if got.ID != "" {
		t.Fatalf("got = %+v, want a zero file", got)
	}
}

// files.info answering ok:true with no `file` object is exactly the
// "looks successful, carries nothing" shape. FileInfo's nil guard
// cannot catch it — slack-go hands back a pointer into its own
// response struct, never nil — so the caller receives a zero-value
// File with a nil error. Pinned here as the current behaviour; see the
// report accompanying this test file.
func TestFileInfo_EmptyBodyYieldsAZeroFileAndNoError(t *testing.T) {
	f := svServer(t)
	f.reply("files.info", svJSON{"ok": true})

	got, err := svMessages(t, f).FileInfo(context.Background(), "F1")
	if err != nil {
		t.Fatalf("FileInfo: %v", err)
	}
	if got.ID != "" || got.URLPrivate != "" || got.Mimetype != "" {
		t.Fatalf("got = %+v, want the zero File", got)
	}
}

func TestDownloadFile_StreamsTheBytesWithThePrimaryToken(t *testing.T) {
	f := svServer(t)
	var mu sync.Mutex
	var seenAuth string
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = r.Header.Get("Authorization")
		mu.Unlock()
		_, _ = w.Write([]byte("alpha-bytes"))
	}))
	t.Cleanup(files.Close)

	var buf bytes.Buffer
	if err := svMessages(t, f).DownloadFile(context.Background(), files.URL+"/f", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if buf.String() != "alpha-bytes" {
		t.Fatalf("body = %q", buf.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if seenAuth != "Bearer xoxp-test" {
		t.Fatalf("auth = %q", seenAuth)
	}
}

func TestDownloadFile_AuthRefusalRetriesWithTheUserToken(t *testing.T) {
	f := svServer(t)
	var mu sync.Mutex
	var attempts []string
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		mu.Lock()
		attempts = append(attempts, auth)
		mu.Unlock()
		if auth != "Bearer xoxp-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("private-bytes"))
	}))
	t.Cleanup(files.Close)

	svc := svMessagesWithFallback(t, f, "xoxb-test", "xoxp-test")
	var buf bytes.Buffer
	if err := svc.DownloadFile(context.Background(), files.URL+"/f", &buf); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	if buf.String() != "private-bytes" {
		t.Fatalf("body = %q", buf.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 || attempts[0] != "Bearer xoxb-test" || attempts[1] != "Bearer xoxp-test" {
		t.Fatalf("attempts = %v, want bot then user", attempts)
	}
}

func TestDownloadFile_NonAuthFailureIsNotRetried(t *testing.T) {
	f := svServer(t)
	var mu sync.Mutex
	var attempts int
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(files.Close)

	svc := svMessagesWithFallback(t, f, "xoxb-test", "xoxp-test")
	var buf bytes.Buffer
	err := svc.DownloadFile(context.Background(), files.URL+"/f", &buf)
	if err == nil {
		t.Fatal("DownloadFile returned nil error on 404")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 — a missing file must not burn the user token", attempts)
	}
}

func TestDownloadFile_BothIdentitiesRefusedReportsBoth(t *testing.T) {
	f := svServer(t)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(files.Close)

	svc := svMessagesWithFallback(t, f, "xoxb-test", "xoxp-test")
	var buf bytes.Buffer
	err := svc.DownloadFile(context.Background(), files.URL+"/f", &buf)
	if err == nil {
		t.Fatal("DownloadFile returned nil error")
	}
	if !svHasSubstr(err.Error(), "user token also refused") {
		t.Fatalf("err = %q", err)
	}
}

func TestDownloadFile_SingleIdentityRefusalIsReportedOnce(t *testing.T) {
	f := svServer(t)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(files.Close)

	var buf bytes.Buffer
	err := svMessages(t, f).DownloadFile(context.Background(), files.URL+"/f", &buf)
	if err == nil {
		t.Fatal("DownloadFile returned nil error")
	}
	if svHasSubstr(err.Error(), "user token also refused") {
		t.Fatalf("err = %q, want no fallback attempt without a second identity", err)
	}
}

// svBadTruncate is a sink whose reset fails, standing in for a file the
// process can no longer rewind.
type svBadTruncate struct{ io.Writer }

func (svBadTruncate) Truncate(int64) error { return errors.New("sink is read-only") }

// svBadSeek truncates fine but cannot rewind.
type svBadSeek struct{ io.Writer }

func (svBadSeek) Truncate(int64) error { return nil }

func (svBadSeek) Seek(int64, int) (int64, error) { return 0, errors.New("sink is not seekable") }

// A truncatable sink (a real file) is reset before the user-token retry
// so the refused attempt's partial body cannot be prepended to the
// bytes the caller finally receives.
func TestDownloadFile_TruncatableSinkIsResetBeforeTheRetry(t *testing.T) {
	f := svServer(t)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xoxp-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("real-bytes"))
	}))
	t.Cleanup(files.Close)

	path := filepath.Join(t.TempDir(), "download.bin")
	sink, err := os.Create(path)
	if err != nil {
		t.Fatalf("create sink: %v", err)
	}
	defer sink.Close()
	if _, err := sink.WriteString("stale-partial-body"); err != nil {
		t.Fatalf("seed sink: %v", err)
	}

	svc := svMessagesWithFallback(t, f, "xoxb-test", "xoxp-test")
	if err := svc.DownloadFile(context.Background(), files.URL+"/f", sink); err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "real-bytes" {
		t.Fatalf("file = %q, want only the retried body", got)
	}
}

func TestDownloadFile_UnresettableSinkIsReportedInsteadOfAppendedTo(t *testing.T) {
	f := svServer(t)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(files.Close)

	svc := svMessagesWithFallback(t, f, "xoxb-test", "xoxp-test")
	err := svc.DownloadFile(context.Background(), files.URL+"/f", svBadTruncate{io.Discard})
	if err == nil {
		t.Fatal("DownloadFile returned nil error")
	}
	if !svHasSubstr(err.Error(), "could not be reset") {
		t.Fatalf("err = %q, want the sink-reset failure surfaced", err)
	}
}

func TestDownloadFile_UnrewindableSinkIsReportedInsteadOfAppendedTo(t *testing.T) {
	f := svServer(t)
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(files.Close)

	svc := svMessagesWithFallback(t, f, "xoxb-test", "xoxp-test")
	err := svc.DownloadFile(context.Background(), files.URL+"/f", svBadSeek{io.Discard})
	if err == nil {
		t.Fatal("DownloadFile returned nil error")
	}
	if !svHasSubstr(err.Error(), "could not be rewound") {
		t.Fatalf("err = %q, want the rewind failure surfaced", err)
	}
}

func TestAddReaction_SendsTheEmojiAndTheMessageRef(t *testing.T) {
	f := svServer(t)
	f.reply("reactions.add", svJSON{"ok": true})

	if err := svMessages(t, f).AddReaction(context.Background(), "C1", "500.000100", "eyes"); err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	form := f.form(t, "reactions.add", 0)
	if form.Get("name") != "eyes" || form.Get("channel") != "C1" || form.Get("timestamp") != "500.000100" {
		t.Fatalf("form = %v", form)
	}
}

func TestAddReaction_SlackErrorPropagates(t *testing.T) {
	f := svServer(t)
	f.fail("reactions.add", "already_reacted")

	if err := svMessages(t, f).AddReaction(context.Background(), "C1", "1.0", "eyes"); err == nil {
		t.Fatal("AddReaction returned nil error")
	}
}
