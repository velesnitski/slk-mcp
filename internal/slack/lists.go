package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
)

// Default endpoint for slackLists.items.list. Overridable via
// ListService.BaseURL for tests; production code reads slack-go's
// goslack.APIURL constant.
const defaultListsAPIBase = "https://slack.com/api/"

// ListItem is the surface shape of a single row from a Slack List
// (the new "Lists" feature — internally files with F-prefix IDs). The
// Slack API returns each item with a generic, schema-driven field
// array; we keep the raw cells alongside a best-effort flattened
// `Title` so callers have something useful to show even when the
// schema is unfamiliar.
type ListItem struct {
	ID      string         `json:"id"`
	RowID   string         `json:"row_id,omitempty"`
	Title   string         `json:"-"`
	Fields  []ListField    `json:"fields,omitempty"`
	Created string         `json:"created,omitempty"`
	Raw     map[string]any `json:"-"`
}

// ListField is one cell from a list row. The Slack schema is column-
// id keyed and the value can be a string, number, person, date, etc.;
// we keep the raw JSON value behind `RawValue` and a stringified form
// behind `Display` so the renderer doesn't need to know each cell
// type.
type ListField struct {
	ColumnID string `json:"column_id,omitempty"`
	Key      string `json:"key,omitempty"`
	Display  string `json:"-"`
	RawValue any    `json:"value,omitempty"`
}

// ListItemsResult is the shape returned by ListService.Items: the
// item slice plus a cursor for pagination. NextCursor is empty when
// the page is the last one (Slack returns "" or omits the field).
type ListItemsResult struct {
	Items      []ListItem
	NextCursor string
}

// ListService wraps the slackLists.items.list endpoint.
//
// slack-go (v0.15.0 at time of writing) has no helpers for the Lists
// API surface, so this service speaks raw HTTP. The token must carry
// the `lists:read` OAuth scope — a missing scope surfaces as
// `missing_scope` in Slack's `error` field; we forward that verbatim
// so the operator immediately knows what to fix at the app-install
// level (the slk-mcp tool itself has no path to add scopes).
//
// Cost shape: 1 HTTP request per page (default 100 items). For lists
// with a few dozen rows that fits inside a single call.
type ListService struct {
	token    string
	http     *http.Client
	log      *slog.Logger
	BaseURL  string // override for tests; defaults to defaultListsAPIBase
	Endpoint string // override for tests; defaults to "slackLists.items.list"
}

func newListService(token string, log *slog.Logger) *ListService {
	return &ListService{
		token:    token,
		http:     &http.Client{Timeout: 30 * time.Second},
		log:      log,
		BaseURL:  defaultListsAPIBase,
		Endpoint: "slackLists.items.list",
	}
}

// HasToken reports whether the service has a user token configured.
// Lists API requires a user-scope token; bot tokens are not eligible
// for `lists:read`.
func (s *ListService) HasToken() bool { return s.token != "" }

// Items fetches one page from the list. `cursor` may be empty for the
// first page; passing the previous response's NextCursor walks
// subsequent pages. `limit <= 0` lets Slack pick its default.
//
// Errors:
//   - `ErrListsNoToken` when no user token is configured.
//   - Slack-side errors (`missing_scope`, `invalid_arguments`,
//     `list_not_found`, etc.) are returned verbatim with the
//     `slack-lists: <error_code>` prefix so callers can pattern-match.
//   - Transport/parse failures are wrapped with %w for errors.Is/As.
func (s *ListService) Items(ctx context.Context, listID, cursor string, limit int) (*ListItemsResult, error) {
	if !s.HasToken() {
		return nil, ErrListsNoToken
	}
	if strings.TrimSpace(listID) == "" {
		return nil, errors.New("slack-lists: list_id is required")
	}

	body := map[string]any{"list_id": listID}
	if cursor != "" {
		body["cursor"] = cursor
	}
	if limit > 0 {
		body["limit"] = limit
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("slack-lists: encode request: %w", err)
	}

	url := strings.TrimRight(s.BaseURL, "/") + "/" + s.Endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("slack-lists: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("slack-lists: http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("slack-lists: read body: %w", err)
	}

	// Slack rate-limits surface as 429 with Retry-After (seconds).
	// We do not retry inline — Lists API is low-volume, and the
	// caller can re-issue. Surfacing as a goslack.RateLimitedError
	// lets DoR-style wrappers retry transparently in the future
	// without changing this signature.
	if resp.StatusCode == http.StatusTooManyRequests {
		wait := parseRetryAfterSeconds(resp.Header.Get("Retry-After"))
		return nil, &goslack.RateLimitedError{RetryAfter: wait}
	}

	return parseListItemsResponse(raw)
}

// ErrListsNoToken is the sentinel for a missing user token. Callers
// (and the tool handler) check this with errors.Is so the error
// message can stay readable.
var ErrListsNoToken = errors.New("slack-lists: a SLACK_USER_TOKEN with the lists:read scope is required")

// parseListItemsResponse decodes the slackLists.items.list payload.
// The decoder is deliberately tolerant: Slack has historically
// shifted field names on this surface, so we keep the raw map for
// each item, then extract the common fields we render today
// (`id`, `row_id`, the cell array, a best-effort `Title`).
func parseListItemsResponse(raw []byte) (*ListItemsResult, error) {
	var envelope struct {
		OK         bool             `json:"ok"`
		Error      string           `json:"error,omitempty"`
		Items      []map[string]any `json:"items,omitempty"`
		NextCursor string           `json:"next_cursor,omitempty"`
		ResponseMD struct {
			NextCursor string `json:"next_cursor,omitempty"`
		} `json:"response_metadata,omitempty"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("slack-lists: parse response: %w (body=%q)",
			err, truncateRaw(raw, 200))
	}
	if !envelope.OK {
		if envelope.Error == "" {
			envelope.Error = "unknown_error"
		}
		return nil, fmt.Errorf("slack-lists: %s", envelope.Error)
	}

	out := &ListItemsResult{
		Items:      make([]ListItem, 0, len(envelope.Items)),
		NextCursor: envelope.NextCursor,
	}
	if out.NextCursor == "" {
		out.NextCursor = envelope.ResponseMD.NextCursor
	}
	for _, rawItem := range envelope.Items {
		out.Items = append(out.Items, hydrateListItem(rawItem))
	}
	return out, nil
}

// hydrateListItem maps the Slack-side raw item map to ListItem. The
// `Title` is a best-effort: prefer an explicit `title` cell, fall
// back to the first non-empty string cell, fall back to row id. This
// keeps each row identifiable in digest output even when the list
// schema is unfamiliar.
func hydrateListItem(raw map[string]any) ListItem {
	li := ListItem{Raw: raw}
	if id, ok := raw["id"].(string); ok {
		li.ID = id
	}
	if rowID, ok := raw["row_id"].(string); ok {
		li.RowID = rowID
	}
	if created, ok := raw["created"].(string); ok {
		li.Created = created
	}

	rawFields, _ := raw["fields"].([]any)
	for _, rf := range rawFields {
		m, ok := rf.(map[string]any)
		if !ok {
			continue
		}
		field := ListField{RawValue: m["value"]}
		if cid, ok := m["column_id"].(string); ok {
			field.ColumnID = cid
		}
		if k, ok := m["key"].(string); ok {
			field.Key = k
		}
		field.Display = displayValue(m["value"])
		li.Fields = append(li.Fields, field)
	}

	li.Title = bestEffortTitle(li.Fields, li.RowID, li.ID)
	return li
}

// bestEffortTitle picks the most "title-like" cell so the renderer
// has something to show. The selection order — explicit title key,
// first non-empty string display — keeps single-column lists
// readable without forcing the caller to know the schema.
func bestEffortTitle(fields []ListField, rowID, id string) string {
	for _, f := range fields {
		if strings.EqualFold(f.Key, "title") && f.Display != "" {
			return f.Display
		}
	}
	for _, f := range fields {
		if f.Display != "" {
			return f.Display
		}
	}
	if rowID != "" {
		return rowID
	}
	return id
}

// displayValue renders any cell value as a single human-readable
// line. Strings pass through; numbers are formatted with %v; objects
// (date, person) get a compact JSON form; nil yields the empty
// string.
func displayValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return fmt.Sprintf("%v", t)
	case bool:
		return fmt.Sprintf("%v", t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, el := range t {
			s := displayValue(el)
			if s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		// Map or unknown: JSON-encode so the renderer never errors.
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// parseRetryAfterSeconds turns Slack's Retry-After header into a
// duration. Empty or unparseable values fall back to 1s — small
// enough to keep latency bounded, large enough to avoid hammering
// the API in a tight loop.
func parseRetryAfterSeconds(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return time.Second
	}
	// Slack documents seconds; some proxies forward HTTP-date instead.
	// We accept the integer form only — anything fancier falls back
	// to the default.
	var seconds int
	if _, err := fmt.Sscanf(header, "%d", &seconds); err != nil || seconds <= 0 {
		return time.Second
	}
	return time.Duration(seconds) * time.Second
}

// truncateRaw lets parse-error messages include just enough of the
// failing body to debug without spilling secrets or flooding logs.
func truncateRaw(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
