package slack

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestVIPsFromPrefs(t *testing.T) {
	ids, keys := vipsFromPrefs([]byte(`{"ok":true,"prefs":{
		"vip_users":"U0AAAAAAA1,U0BBBBBBB2",
		"vip_list_json":"[\"U0CCCCCCC3\",\"U0AAAAAAA1\"]",
		"vipUsersEnabled":true,
		"muted_channels":"C0DDDDDDD4",
		"emoji_use":"{\"U0EEEEEEE5\":1}"
	}}`))
	if !reflect.DeepEqual(ids, []string{"U0AAAAAAA1", "U0BBBBBBB2", "U0CCCCCCC3"}) {
		t.Fatalf("ids=%v", ids)
	}
	// A vip-named key with no IDs (a toggle) is not a source; unrelated
	// keys holding user IDs are never read.
	if !reflect.DeepEqual(keys, []string{"vip_list_json", "vip_users"}) {
		t.Fatalf("keys=%v", keys)
	}
	if ids, _ := vipsFromPrefs([]byte(`not json`)); ids != nil {
		t.Fatalf("garbage → nothing, got %v", ids)
	}
}

func TestStarredFromSectionsBody(t *testing.T) {
	got, err := starredFromSectionsBody([]byte(`{"ok":true,"channel_sections":[
		{"channel_section_type":"standard","channel_ids_page":{"channel_ids":["C1"]}},
		{"channel_section_type":"stars","channel_ids_page":{"channel_ids":["C2","D3"]}}]}`))
	if err != nil || !reflect.DeepEqual(got, []string{"C2", "D3"}) {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func prefsServer(t *testing.T, routes map[string]string) *CanvasService {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			body = `{"ok":false,"error":"unknown_method"}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	s := &CanvasService{token: "t", http: srv.Client(), log: slog.New(slog.NewTextHandler(io.Discard, nil)), BaseURL: srv.URL + "/"}
	return s
}

// Sections refused → the legacy star list answers; the VIP list is read
// independently of both.
func TestFetchPrefs_FallsBackPerSource(t *testing.T) {
	s := prefsServer(t, map[string]string{
		"users.prefs.get":            `{"ok":true,"prefs":{"vip_users":"U0AAAAAAA1"}}`,
		"users.channelSections.list": `{"ok":false,"error":"not_allowed_token_type"}`,
		"stars.list":                 `{"ok":true,"items":[{"type":"channel","channel":"C9"},{"type":"message","channel":"C8"},{"type":"im","channel":"D7"}]}`,
	})
	p, err := s.FetchPrefs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.VIPs, []string{"U0AAAAAAA1"}) || !reflect.DeepEqual(p.Starred, []string{"C9", "D7"}) {
		t.Fatalf("prefs=%+v", p)
	}
	if !reflect.DeepEqual(p.Sources, []string{"users.prefs.get", "stars.list"}) || len(p.Failures) != 1 || !strings.Contains(p.Failures[0], "not_allowed_token_type") {
		t.Fatalf("sources=%v failures=%v", p.Sources, p.Failures)
	}
}

func TestFetchPrefs_AllRefusedIsAnError(t *testing.T) {
	s := prefsServer(t, map[string]string{})
	if _, err := s.FetchPrefs(context.Background()); err == nil || !strings.Contains(err.Error(), "no Slack preference source answered") {
		t.Fatalf("err=%v", err)
	}
}
