package slack

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

// newTestListService returns a ListService that talks to `srv` and
// keeps the production token / endpoint path. The httptest.Server is
// expected to route on path "/slackLists.items.list" (or whatever the
// caller overrides via Endpoint) — we do not assume any other slack
// endpoint exists.
func newTestListService(t *testing.T, srv *httptest.Server) *ListService {
	t.Helper()
	s := newListService("xoxp-test", slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.BaseURL = srv.URL + "/"
	return s
}

func TestListService_HasToken(t *testing.T) {
	with := newListService("xoxp-x", nil)
	if !with.HasToken() {
		t.Fatal("HasToken=false for non-empty token")
	}
	without := newListService("", nil)
	if without.HasToken() {
		t.Fatal("HasToken=true for empty token")
	}
}

func TestListService_Items_RequiresToken(t *testing.T) {
	s := newListService("", nil)
	_, err := s.Items(context.Background(), "F123", "", 0)
	if !errors.Is(err, ErrListsNoToken) {
		t.Fatalf("expected ErrListsNoToken; got %v", err)
	}
}

func TestListService_Items_RequiresListID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request reached server with empty list_id")
	}))
	defer srv.Close()
	s := newTestListService(t, srv)
	_, err := s.Items(context.Background(), "  ", "", 0)
	if err == nil || !strings.Contains(err.Error(), "list_id is required") {
		t.Fatalf("expected list_id-required error; got %v", err)
	}
}

func TestListService_Items_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/slackLists.items.list" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer xoxp-test" {
			http.Error(w, "wrong auth", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"ok": true,
			"items": [
				{
					"id": "Ri111",
					"row_id": "row-1",
					"fields": [
						{"column_id":"ColA","key":"title","value":"First task"},
						{"column_id":"ColB","key":"due","value":"2026-06-01"},
						{"column_id":"ColC","key":"done","value":false}
					]
				},
				{
					"id": "Ri112",
					"row_id": "row-2",
					"fields": [
						{"column_id":"ColA","key":"title","value":"Second task"}
					]
				}
			],
			"response_metadata": {"next_cursor": "cur-abc"}
		}`))
	}))
	defer srv.Close()

	s := newTestListService(t, srv)
	got, err := s.Items(context.Background(), "F0AHG5CBXC5", "", 0)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("expected 2 items; got %d", len(got.Items))
	}
	if got.NextCursor != "cur-abc" {
		t.Fatalf("NextCursor=%q; want cur-abc", got.NextCursor)
	}
	if got.Items[0].Title != "First task" {
		t.Fatalf("first item Title=%q; want 'First task'", got.Items[0].Title)
	}
	if got.Items[0].RowID != "row-1" {
		t.Fatalf("first item RowID=%q; want row-1", got.Items[0].RowID)
	}
	// Boolean false should render as "false" — guards a regression where
	// a falsy value silently drops out of the rendered output.
	var foundDone bool
	for _, f := range got.Items[0].Fields {
		if f.Key == "done" && f.Display == "false" {
			foundDone = true
		}
	}
	if !foundDone {
		t.Fatalf("done=false cell did not render; fields=%+v", got.Items[0].Fields)
	}
}

func TestListService_Items_PassesCursorAndLimit(t *testing.T) {
	var sawCursor, sawLimit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"cursor":"cur-1"`) {
			sawCursor = true
		}
		if strings.Contains(string(body), `"limit":50`) {
			sawLimit = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"items":[]}`))
	}))
	defer srv.Close()

	s := newTestListService(t, srv)
	if _, err := s.Items(context.Background(), "F0AHG5CBXC5", "cur-1", 50); err != nil {
		t.Fatalf("Items: %v", err)
	}
	if !sawCursor || !sawLimit {
		t.Fatalf("cursor/limit not forwarded: cursor=%v limit=%v", sawCursor, sawLimit)
	}
}

func TestListService_Items_MissingScopeSurfacesVerbatim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer srv.Close()

	s := newTestListService(t, srv)
	_, err := s.Items(context.Background(), "F0AHG5CBXC5", "", 0)
	if err == nil || !strings.Contains(err.Error(), "missing_scope") {
		t.Fatalf("expected missing_scope error; got %v", err)
	}
}

func TestListService_Items_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error":"rate_limited"}`))
	}))
	defer srv.Close()

	s := newTestListService(t, srv)
	_, err := s.Items(context.Background(), "F0AHG5CBXC5", "", 0)
	if err == nil {
		t.Fatal("expected error on 429")
	}
	var rle *goslack.RateLimitedError
	if !errors.As(err, &rle) {
		t.Fatalf("expected *goslack.RateLimitedError; got %T (%v)", err, err)
	}
	if rle.RetryAfter.Seconds() != 3 {
		t.Fatalf("RetryAfter=%v; want 3s", rle.RetryAfter)
	}
}

func TestListService_Items_MalformedBodySurfacesContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not actually json {`))
	}))
	defer srv.Close()

	s := newTestListService(t, srv)
	_, err := s.Items(context.Background(), "F0AHG5CBXC5", "", 0)
	if err == nil || !strings.Contains(err.Error(), "parse response") {
		t.Fatalf("expected parse error; got %v", err)
	}
	// Body fragment must be in the error so the operator can debug
	// without reproducing the failure.
	if !strings.Contains(err.Error(), "not actually json") {
		t.Fatalf("error should include body fragment; got %v", err)
	}
}

func TestParseRetryAfterSeconds(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"5", 5},
		{"", 1},      // default
		{"abc", 1},   // unparseable → default
		{"-2", 1},    // non-positive → default
		{"  9  ", 9}, // trimmed
	}
	for _, c := range cases {
		got := parseRetryAfterSeconds(c.in).Seconds()
		if int(got) != c.want {
			t.Fatalf("parseRetryAfterSeconds(%q)=%v; want %d", c.in, got, c.want)
		}
	}
}

func TestDisplayValue_TypeCoverage(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"string", "hello", "hello"},
		{"int via float64 (JSON)", float64(42), "42"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"array of strings", []any{"a", "b", "c"}, "a, b, c"},
		{"map → json", map[string]any{"k": "v"}, `{"k":"v"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := displayValue(c.in); got != c.want {
				t.Fatalf("displayValue(%v)=%q; want %q", c.in, got, c.want)
			}
		})
	}
}

func TestBestEffortTitle_Precedence(t *testing.T) {
	// Explicit title key wins over other non-empty cells.
	fields := []ListField{
		{Key: "other", Display: "X"},
		{Key: "title", Display: "Y"},
	}
	if got := bestEffortTitle(fields, "r1", "i1"); got != "Y" {
		t.Fatalf("title-key precedence broken; got %q", got)
	}
	// No explicit title → first non-empty Display.
	fields2 := []ListField{
		{Key: "a", Display: ""},
		{Key: "b", Display: "non-empty"},
	}
	if got := bestEffortTitle(fields2, "r1", "i1"); got != "non-empty" {
		t.Fatalf("first non-empty precedence broken; got %q", got)
	}
	// Nothing usable → row id, then id.
	if got := bestEffortTitle(nil, "row-fb", "id-fb"); got != "row-fb" {
		t.Fatalf("row-id fallback broken; got %q", got)
	}
	if got := bestEffortTitle(nil, "", "id-fb"); got != "id-fb" {
		t.Fatalf("id fallback broken; got %q", got)
	}
}
