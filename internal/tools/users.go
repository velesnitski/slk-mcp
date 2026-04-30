package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func registerUserTools(s *server.MCPServer, d Deps) {
	if d.Cfg.IsDisabled("list_users") {
		return
	}
	s.AddTool(
		mcp.NewTool("list_users",
			mcp.WithDescription("List active workspace users with handle, real name, and role flags."),
			mcp.WithBoolean("include_bots", mcp.Description("Include bot/integration accounts (default: false)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			includeBots := req.GetBool("include_bots", false)

			users, err := d.Client.Users.List(ctx)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

			var b strings.Builder
			kept := 0
			for _, u := range users {
				if (u.IsBot || u.Name == "slackbot") && !includeBots {
					continue
				}
				kept++
				flags := ""
				switch {
				case u.IsAdmin:
					flags = " admin"
				case u.IsOwner:
					flags = " owner"
				case u.IsRestricted:
					flags = " guest"
				}
				if u.IsBot {
					flags += " bot"
				}
				real := u.RealName
				if real == "" {
					real = u.Profile.RealName
				}
				if real == "" {
					real = u.Profile.DisplayName
				}
				if real == "" {
					real = "(no name)"
				}
				fmt.Fprintf(&b, "%s | %s |%s\n", u.Name, real, flags)
			}
			header := fmt.Sprintf("%d users\n", kept)
			return mcp.NewToolResultText(header + strings.TrimRight(b.String(), "\n")), nil
		},
	)
}
