package slack

import (
	"context"
	"log/slog"
	"strings"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// UserGroupService reads and edits user groups (Slack's @handle that
// notifies a team at once). The user token is preferred: a person's own
// rights decide who may edit groups, and usergroups:write is usually
// granted as a user scope. Falls back to the primary client when the
// workspace has no user token.
type UserGroupService struct {
	api *goslack.Client
	log *slog.Logger
}

func newUserGroupService(primary, user *goslack.Client, log *slog.Logger) *UserGroupService {
	api := user
	if api == nil {
		api = primary
	}
	return &UserGroupService{api: api, log: log}
}

// List returns the workspace's active user groups with their member IDs.
func (s *UserGroupService) List(ctx context.Context) ([]goslack.UserGroup, error) {
	return ratelimit.DoR(ctx, s.log, func() ([]goslack.UserGroup, error) {
		return s.api.GetUserGroupsContext(ctx,
			goslack.GetUserGroupsOptionIncludeUsers(true),
			goslack.GetUserGroupsOptionIncludeCount(true))
	})
}

// Create makes a new user group. Slack's usergroups.create takes no
// members; the caller sets them with SetMembers.
func (s *UserGroupService) Create(ctx context.Context, name, handle, description string) (goslack.UserGroup, error) {
	return ratelimit.DoR(ctx, s.log, func() (goslack.UserGroup, error) {
		return s.api.CreateUserGroupContext(ctx, goslack.UserGroup{
			Name: name, Handle: handle, Description: description,
		})
	})
}

// SetMembers replaces the group's members with ids.
func (s *UserGroupService) SetMembers(ctx context.Context, groupID string, ids []string) (goslack.UserGroup, error) {
	return ratelimit.DoR(ctx, s.log, func() (goslack.UserGroup, error) {
		return s.api.UpdateUserGroupMembersContext(ctx, groupID, strings.Join(ids, ","))
	})
}
