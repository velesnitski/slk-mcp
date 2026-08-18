package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	goslack "github.com/slack-go/slack"
)

// defaultFilesAPIBase is the Slack Web API root used by the raw
// files.list call below. Overridable via CanvasService.BaseURL in tests.
const defaultFilesAPIBase = "https://slack.com/api/"

// canvasListPageSize is how many canvases one files.list page returns.
// Canvases are rare relative to other file types, so a single page
// comfortably covers a workspace's recent edits.
const canvasListPageSize = 100

// CanvasRef is a canvas as the workspace-wide sweep sees it: enough to
// decide "did this change since my cursor" and to fetch its body if the
// caller wants to look for a mention.
//
// Created/Updated are Slack's Unix seconds. Updated is the field that
// matters here and the reason this type exists at all — see
// RecentCanvases.
type CanvasRef struct {
	ID          string
	Title       string
	Channels    []string
	Created     int64
	Updated     int64
	Mimetype    string
	Permalink   string
	DownloadURL string
}

// ChangedAfterCreate reports whether the canvas was edited after it was
// first created, i.e. this is an edit rather than a brand-new document.
// Slack sets updated == created on creation; a one-second skew has been
// observed, so allow it.
func (c CanvasRef) ChangedAfterCreate() bool { return c.Updated-c.Created > 1 }

// ErrCanvasNoToken is the sentinel for a missing user token on the
// canvas-delta path. files.list without a channel filter needs user
// visibility — a bot token only ever sees canvases in channels it has
// been invited to, which is precisely the blind spot this closes.
var ErrCanvasNoToken = errors.New("slack-canvas: a SLACK_USER_TOKEN with the files:read scope is required for canvas discovery")

// RecentCanvases lists canvases in the workspace whose `updated`
// timestamp is newer than `since` (Unix seconds), newest edit first.
//
// Why raw HTTP instead of the slack-go SDK: goslack.File exposes only
// `Created` and `Timestamp` — it drops files.list's `updated` field
// entirely. Through the typed SDK a canvas that was EDITED is
// indistinguishable from one that was not, so an edit-driven delta is
// impossible to express. Since an edit (someone adding a line that
// @-mentions the operator) produces no channel message at all, that gap
// is the whole reason canvas activity never reached the unread sweep.
// This method decodes the field by hand.
//
// `limit <= 0` applies no cap beyond the single page fetched.
func (s *CanvasService) RecentCanvases(ctx context.Context, since int64, limit int) ([]CanvasRef, error) {
	if s.token == "" {
		return nil, ErrCanvasNoToken
	}

	form := url.Values{}
	form.Set("types", "canvas")
	form.Set("count", strconv.Itoa(canvasListPageSize))
	// NOTE: files.list `ts_from`/`ts_to` filter on CREATE time, not update
	// time, so they cannot narrow this query — an old canvas edited a
	// minute ago is exactly the case we care about. The page is fetched
	// unfiltered and selected on `updated` client-side.

	endpoint := strings.TrimRight(s.BaseURL, "/") + "/files.list"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("slack-canvas: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("slack-canvas: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("slack-canvas: read body: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, &goslack.RateLimitedError{RetryAfter: parseRetryAfterSeconds(resp.Header.Get("Retry-After"))}
	}

	refs, err := parseCanvasFilesResponse(raw)
	if err != nil {
		return nil, err
	}
	return selectRecentCanvases(refs, since, limit), nil
}

// parseCanvasFilesResponse decodes a files.list payload into CanvasRefs.
// Tolerant by design: `updated` is undocumented on this surface and has
// been observed both as a number and (rarely) absent, so a missing
// value falls back to `created` rather than failing the whole page.
// Pure — unit-tested without any API.
func parseCanvasFilesResponse(raw []byte) ([]CanvasRef, error) {
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
		Files []struct {
			ID          string   `json:"id"`
			Title       string   `json:"title"`
			Name        string   `json:"name"`
			Created     int64    `json:"created"`
			Updated     int64    `json:"updated"`
			Timestamp   int64    `json:"timestamp"`
			Mimetype    string   `json:"mimetype"`
			Permalink   string   `json:"permalink"`
			URLDownload string   `json:"url_private_download"`
			URLPrivate  string   `json:"url_private"`
			Channels    []string `json:"channels"`
			Groups      []string `json:"groups"`
			IMs         []string `json:"ims"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("slack-canvas: parse response: %w (body=%q)", err, truncateRaw(raw, 200))
	}
	if !envelope.OK {
		if envelope.Error == "" {
			envelope.Error = "unknown_error"
		}
		return nil, fmt.Errorf("slack-canvas: %s", envelope.Error)
	}

	out := make([]CanvasRef, 0, len(envelope.Files))
	for _, f := range envelope.Files {
		title := strings.TrimSpace(f.Title)
		if title == "" {
			title = strings.TrimSpace(f.Name)
		}
		created := f.Created
		if created == 0 {
			created = f.Timestamp
		}
		updated := f.Updated
		if updated == 0 {
			updated = created
		}
		dl := f.URLDownload
		if dl == "" {
			dl = f.URLPrivate
		}
		chans := make([]string, 0, len(f.Channels)+len(f.Groups)+len(f.IMs))
		chans = append(chans, f.Channels...)
		chans = append(chans, f.Groups...)
		chans = append(chans, f.IMs...)
		out = append(out, CanvasRef{
			ID:          f.ID,
			Title:       title,
			Channels:    chans,
			Created:     created,
			Updated:     updated,
			Mimetype:    f.Mimetype,
			Permalink:   f.Permalink,
			DownloadURL: dl,
		})
	}
	return out, nil
}

// selectRecentCanvases keeps canvases updated strictly after `since`,
// sorts them newest-edit-first, and applies `limit` (<=0 means no cap).
// `since <= 0` disables the time filter. Pure.
func selectRecentCanvases(refs []CanvasRef, since int64, limit int) []CanvasRef {
	out := make([]CanvasRef, 0, len(refs))
	for _, r := range refs {
		if since > 0 && r.Updated <= since {
			continue
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated > out[j].Updated })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
