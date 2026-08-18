package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const canvasPayload = `{"ok":true,"files":[
  {"id":"F1","title":"Team notes","created":1000,"updated":1500,"channels":["C1"],"permalink":"https://example.slack.com/docs/F1","url_private_download":"https://files.example/F1"},
  {"id":"F2","title":"Old doc","created":900,"updated":900,"channels":["C2"]},
  {"id":"F3","name":"fallback-name","created":1200,"channels":[],"groups":["G9"],"ims":["D9"]}
]}`

func TestParseCanvasFilesResponse_FieldsAndFallbacks(t *testing.T) {
	refs, err := parseCanvasFilesResponse([]byte(canvasPayload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("want 3 refs, got %d", len(refs))
	}
	if refs[0].ID != "F1" || refs[0].Updated != 1500 || refs[0].DownloadURL == "" {
		t.Fatalf("F1 decoded wrong: %+v", refs[0])
	}
	// updated absent -> falls back to created, so the canvas is never
	// silently stamped at the epoch and reported as ancient.
	if refs[2].Updated != 1200 {
		t.Fatalf("want updated to fall back to created 1200, got %d", refs[2].Updated)
	}
	// title absent -> name.
	if refs[2].Title != "fallback-name" {
		t.Fatalf("want title fallback to name, got %q", refs[2].Title)
	}
	// groups and ims fold into Channels so private/DM canvases still label.
	if len(refs[2].Channels) != 2 || refs[2].Channels[0] != "G9" || refs[2].Channels[1] != "D9" {
		t.Fatalf("want groups+ims folded into Channels, got %v", refs[2].Channels)
	}
}

func TestParseCanvasFilesResponse_SlackError(t *testing.T) {
	_, err := parseCanvasFilesResponse([]byte(`{"ok":false,"error":"missing_scope"}`))
	if err == nil {
		t.Fatal("want error for ok:false")
	}
	if got := err.Error(); got != "slack-canvas: missing_scope" {
		t.Fatalf("want the Slack code forwarded verbatim, got %q", got)
	}
}

func TestChangedAfterCreate(t *testing.T) {
	if (CanvasRef{Created: 100, Updated: 100}).ChangedAfterCreate() {
		t.Fatal("created==updated must read as a creation, not an edit")
	}
	if (CanvasRef{Created: 100, Updated: 101}).ChangedAfterCreate() {
		t.Fatal("1s skew must still read as a creation")
	}
	if !(CanvasRef{Created: 100, Updated: 160}).ChangedAfterCreate() {
		t.Fatal("want edit")
	}
}

func TestSelectRecentCanvases_FiltersSortsAndCaps(t *testing.T) {
	refs := []CanvasRef{
		{ID: "A", Updated: 100},
		{ID: "B", Updated: 300},
		{ID: "C", Updated: 200},
	}
	got := selectRecentCanvases(refs, 150, 0)
	if len(got) != 2 || got[0].ID != "B" || got[1].ID != "C" {
		t.Fatalf("want B,C newest-first; got %+v", got)
	}
	// Boundary: `since` itself is excluded, so re-passing a cursor can't
	// re-report the canvas that produced it.
	if len(selectRecentCanvases(refs, 300, 0)) != 0 {
		t.Fatal("want updated == since to be excluded")
	}
	if got := selectRecentCanvases(refs, 0, 1); len(got) != 1 || got[0].ID != "B" {
		t.Fatalf("want limit applied after sort; got %+v", got)
	}
}

func TestRecentCanvases_NoTokenIsSentinel(t *testing.T) {
	s := newCanvasService(nil, nil, "", slog.Default())
	if _, err := s.RecentCanvases(context.Background(), 0, 0); err != ErrCanvasNoToken {
		t.Fatalf("want ErrCanvasNoToken, got %v", err)
	}
}

func TestRecentCanvases_SendsAuthAndCanvasType(t *testing.T) {
	var gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, canvasPayload)
	}))
	defer srv.Close()

	s := newCanvasService(nil, nil, "xoxp-test", slog.Default())
	s.BaseURL = srv.URL

	got, err := s.RecentCanvases(context.Background(), 1000, 0)
	if err != nil {
		t.Fatalf("RecentCanvases: %v", err)
	}
	if gotAuth != "Bearer xoxp-test" {
		t.Fatalf("want bearer auth, got %q", gotAuth)
	}
	if !strings.Contains(gotBody, "types=canvas") {
		t.Fatalf("want types=canvas in form body, got %q", gotBody)
	}
	if len(got) != 2 {
		t.Fatalf("want the 2 canvases newer than 1000, got %d", len(got))
	}
	if got[0].ID != "F1" {
		t.Fatalf("want newest-edit first, got %q", got[0].ID)
	}
}
