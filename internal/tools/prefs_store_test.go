package tools

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/velesnitski/slk-mcp/internal/config"
)

func resetPrefsCaches() {
	prefsMu.Lock()
	prefsMem = map[string]storedPrefs{}
	prefsFailedAt = map[string]time.Time{}
	prefsMu.Unlock()
}

// Slack's VIP list is read once, kept in an owner-only file, and makes
// the VIP's ask lead get_my_actions without any SLACK_VIP_USERS entry.
func TestSlackPrefs_SyncStoreAndUse(t *testing.T) {
	resetPrefsCaches()
	dir := t.TempDir()
	f := uaNewFakeSlack(t)
	f.On("auth.test", uaAuthOK)
	f.uaUsers(map[string]string{pat: "pat", "U3": "sam"})
	f.On("users.list", `{"ok":true,"members":[{"id":"`+pat+`","name":"pat","real_name":"Pat Novak"},{"id":"U3","name":"sam","real_name":"Sam Lee"}],"response_metadata":{"next_cursor":""}}`)
	f.On("users.prefs.get", `{"ok":true,"prefs":{"vip_users":"`+pat+`"}}`)
	f.On("users.channelSections.list", `{"ok":true,"channel_sections":[{"channel_section_type":"stars","channel_ids_page":{"channel_ids":["C7"]}}]}`)
	askTS := uaTS(-2*3600, 100)
	f.uaSearchByQuery(t, map[string][]map[string]any{
		"to:me": {uaHit("D2", pat, askTS, pat, "можешь проверить домен?", "https://x.slack.com/archives/D2/p1")},
	})
	f.uaHistory(t, map[string][]map[string]any{"D2": {uaMsg(askTS, pat, "можешь проверить домен?", nil)}})
	h := uaHub(t, f, func(c *config.Config) { c.StateDir = dir })

	out := resultText(h.runMyActions(context.Background(), actionDefaults(), ""))
	tmWant(t, out, "⭐ VIP · 💤 no reply 2h · asked of you by pat", "VIP first: Pat Novak")

	path := filepath.Join(dir, "slack-prefs-primary.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("prefs file not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("prefs file must be owner-only, got %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"`+pat+`"`) || !strings.Contains(string(raw), `"C7"`) || strings.Contains(string(raw), "домен") {
		t.Fatalf("file must hold IDs only: %s", raw)
	}

	// A second call reads the copy, not Slack.
	before := f.CountOf("users.prefs.get")
	_ = h.slackPrefs(context.Background(), "primary")
	if f.CountOf("users.prefs.get") != before {
		t.Fatal("a fresh copy must not be re-read from Slack")
	}
}

// A workspace that refuses every source is tried once, then left alone
// for the back-off; tools keep working without priorities.
func TestSlackPrefs_RefusalBacksOff(t *testing.T) {
	resetPrefsCaches()
	f := uaNewFakeSlack(t)
	f.OnError("users.prefs.get", "not_allowed_token_type")
	f.OnError("users.channelSections.list", "not_allowed_token_type")
	f.OnError("stars.list", "missing_scope")
	h := uaHub(t, f, func(c *config.Config) { c.StateDir = t.TempDir() })

	for i := 0; i < 3; i++ {
		if p := h.slackPrefs(context.Background(), "primary"); len(p.VIPs) != 0 {
			t.Fatalf("nothing to read, got %+v", p)
		}
	}
	if n := f.CountOf("users.prefs.get"); n != 1 {
		t.Fatalf("users.prefs.get called %d times, want 1 (back-off)", n)
	}
}

func TestSlackPrefs_OffWithoutStateDir(t *testing.T) {
	resetPrefsCaches()
	f := uaNewFakeSlack(t)
	h := uaHub(t, f)
	if p := h.slackPrefs(context.Background(), "primary"); len(p.VIPs) != 0 || f.Called("users.prefs.get") {
		t.Fatalf("no state dir: no reads, calls=%v", f.Calls())
	}
	tmWant(t, resultText(h.runSyncPrefs(context.Background(), "")), "local state is off")
}

func TestSyncSlackPrefs_ReportsWhatItFound(t *testing.T) {
	resetPrefsCaches()
	f := uaNewFakeSlack(t)
	f.uaUsers(map[string]string{pat: "pat"})
	f.On("users.prefs.get", `{"ok":true,"prefs":{"vip_users":"`+pat+`"}}`)
	f.OnError("users.channelSections.list", "not_allowed_token_type")
	f.On("stars.list", `{"ok":true,"items":[{"type":"channel","channel":"C7"}]}`)
	f.uaInfo(t, map[string]map[string]any{"C7": uaChannel("C7", "alerts", nil)})
	h := uaHub(t, f, func(c *config.Config) { c.StateDir = t.TempDir() })

	out := resultText(h.runSyncPrefs(context.Background(), ""))
	tmWant(t, out, "synced", "1 VIP, 1 starred", "VIP: pat", "starred: #alerts", "not available: users.channelSections.list")
}

// pat is a user ID of real length: Slack's are nine characters or more,
// and the preference parser accepts nothing shorter.
const pat = "U0PATNOVAK"

var _ = http.MethodPost
