package tools

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

// VIPs are the people whose messages come first: the ones Slack's own VIP
// list lets through Do Not Disturb. Slack keeps that list in client
// preferences with no public API, so it is configured here instead, as
// SLACK_VIP_USERS – names or handles, the same list for every workspace,
// matched to a person in each. See ADR 142.

// vipSet holds the VIPs found in one workspace: user ID → display name.
type vipSet struct {
	ids     map[string]string
	missing []string // configured entries nobody in this workspace matches
}

func (v vipSet) has(id string) bool { _, ok := v.ids[id]; return ok && id != "" }

func (v vipSet) empty() bool { return len(v.ids) == 0 }

// names lists the matched VIPs for a note, sorted.
func (v vipSet) names() []string {
	out := make([]string, 0, len(v.ids))
	for _, n := range v.ids {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// vipCacheTTL keeps the resolution for a while: the roster read is the
// expensive part and VIPs rarely change. A failed read is retried soon.
const (
	vipCacheTTL      = 30 * time.Minute
	vipFailRetryTime = time.Minute
)

type vipCacheEntry struct {
	set vipSet
	at  time.Time
	ttl time.Duration
}

var (
	vipCacheMu sync.Mutex
	vipCache   = map[*slack.Client]vipCacheEntry{}
)

// vips resolves the configured VIPs in this Hub's workspace. An empty
// configuration costs nothing; a roster that cannot be read yields an
// empty set, so VIP ordering degrades to the normal order rather than
// failing the tool.
func (h *Hub) vips(ctx context.Context) vipSet {
	entries := h.cfg.VIPUsers
	if len(entries) == 0 || h.client == nil {
		return vipSet{}
	}
	vipCacheMu.Lock()
	if e, ok := vipCache[h.client]; ok && time.Since(e.at) < e.ttl {
		vipCacheMu.Unlock()
		return e.set
	}
	vipCacheMu.Unlock()

	users, err := h.Users().List(ctx)
	set, ttl := vipSet{}, vipFailRetryTime
	if err == nil {
		set, ttl = resolveVIPs(entries, users), vipCacheTTL
	}
	vipCacheMu.Lock()
	vipCache[h.client] = vipCacheEntry{set: set, at: time.Now(), ttl: ttl}
	vipCacheMu.Unlock()
	return set
}

// resolveVIPs matches each configured entry to exactly one person: a user
// ID, a handle, a display or real name, or – as platforms write it – all
// parts of a full name in any order. A name that fits two people is not
// guessed; it is reported as missing so the list gets fixed. Pure.
func resolveVIPs(entries []string, users []goslack.User) vipSet {
	set := vipSet{ids: map[string]string{}}
	for _, raw := range entries {
		entry := strings.TrimPrefix(strings.TrimSpace(raw), "@")
		if entry == "" {
			continue
		}
		var hits []goslack.User
		for _, u := range users {
			if u.ID == entry {
				hits = []goslack.User{u}
				break
			}
		}
		if len(hits) == 0 {
			want := strings.ToLower(entry)
			for _, u := range users {
				if u.IsBot || u.Deleted {
					continue
				}
				if strings.ToLower(u.Name) == want {
					hits = []goslack.User{u}
					break
				}
			}
		}
		if len(hits) == 0 {
			if u, ok := rosterMatch(entry, users); ok {
				hits = []goslack.User{u}
			}
		}
		if len(hits) != 1 {
			set.missing = append(set.missing, raw)
			continue
		}
		u := hits[0]
		set.ids[u.ID] = firstNonEmpty(u.RealName, u.Profile.RealName, u.Profile.DisplayName, u.Name, u.ID)
	}
	return set
}

// vipNote is the one line a tool adds when VIPs are configured: who was
// put first, and which entries matched nobody here. Empty when there is
// nothing to say.
func vipNote(v vipSet) string {
	var parts []string
	if !v.empty() {
		parts = append(parts, "VIP first: "+strings.Join(v.names(), ", "))
	}
	if len(v.missing) > 0 {
		parts = append(parts, "VIP not matched here (no one, or more than one person): "+strings.Join(v.missing, ", "))
	}
	return strings.Join(parts, " · ")
}

// unreadHasVIP reports whether an unread conversation carries a VIP: a DM
// with one, or any new message or thread reply one wrote.
func unreadHasVIP(cu *slack.ChannelUnread, v vipSet) bool {
	if v.empty() || cu == nil {
		return false
	}
	if slack.IsDirectMessage(cu.Channel) && v.has(cu.Channel.User) {
		return true
	}
	for _, m := range cu.Messages {
		if v.has(m.User) {
			return true
		}
	}
	for _, rs := range cu.Replies {
		for _, m := range rs {
			if v.has(m.User) {
				return true
			}
		}
	}
	return false
}

// actionHasVIP reports whether a commitment involves a VIP: one asked it,
// it sits in a DM with one, or it names one.
func actionHasVIP(it *actionItem, v vipSet) bool {
	if v.empty() {
		return false
	}
	if v.has(it.hit.User) {
		return true
	}
	if it.dm && v.has(it.hit.Channel.Name) {
		return true
	}
	for _, m := range userRefRe.FindAllStringSubmatch(it.hit.Text, -1) {
		if v.has(m[1]) {
			return true
		}
	}
	return false
}
