package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// registerListTools wires the Slack Lists surface (`get_list_items`)
// into the MCP server. The tool requires a user-scope token because
// Slack denies `lists:read` on bot tokens; the table-driven shape
// already supports that gate via RequiresUserToken.
func (h *Hub) registerListTools(s *server.MCPServer) {
	h.register(s,
		toolDef{
			Name: "get_list_items",
			Description: "Read items from a Slack List (the structured-table feature surfaced under https://<workspace>.slack.com/lists/.../<list_id>). " +
				"Requires `lists:read` on the user token; bot tokens are not eligible. " +
				"Pass list_id (the F-prefix file ID from the list URL).",
			Opts: []mcp.ToolOption{
				mcp.WithString("list_id", mcp.Required(), mcp.Description("Slack List ID (F... — copy the second path segment from the list URL)")),
				mcp.WithNumber("limit", mcp.Description("Items per page (default: Slack picks)")),
				mcp.WithString("cursor", mcp.Description("Cursor from a previous response's next_cursor for pagination")),
				mcp.WithBoolean("with_fields", mcp.Description("Show every column cell per item (default: false — only the inferred title)")),
			},
			RequiresUserToken: true,
			Handle:            h.handleGetListItems,
		},
	)
}

func (h *Hub) handleGetListItems(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	listID, err := req.RequireString("list_id")
	if err != nil {
		return mcp.NewToolResultError("list_id is required"), nil
	}
	cursor := req.GetString("cursor", "")
	limit := int(req.GetFloat("limit", 0))
	withFields := req.GetBool("with_fields", false)

	if !h.Lists().HasToken() {
		return mcp.NewToolResultError(slack.ErrListsNoToken.Error()), nil
	}

	result, err := h.Lists().Items(ctx, listID, cursor, limit)
	if err != nil {
		if errors.Is(err, slack.ErrListsNoToken) {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(result.Items) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("list %s: no items", listID)), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "list %s — %d item(s)", listID, len(result.Items))
	if result.NextCursor != "" {
		fmt.Fprintf(&b, " (more: cursor=%s)", result.NextCursor)
	}
	b.WriteByte('\n')
	for _, it := range result.Items {
		renderListItem(&b, it, withFields)
	}
	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n")), nil
}

func renderListItem(b *strings.Builder, it slack.ListItem, withFields bool) {
	id := it.RowID
	if id == "" {
		id = it.ID
	}
	title := it.Title
	if title == "" {
		title = "(no title)"
	}
	fmt.Fprintf(b, "- [%s] %s\n", id, title)
	if !withFields {
		return
	}
	for _, f := range it.Fields {
		key := f.Key
		if key == "" {
			key = f.ColumnID
		}
		if key == "" || f.Display == "" {
			continue
		}
		fmt.Fprintf(b, "    %s: %s\n", key, f.Display)
	}
}

// Compile-time guard mirrors the pattern in search.go / unread.go:
// drift in mcp-go's server.ToolHandlerFunc surfaces here.
var _ server.ToolHandlerFunc = (*Hub)(nil).handleGetListItems
