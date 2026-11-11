package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// SlackPrefs is what Slack itself says matters to the operator: the VIP
// list (people whose messages ring through Do Not Disturb) and the starred
// conversations. Neither has a documented Web API: the VIP list lives in
// the client preferences (users.prefs.get) and starred conversations in
// the sidebar sections (users.channelSections.list), with the legacy
// stars.list as a fallback. Every source is optional and reported, so a
// workspace that refuses one still yields the others. See ADR 143.
type SlackPrefs struct {
	VIPs     []string // user IDs
	VIPKeys  []string // preference keys the VIP IDs came from
	Starred  []string // conversation IDs
	Sources  []string // which calls answered
	Failures []string // "method: error" for those that did not
}

var userIDRe = regexp.MustCompile(`\b[UW][A-Z0-9]{8,}\b`)

// FetchPrefs reads the VIP list and the starred conversations with the
// user token. It fails only when every source failed.
func (s *CanvasService) FetchPrefs(ctx context.Context) (SlackPrefs, error) {
	var p SlackPrefs
	if raw, err := s.callUser(ctx, "users.prefs.get", url.Values{}); err != nil {
		p.Failures = append(p.Failures, "users.prefs.get: "+err.Error())
	} else {
		p.VIPs, p.VIPKeys = vipsFromPrefs(raw)
		p.Sources = append(p.Sources, "users.prefs.get")
	}

	starred, err := s.starredFromSections(ctx)
	if err == nil {
		p.Sources = append(p.Sources, "users.channelSections.list")
	} else {
		p.Failures = append(p.Failures, "users.channelSections.list: "+err.Error())
		starred, err = s.starredFromStars(ctx)
		if err == nil {
			p.Sources = append(p.Sources, "stars.list")
		} else {
			p.Failures = append(p.Failures, "stars.list: "+err.Error())
		}
	}
	p.Starred = starred

	if len(p.Sources) == 0 {
		return p, fmt.Errorf("no Slack preference source answered: %s", strings.Join(p.Failures, "; "))
	}
	return p, nil
}

// vipsFromPrefs finds the VIP list in users.prefs.get. The key is not
// documented and has changed between client versions, so every preference
// whose name mentions "vip" is read, and user IDs are taken from it
// whether it holds a comma-separated string, a JSON array or JSON text.
// Pure.
func vipsFromPrefs(raw []byte) (ids, keys []string) {
	var env struct {
		Prefs map[string]json.RawMessage `json:"prefs"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for k, v := range env.Prefs {
		if !strings.Contains(strings.ToLower(k), "vip") {
			continue
		}
		found := false
		for _, id := range userIDRe.FindAllString(string(v), -1) {
			found = true
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if found {
			keys = append(keys, k)
		}
	}
	sort.Strings(ids)
	sort.Strings(keys)
	return ids, keys
}

// starredFromSections reads the sidebar's Starred section.
func (s *CanvasService) starredFromSections(ctx context.Context) ([]string, error) {
	raw, err := s.callUser(ctx, "users.channelSections.list", url.Values{})
	if err != nil {
		return nil, err
	}
	return starredFromSectionsBody(raw)
}

// starredFromSectionsBody picks the conversations of the "stars" section.
// Pure.
func starredFromSectionsBody(raw []byte) ([]string, error) {
	var env struct {
		Sections []struct {
			Type       string `json:"channel_section_type"`
			ChannelIDs struct {
				IDs []string `json:"channel_ids"`
			} `json:"channel_ids_page"`
		} `json:"channel_sections"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("users.channelSections.list: %w", err)
	}
	var out []string
	for _, sec := range env.Sections {
		if sec.Type == "stars" {
			out = append(out, sec.ChannelIDs.IDs...)
		}
	}
	return out, nil
}

// starredFromStars reads the legacy star list (stars:read).
func (s *CanvasService) starredFromStars(ctx context.Context) ([]string, error) {
	raw, err := s.callUser(ctx, "stars.list", url.Values{"limit": {"1000"}})
	if err != nil {
		return nil, err
	}
	var env struct {
		Items []struct {
			Type    string `json:"type"`
			Channel string `json:"channel"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("stars.list: %w", err)
	}
	var out []string
	for _, it := range env.Items {
		switch it.Type {
		case "channel", "im", "group", "mpim":
			out = append(out, it.Channel)
		}
	}
	return out, nil
}
