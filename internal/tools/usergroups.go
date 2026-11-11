package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	goslack "github.com/slack-go/slack"
)

// registerUserGroupTools wires list_usergroups and manage_usergroup. A
// user group is Slack's @handle that notifies a whole team at once, so a
// recurring list of people ("all tech leads") is kept in Slack itself
// and tagged with one word. See ADR 138.
func (h *Hub) registerUserGroupTools(s *server.MCPServer) {
	h.register(s,
		toolDef{
			Name:        "list_usergroups",
			Description: "List the workspace's user groups (the @handle that notifies a team at once): handle, name, ID and members. Needs the usergroups:read scope.",
			Opts: []mcp.ToolOption{
				mcp.WithString("filter", mcp.Description("Case-insensitive substring of the handle or name")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			},
			Handle: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.runListUserGroups(ctx, req.GetString("workspace", ""), req.GetString("filter", "")), nil
			},
		},
		toolDef{
			Name:        "manage_usergroup",
			Description: "Create a user group or change its members, so one @handle tags the whole list. Members are given as handles or names (\"@pat, Sam Lee\"); every one must match exactly one person or nothing is changed. mode=set (default) replaces the members, add and remove edit them. A handle that does not exist yet is created (name defaults to the handle). Needs the usergroups:write scope; the workspace may also limit who can edit groups.",
			Opts: []mcp.ToolOption{
				mcp.WithString("handle", mcp.Required(), mcp.Description("Group handle without @, e.g. tech-leads")),
				mcp.WithString("members", mcp.Required(), mcp.Description("People, separated by commas or spaces: @handle, handle, or a display/real name (names with spaces must be comma-separated)")),
				mcp.WithString("mode", mcp.Description("set (default) | add | remove")),
				mcp.WithString("name", mcp.Description("Group name when creating (default: the handle)")),
				mcp.WithString("description", mcp.Description("Group description when creating")),
				mcp.WithBoolean("dry_run", mcp.Description("Resolve the members and show what would change, without changing anything (default: false)")),
				mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
			},
			Writes: true,
			Handle: func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				handle, err := req.RequireString("handle")
				if err != nil {
					return mcp.NewToolResultError("handle is required"), nil
				}
				members, err := req.RequireString("members")
				if err != nil {
					return mcp.NewToolResultError("members is required"), nil
				}
				return h.runManageUserGroup(ctx, manageGroupArgs{
					workspace:   req.GetString("workspace", ""),
					handle:      handle,
					members:     members,
					mode:        req.GetString("mode", "set"),
					name:        req.GetString("name", ""),
					description: req.GetString("description", ""),
					dryRun:      req.GetBool("dry_run", false),
				}), nil
			},
		},
	)
}

func (h *Hub) runListUserGroups(ctx context.Context, workspace, filter string) *mcp.CallToolResult {
	scoped, wsName, errRes := h.scopedWorkspace(workspace)
	if errRes != nil {
		return errRes
	}
	groups, err := scoped.Groups().List(ctx)
	if err != nil {
		return mcp.NewToolResultError(groupErrorHint(err, "usergroups:read"))
	}
	users, _ := scoped.Users().List(ctx) // names only; IDs stand in if unreadable
	names := map[string]string{}
	for _, u := range users {
		names[u.ID] = firstNonEmpty(u.Name, u.RealName, u.ID)
	}
	filter = strings.ToLower(strings.TrimSpace(filter))

	sort.Slice(groups, func(i, j int) bool { return groups[i].Handle < groups[j].Handle })
	var lines []string
	for _, g := range groups {
		if g.DateDelete.Time().Unix() > 0 {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(g.Handle+" "+g.Name), filter) {
			continue
		}
		who := make([]string, 0, len(g.Users))
		for _, id := range g.Users {
			who = append(who, "@"+firstNonEmpty(names[id], id))
		}
		lines = append(lines, fmt.Sprintf("@%s | %s | %s | %d members: %s", g.Handle, g.Name, g.ID, len(g.Users), strings.Join(who, " ")))
	}
	if len(lines) == 0 {
		return mcp.NewToolResultText("no user groups" + h.wsLabel(wsName))
	}
	return mcp.NewToolResultText(fmt.Sprintf("%d user groups%s\n%s", len(lines), h.wsLabel(wsName), strings.Join(lines, "\n")))
}

type manageGroupArgs struct {
	workspace, handle, members, mode, name, description string
	dryRun                                              bool
}

var groupHandleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func (h *Hub) runManageUserGroup(ctx context.Context, a manageGroupArgs) *mcp.CallToolResult {
	handle := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(a.handle), "@"))
	if !groupHandleRe.MatchString(handle) {
		return mcp.NewToolResultError(fmt.Sprintf("handle %q: use lowercase letters, digits, dots, dashes or underscores", a.handle))
	}
	mode := strings.ToLower(strings.TrimSpace(a.mode))
	if mode == "" {
		mode = "set"
	}
	if mode != "set" && mode != "add" && mode != "remove" {
		return mcp.NewToolResultError(fmt.Sprintf("mode %q: use set, add or remove", a.mode))
	}
	scoped, wsName, errRes := h.scopedWorkspace(a.workspace)
	if errRes != nil {
		return errRes
	}

	users, err := scoped.Users().List(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	ids, label, problems := resolvePeople(a.members, users)
	if len(problems) > 0 {
		return mcp.NewToolResultError("nothing changed – fix these members first:\n" + strings.Join(problems, "\n"))
	}
	if len(ids) == 0 {
		return mcp.NewToolResultError("members: no one given")
	}

	groups, err := scoped.Groups().List(ctx)
	if err != nil {
		return mcp.NewToolResultError(groupErrorHint(err, "usergroups:read"))
	}
	var group *goslack.UserGroup
	for i := range groups {
		if strings.EqualFold(groups[i].Handle, handle) && groups[i].DateDelete.Time().Unix() <= 0 {
			group = &groups[i]
			break
		}
	}

	var current []string
	if group != nil {
		current = group.Users
	} else if mode == "remove" {
		return mcp.NewToolResultError(fmt.Sprintf("no user group @%s to remove members from", handle))
	}
	next := nextMembers(current, ids, mode)
	if len(next) == 0 {
		return mcp.NewToolResultError("a user group cannot be left without members; nothing changed")
	}
	added, removed := diffMembers(current, next)

	nameOf := func(list []string) string {
		out := make([]string, 0, len(list))
		for _, id := range list {
			out = append(out, firstNonEmpty(label[id], userLabel(users, id)))
		}
		return strings.Join(out, ", ")
	}
	summary := func(verb string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "%s @%s%s – %d members", verb, handle, h.wsLabel(wsName), len(next))
		if len(added) > 0 {
			fmt.Fprintf(&b, "\nadded: %s", nameOf(added))
		}
		if len(removed) > 0 {
			fmt.Fprintf(&b, "\nremoved: %s", nameOf(removed))
		}
		if len(added) == 0 && len(removed) == 0 {
			b.WriteString("\nmembers already match; nothing to change")
		}
		return b.String()
	}

	if a.dryRun {
		verb := "would update"
		if group == nil {
			verb = "would create"
		}
		return mcp.NewToolResultText(summary(verb))
	}
	if group != nil && len(added) == 0 && len(removed) == 0 {
		return mcp.NewToolResultText(summary("unchanged"))
	}

	verb := "updated"
	groupID := ""
	if group == nil {
		name := strings.TrimSpace(a.name)
		if name == "" {
			name = handle
		}
		g, err := scoped.Groups().Create(ctx, name, handle, a.description)
		if err != nil {
			return mcp.NewToolResultError(groupErrorHint(err, "usergroups:write"))
		}
		groupID, verb = g.ID, "created"
	} else {
		groupID = group.ID
	}
	if _, err := scoped.Groups().SetMembers(ctx, groupID, next); err != nil {
		msg := groupErrorHint(err, "usergroups:write")
		if verb == "created" {
			msg = fmt.Sprintf("created @%s (%s) but setting its members failed: %s", handle, groupID, msg)
		}
		return mcp.NewToolResultError(msg)
	}
	return mcp.NewToolResultText(summary(verb) + "\ntag them all with @" + handle)
}

// resolvePeople maps a members string to user IDs. Each entry must name
// exactly one person by handle, display name or real name; anything else
// is reported, so a group is never edited on a guess.
func resolvePeople(members string, users []goslack.User) ([]string, map[string]string, []string) {
	names, label := mentionIndex(users)
	byName := map[string][]string{}
	for _, n := range names {
		k := strings.ToLower(n.name)
		if !containsString(byName[k], n.id) {
			byName[k] = append(byName[k], n.id)
		}
	}
	var ids, problems []string
	for _, entry := range splitMembers(members) {
		key := strings.ToLower(strings.TrimPrefix(entry, "@"))
		match := byName[key]
		if _, isID := label[strings.ToUpper(key)]; isID && len(match) == 0 {
			match = []string{strings.ToUpper(key)} // a raw user ID, as list_users prints it
		}
		switch len(match) {
		case 0:
			problems = append(problems, "no such person: "+entry)
		case 1:
			if !containsString(ids, match[0]) {
				ids = append(ids, match[0])
			}
		default:
			who := make([]string, 0, len(match))
			for _, id := range match {
				who = append(who, label[id])
			}
			problems = append(problems, fmt.Sprintf("%s matches more than one person: %s", entry, strings.Join(who, ", ")))
		}
	}
	return ids, label, problems
}

// splitMembers splits on commas when there are any (so names with
// spaces survive), otherwise on whitespace.
func splitMembers(s string) []string {
	var parts []string
	if strings.Contains(s, ",") {
		parts = strings.Split(s, ",")
	} else {
		parts = strings.Fields(s)
	}
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" && p != "@" {
			out = append(out, p)
		}
	}
	return out
}

func nextMembers(current, ids []string, mode string) []string {
	switch mode {
	case "add":
		out := append([]string(nil), current...)
		for _, id := range ids {
			if !containsString(out, id) {
				out = append(out, id)
			}
		}
		return out
	case "remove":
		var out []string
		for _, id := range current {
			if !containsString(ids, id) {
				out = append(out, id)
			}
		}
		return out
	}
	return ids
}

func diffMembers(current, next []string) (added, removed []string) {
	for _, id := range next {
		if !containsString(current, id) {
			added = append(added, id)
		}
	}
	for _, id := range current {
		if !containsString(next, id) {
			removed = append(removed, id)
		}
	}
	return added, removed
}

func userLabel(users []goslack.User, id string) string {
	for _, u := range users {
		if u.ID == id {
			return firstNonEmpty(u.RealName, u.Name, id)
		}
	}
	return id
}

// groupErrorHint names the scope or the workspace setting behind Slack's
// terse user-group errors.
func groupErrorHint(err error, scope string) string {
	e := err.Error()
	switch {
	case strings.Contains(e, "missing_scope"):
		return e + " – the token lacks the " + scope + " scope: add it under OAuth & Permissions → User Token Scopes, reinstall the app, then retry"
	case strings.Contains(e, "permission_denied"), strings.Contains(e, "no_permission"):
		return e + " – this workspace limits who may edit user groups; an admin can allow it in workspace settings"
	case strings.Contains(e, "paid_teams_only"), strings.Contains(e, "plan_upgrade_required"):
		return e + " – user groups need a paid Slack plan"
	case strings.Contains(e, "name_already_exists"), strings.Contains(e, "handle_already_exists"):
		return e + " – a group (possibly disabled) or a person already uses this name or handle"
	}
	return e
}
