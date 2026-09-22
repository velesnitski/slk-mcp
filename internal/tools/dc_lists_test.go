package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/velesnitski/slk-mcp/internal/config"
)

const dcListsMethod = "slackLists.items.list"

// dcCallListItems drives get_list_items end to end: handler → ListService
// → raw HTTP → the fake.
func dcCallListItems(t *testing.T, h *Hub, args map[string]any) string {
	t.Helper()
	res, err := h.handleGetListItems(context.Background(), callToolRequest("get_list_items", args))
	if err != nil {
		t.Fatalf("get_list_items returned a transport error: %v", err)
	}
	return resultText(res)
}

// dcListBody decodes the JSON payload the ListService posted, so a test
// can assert on the arguments it built rather than on its internals.
func dcListBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read lists request body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode lists request body %q: %v", raw, err)
	}
	return body
}

func TestGetListItems_RendersItemsAndCursor(t *testing.T) {
	f := newFakeSlack(t)
	f.On(dcListsMethod, jsonBody(t, map[string]any{
		"ok": true,
		"items": []any{
			map[string]any{
				"id":     "Rec1",
				"row_id": "R1",
				"fields": []any{
					map[string]any{"key": "title", "value": "Rotate the staging key"},
					map[string]any{"column_id": "Col9", "value": "open"},
				},
			},
			map[string]any{
				"id": "Rec2",
				"fields": []any{
					map[string]any{"key": "title", "value": "Write the runbook"},
				},
			},
		},
		"response_metadata": map[string]any{"next_cursor": "page2"},
	}))
	h := newFakeHub(t, f)

	got := dcCallListItems(t, h, map[string]any{"list_id": "F1"})

	for _, want := range []string{
		"list F1 — 2 item(s)",
		"(more: cursor=page2)",
		"- [R1] Rotate the staging key",
		"- [Rec2] Write the runbook",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// Without with_fields the per-column cells stay out of the way.
	if strings.Contains(got, "Col9") || strings.Contains(got, "open") {
		t.Errorf("cells leaked into the default rendering:\n%s", got)
	}
}

func TestGetListItems_WithFieldsShowsEveryCell(t *testing.T) {
	f := newFakeSlack(t)
	f.On(dcListsMethod, jsonBody(t, map[string]any{
		"ok": true,
		"items": []any{
			map[string]any{
				"row_id": "R1",
				"fields": []any{
					map[string]any{"key": "title", "value": "Rotate the staging key"},
					map[string]any{"column_id": "Col9", "value": "open"},
					// No key and no column id — unaddressable, so it is dropped.
					map[string]any{"value": "orphan"},
					// Addressable but empty — nothing to show.
					map[string]any{"key": "notes", "value": nil},
				},
			},
		},
	}))
	h := newFakeHub(t, f)

	got := dcCallListItems(t, h, map[string]any{"list_id": "F1", "with_fields": true})

	if !strings.Contains(got, "title: Rotate the staging key") {
		t.Errorf("keyed cell missing:\n%s", got)
	}
	if !strings.Contains(got, "Col9: open") {
		t.Errorf("column-id cell must fall back to its id:\n%s", got)
	}
	if strings.Contains(got, "orphan") {
		t.Errorf("a cell with neither key nor column id must be dropped:\n%s", got)
	}
	if strings.Contains(got, "notes:") {
		t.Errorf("an empty cell must be dropped:\n%s", got)
	}
	if strings.Contains(got, "(more: cursor=") {
		t.Errorf("no next_cursor means no pagination hint:\n%s", got)
	}
}

func TestGetListItems_EmptyListSaysSoInsteadOfNothing(t *testing.T) {
	f := newFakeSlack(t)
	f.On(dcListsMethod, `{"ok":true,"items":[]}`)
	h := newFakeHub(t, f)

	if got := dcCallListItems(t, h, map[string]any{"list_id": "F1"}); got != "list F1: no items" {
		t.Errorf("empty list rendered as %q", got)
	}
}

func TestGetListItems_UntitledRowStaysIdentifiable(t *testing.T) {
	f := newFakeSlack(t)
	// No id, no row_id, no cells: nothing to title the row with.
	f.On(dcListsMethod, `{"ok":true,"items":[{}]}`)
	h := newFakeHub(t, f)

	got := dcCallListItems(t, h, map[string]any{"list_id": "F1"})
	if !strings.Contains(got, "(no title)") {
		t.Errorf("a row with no title must still be listed:\n%s", got)
	}
}

func TestGetListItems_ForwardsPagingArguments(t *testing.T) {
	f := newFakeSlack(t)
	var body map[string]any
	f.OnFunc(dcListsMethod, func(r *http.Request) string {
		body = dcListBody(t, r)
		return `{"ok":true,"items":[{"row_id":"R1","fields":[{"key":"title","value":"x"}]}]}`
	})
	h := newFakeHub(t, f)

	dcCallListItems(t, h, map[string]any{"list_id": "F1", "cursor": "page2", "limit": float64(7)})

	if body["list_id"] != "F1" {
		t.Errorf("list_id not forwarded: %v", body)
	}
	if body["cursor"] != "page2" {
		t.Errorf("cursor not forwarded: %v", body)
	}
	if body["limit"] != float64(7) {
		t.Errorf("limit not forwarded: %v", body)
	}
}

func TestGetListItems_SlackErrorIsReportedVerbatim(t *testing.T) {
	f := newFakeSlack(t)
	f.OnError(dcListsMethod, "missing_scope")
	h := newFakeHub(t, f)

	got := dcCallListItems(t, h, map[string]any{"list_id": "F1"})
	if !strings.Contains(got, "missing_scope") {
		t.Errorf("the Slack error code must survive to the caller: %q", got)
	}
}

func TestGetListItems_WithoutUserTokenRefusesClearly(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f, func(c *config.Config) { c.UserToken = "" })

	got := dcCallListItems(t, h, map[string]any{"list_id": "F1"})

	if !strings.Contains(got, "SLACK_USER_TOKEN") || !strings.Contains(got, "lists:read") {
		t.Errorf("the refusal must name the missing credential, got %q", got)
	}
	// An empty answer that looks successful is the failure mode; no call
	// should even be attempted.
	if f.Called(dcListsMethod) {
		t.Errorf("the Lists API must not be called without a token; calls=%v", f.Calls())
	}
}

func TestGetListItems_MissingListIDIsRefused(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	if got := dcCallListItems(t, h, map[string]any{}); got != "list_id is required" {
		t.Errorf("missing list_id rendered as %q", got)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("an argument error must not reach Slack; calls=%v", f.Calls())
	}
}
