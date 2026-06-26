package tools

import (
	"context"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// Narrow consumer contracts for the Slack-service layer, declared at
// the tools-package boundary so handler tests can substitute fake
// implementations without spinning up the goslack stack.
//
// Each interface mirrors the methods actually called by handlers
// today (see the discovery grep in commit history). The concrete
// `*slack.XService` types satisfy these implicitly; compile-time
// assertions at the bottom of this file enforce that — a method-name
// drift in either direction breaks the build instead of silently
// breaking tests.
//
// These interfaces are intentionally narrow: adding a method to a
// service should require an explicit decision to expose it through
// the contract, not happen as a side effect of an internal refactor.

// UserClient is the user-resolution surface tool handlers consume.
type UserClient interface {
	NamesFor(ctx context.Context, userIDs []string) map[string]string
	Name(ctx context.Context, userID string) string
	List(ctx context.Context) ([]goslack.User, error)
}

// ChannelClient is the channel-resolution / introspection surface.
type ChannelClient interface {
	ResolveID(ctx context.Context, name string) (string, error)
	Info(ctx context.Context, channelID string) (*goslack.Channel, error)
	List(ctx context.Context, limit int) ([]goslack.Channel, error)
	NamesForIDs(ctx context.Context, channelIDs []string) map[string]string
	Members(ctx context.Context, channelID string, limit int) ([]string, error)
	Archive(ctx context.Context, channelID string) error
	Unarchive(ctx context.Context, channelID string) error
}

// MessageClient covers reading channel history, fetching thread
// replies, posting, and adding reactions. Together with SearchClient
// this is the read/write surface against a workspace's content.
type MessageClient interface {
	History(ctx context.Context, p slack.HistoryParams) ([]goslack.Message, error)
	ThreadReplies(ctx context.Context, channelID, threadTS string) ([]goslack.Message, error)
	Post(ctx context.Context, channelID, text, threadTS string) (string, error)
	AddReaction(ctx context.Context, channelID, timestamp, emoji string) error
	Delete(ctx context.Context, channelID, timestamp string) error
}

// SearchClient wraps search.messages. Separate from MessageClient
// because Slack treats search as a different scope tier and we may
// want to fake it independently in tests (e.g. simulate empty
// results without touching history fakes).
type SearchClient interface {
	Messages(ctx context.Context, query string, count int) ([]goslack.SearchMessage, error)
}

// UnreadClient is the personal-workflow surface: requires a
// user-scope token in production, so handlers gate on
// `slackSession.HasUserToken()` before constructing requests.
type UnreadClient interface {
	UnreadAll(ctx context.Context, maxPerChannel int) ([]*slack.ChannelUnread, error)
	RecentDMActivity(ctx context.Context, hours, maxPerChannel int) ([]*slack.ChannelUnread, error)
	UnreadThreadMentions(ctx context.Context, hours int) ([]*slack.ChannelUnread, error)
	Self(ctx context.Context) (string, error)
	MarkRead(ctx context.Context, channelID, ts string) error
}

// ListClient wraps the Slack Lists surface (the "Lists" feature with
// F-prefix file IDs). Requires `lists:read` on the user token —
// handlers must gate on HasToken() before issuing requests, since
// bot tokens cannot carry this scope.
type ListClient interface {
	HasToken() bool
	Items(ctx context.Context, listID, cursor string, limit int) (*slack.ListItemsResult, error)
}

// Compile-time assertions: if the concrete services in
// `internal/slack` drift from the contracts above, the build breaks
// with a clear `does not implement` diagnostic that names the missing
// method. This is the cheapest possible interface-seam enforcement.
var (
	_ UserClient    = (*slack.UserService)(nil)
	_ ChannelClient = (*slack.ChannelService)(nil)
	_ MessageClient = (*slack.MessageService)(nil)
	_ SearchClient  = (*slack.SearchService)(nil)
	_ UnreadClient  = (*slack.UnreadService)(nil)
	_ ListClient    = (*slack.ListService)(nil)
)
