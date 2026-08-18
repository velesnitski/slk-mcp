package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// ErrNoUserToken is returned by UnreadService methods when the server
// was started without SLACK_USER_TOKEN.
var ErrNoUserToken = errors.New(
	"SLACK_USER_TOKEN is required for this tool. " +
		"Create a Slack App with user scopes and set SLACK_USER_TOKEN=xoxp-...",
)

// UnreadService reports unread messages and mentions for the authenticated user.
// All methods require a user token (xoxp-).
type UnreadService struct {
	api      *goslack.Client
	channels *ChannelService
	users    *UserService
	search   *SearchService
	log      *slog.Logger

	selfMu  sync.RWMutex
	selfID  string
	teamURL string
}

func newUnreadService(api *goslack.Client, channels *ChannelService, users *UserService, search *SearchService, log *slog.Logger) *UnreadService {
	return &UnreadService{api: api, channels: channels, users: users, search: search, log: log}
}

// Self returns the authenticated user's Slack ID, calling auth.test on
// first use and caching the result for the lifetime of the service.
// Used by tools that highlight messages mentioning the operator.
func (s *UnreadService) Self(ctx context.Context) (string, error) {
	if !s.Enabled() {
		return "", ErrNoUserToken
	}

	s.selfMu.RLock()
	id := s.selfID
	s.selfMu.RUnlock()
	if id != "" {
		return id, nil
	}

	var resp *goslack.AuthTestResponse
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.AuthTestContext(ctx)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("auth.test: %w", err)
	}

	s.selfMu.Lock()
	s.selfID = resp.UserID
	s.teamURL = resp.URL
	s.selfMu.Unlock()
	return resp.UserID, nil
}

// TeamURL returns the workspace base URL from the same cached auth.test
// response Self uses, so permalinks can be built without a per-message
// chat.getPermalink call. Empty string when unavailable.
func (s *UnreadService) TeamURL(ctx context.Context) (string, error) {
	if _, err := s.Self(ctx); err != nil {
		return "", err
	}
	s.selfMu.RLock()
	defer s.selfMu.RUnlock()
	return s.teamURL, nil
}

// Enabled reports whether a user token is available.
func (s *UnreadService) Enabled() bool { return s.api != nil }

// ChannelUnread summarizes the unread state of a single channel.
//
// Replies maps a thread parent's timestamp to replies posted after
// LastRead, exclusive of the parent itself. A parent with no new
// replies is absent from the map.
type ChannelUnread struct {
	Channel  goslack.Channel
	LastRead string
	Messages []goslack.Message
	Replies  map[string][]goslack.Message
}

// JoinedChannels returns channels the user is a member of, useful for
// building the full unread sweep without iterating the whole workspace.
func (s *UnreadService) JoinedChannels(ctx context.Context) ([]goslack.Channel, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}

	var all []goslack.Channel
	cursor := ""
	for {
		params := &goslack.GetConversationsForUserParameters{
			// im + mpim cover direct messages and group DMs. Without
			// them, conversations the operator sees in their Slack
			// sidebar (DMs, multi-party chats) are silently dropped
			// from the unread sweep.
			Types:           []string{"public_channel", "private_channel", "mpim", "im"},
			Limit:           200,
			Cursor:          cursor,
			ExcludeArchived: true,
		}
		var channels []goslack.Channel
		var next string
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			ch, cur, err := s.api.GetConversationsForUserContext(ctx, params)
			if err != nil {
				return err
			}
			channels = ch
			next = cur
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("users.conversations: %w", err)
		}
		all = append(all, channels...)
		if next == "" {
			break
		}
		cursor = next
	}
	return all, nil
}

// Unread pulls messages newer than the user's last_read for the given channel.
// Returns a ChannelUnread with Messages empty if fully caught up.
func (s *UnreadService) Unread(ctx context.Context, channelID string, maxMessages int) (*ChannelUnread, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	if maxMessages <= 0 {
		maxMessages = 50
	}

	var info *goslack.Channel
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.GetConversationInfoContext(ctx, &goslack.GetConversationInfoInput{
			ChannelID:     channelID,
			IncludeLocale: false,
		})
		if err != nil {
			return err
		}
		info = r
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.info: %w", err)
	}

	cu := &ChannelUnread{Channel: *info, LastRead: info.LastRead}
	if info.LastRead == "" {
		return cu, nil
	}

	oldest, _ := strconv.ParseFloat(info.LastRead, 64)

	// Reach BEHIND last_read. A thread whose parent is already read stays
	// invisible otherwise: history starts at last_read, so the parent is
	// never returned, and fetchReplies has nothing to walk — the thread
	// can run for hours without ever entering the sweep. Only parents
	// with a reply newer than last_read survive the filter downstream, so
	// the widened window costs one bigger page, not more calls.
	histOldest := info.LastRead
	if lookback := float64(s.nowUnix() - threadLookbackHours*3600); lookback < oldest {
		histOldest = strconv.FormatFloat(lookback, 'f', 6, 64)
	}
	params := &goslack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Oldest:    histOldest,
		Limit:     maxMessages + threadParentHeadroom,
		Inclusive: false,
	}

	var resp *goslack.GetConversationHistoryResponse
	err = ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.GetConversationHistoryContext(ctx, params)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.history (unread): %w", err)
	}

	for _, msg := range resp.Messages {
		ts, _ := strconv.ParseFloat(msg.Timestamp, 64)
		if ts <= oldest {
			continue
		}
		cu.Messages = append(cu.Messages, msg)
	}

	// Parents come from the FULL page (including the pre-last_read
	// lookback); cu.Messages stays strictly "new since last_read".
	if err := s.fetchReplies(ctx, channelID, oldest, resp.Messages, cu); err != nil {
		// Replies are best-effort context; a failure here should not
		// block the rest of the digest. Log and continue.
		s.log.Warn("fetch thread replies failed", "channel", channelID, "err", err)
	}
	return cu, nil
}

// threadLookbackHours is how far BEFORE last_read the channel history is
// pulled, purely to find thread parents that are already read but still
// moving. It bounds the extra work: parents older than this cannot
// surface new replies through the sweep.
const threadLookbackHours = 12

// threadParentHeadroom is the extra page size requested so the
// pre-last_read lookback cannot crowd out genuinely new messages:
// conversations.history returns newest-first, so the new ones are always
// in the page, and the headroom is what leaves room for older parents.
const threadParentHeadroom = 30

// activeThreadParents picks the thread parents worth fetching replies
// for: those whose NEWEST reply landed after oldest. Slack returns
// latest_reply on every parent, so this filter is free and it is what
// lets an already-read parent stay in scope while its thread is alive.
// A parent with no latest_reply recorded falls back to the original
// rule (fetch when the parent itself is new), so nothing regresses.
// Pure.
func activeThreadParents(msgs []goslack.Message, oldest float64) []goslack.Message {
	var out []goslack.Message
	for _, m := range msgs {
		// A thread parent has thread_ts == ts and reply_count > 0.
		// A reply has thread_ts != ts; replies do not appear in
		// conversations.history so we never see them at this layer.
		if m.ThreadTimestamp == "" || m.ThreadTimestamp != m.Timestamp || m.ReplyCount == 0 {
			continue
		}
		probe := m.LatestReply
		if probe == "" {
			probe = m.Timestamp
		}
		if ts, _ := strconv.ParseFloat(probe, 64); ts > oldest {
			out = append(out, m)
		}
	}
	return out
}

// fetchReplies populates cu.Replies for every thread parent in parents
// that is still active — see activeThreadParents. Only replies strictly
// newer than oldest (the channel's last_read) are kept.
func (s *UnreadService) fetchReplies(ctx context.Context, channelID string, oldest float64, parents []goslack.Message, cu *ChannelUnread) error {
	for _, m := range activeThreadParents(parents, oldest) {
		var replies []goslack.Message
		err := ratelimit.Do(ctx, s.log, 0, func() error {
			params := &goslack.GetConversationRepliesParameters{
				ChannelID: channelID,
				Timestamp: m.Timestamp,
				Oldest:    m.Timestamp,
				Inclusive: false,
				Limit:     100,
			}
			r, _, _, err := s.api.GetConversationRepliesContext(ctx, params)
			if err != nil {
				return err
			}
			replies = r
			return nil
		})
		if err != nil {
			return fmt.Errorf("conversations.replies %s: %w", m.Timestamp, err)
		}

		var newer []goslack.Message
		for _, r := range replies {
			ts, _ := strconv.ParseFloat(r.Timestamp, 64)
			if ts <= oldest || r.Timestamp == m.Timestamp {
				continue
			}
			newer = append(newer, r)
		}
		if len(newer) == 0 {
			continue
		}
		if cu.Replies == nil {
			cu.Replies = make(map[string][]goslack.Message)
		}
		cu.Replies[m.Timestamp] = newer
	}
	return nil
}

// UnreadAll returns unread state for all channels the user joined.
// Channels with zero unread are omitted from the result.
func (s *UnreadService) UnreadAll(ctx context.Context, maxPerChannel int) ([]*ChannelUnread, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	channels, err := s.JoinedChannels(ctx)
	if err != nil {
		return nil, err
	}

	// Fetch in parallel, capped to a modest concurrency to stay polite.
	const workers = 4
	type result struct {
		cu  *ChannelUnread
		err error
	}

	jobs := make(chan goslack.Channel, len(channels))
	results := make(chan result, len(channels))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ch := range jobs {
				// users.conversations does not populate unread_count;
				// Unread() fetches conversations.info and short-circuits
				// when the channel is actually caught up.
				cu, err := s.Unread(ctx, ch.ID, maxPerChannel)
				results <- result{cu, err}
			}
		}()
	}
	for _, ch := range channels {
		jobs <- ch
	}
	close(jobs)
	wg.Wait()
	close(results)

	var out []*ChannelUnread
	for r := range results {
		if r.err != nil {
			s.log.Warn("unread fetch failed", "err", r.err)
			continue
		}
		if r.cu != nil && (len(r.cu.Messages) > 0 || len(r.cu.Replies) > 0) {
			out = append(out, r.cu)
		}
	}
	return out, nil
}

// RecentDMActivity returns DM and multi-party-DM conversations the
// user is part of, populated with messages from the last `hours`
// regardless of last_read. Unlike UnreadAll, it surfaces DMs the
// user has *already opened* — useful for end-of-day recaps of
// decisions made privately, where the operator is themselves a
// participant and so last_read has long since caught up.
//
// hours <= 0 is a programming error and returns nil, nil so the
// caller can no-op trivially. maxPerChannel caps history depth.
func (s *UnreadService) RecentDMActivity(ctx context.Context, hours, maxPerChannel int) ([]*ChannelUnread, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	if hours <= 0 {
		return nil, nil
	}
	if maxPerChannel <= 0 {
		maxPerChannel = 20
	}

	channels, err := s.JoinedChannels(ctx)
	if err != nil {
		return nil, err
	}

	// Slack timestamps are unix-seconds with usec fraction. Compose
	// the oldest cutoff as `<sec>.000000` so the API treats it
	// canonically.
	oldestSec := s.nowUnix() - int64(hours)*3600
	oldest := fmt.Sprintf("%d.000000", oldestSec)
	oldestFloat := float64(oldestSec)

	// Workers cap stays at 4 to match UnreadAll — same politeness budget.
	const workers = 4
	type result struct {
		cu  *ChannelUnread
		err error
	}
	jobs := make(chan goslack.Channel, len(channels))
	results := make(chan result, len(channels))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ch := range jobs {
				if !isDirectMessage(ch) {
					continue // non-DM channels handled by UnreadAll
				}
				cu, err := s.dmHistorySince(ctx, ch, oldest, oldestFloat, maxPerChannel)
				results <- result{cu, err}
			}
		}()
	}
	for _, ch := range channels {
		jobs <- ch
	}
	close(jobs)
	wg.Wait()
	close(results)

	var out []*ChannelUnread
	for r := range results {
		if r.err != nil {
			s.log.Warn("dm window fetch failed", "err", r.err)
			continue
		}
		if r.cu != nil && (len(r.cu.Messages) > 0 || len(r.cu.Replies) > 0) {
			out = append(out, r.cu)
		}
	}
	return out, nil
}

// dmHistorySince is the per-channel worker for RecentDMActivity. It
// pulls history newer than `oldest` and reuses fetchReplies so the
// thread-reply contract matches UnreadAll's output shape exactly.
func (s *UnreadService) dmHistorySince(ctx context.Context, ch goslack.Channel, oldest string, oldestFloat float64, maxMessages int) (*ChannelUnread, error) {
	params := &goslack.GetConversationHistoryParameters{
		ChannelID: ch.ID,
		Oldest:    oldest,
		Limit:     maxMessages,
		Inclusive: false,
	}
	var resp *goslack.GetConversationHistoryResponse
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		r, err := s.api.GetConversationHistoryContext(ctx, params)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.history (dm window): %w", err)
	}

	cu := &ChannelUnread{Channel: ch, LastRead: ch.LastRead}
	for _, msg := range resp.Messages {
		cu.Messages = append(cu.Messages, msg)
	}
	if err := s.fetchReplies(ctx, ch.ID, oldestFloat, cu.Messages, cu); err != nil {
		s.log.Warn("fetch thread replies failed", "channel", ch.ID, "err", err)
	}
	return cu, nil
}

// nowUnix is a seam for tests — overridden via a package-level var
// when deterministic time is needed.
var nowUnixFn = func() int64 { return time.Now().Unix() }

func (s *UnreadService) nowUnix() int64 { return nowUnixFn() }

// isDirectMessage decides whether a goslack.Channel represents a
// 1:1 DM or multi-party DM. Slack's `users.conversations` *should*
// populate IsIM / IsMpIM correctly for every member-side DM, but in
// practice the booleans go missing for channels whose state is
// stale on the listing side (typical with read-state-only-outgoing
// DMs). Falling back to the channel ID prefix preserves the
// invariant that any user-token DM is detected:
//
//   - `D…` — direct message (1:1)
//   - `G…` whose name starts with `mpdm-` — multi-party DM
//
// Plain `G…` channels with non-mpdm names are private group
// channels, not DMs, so they intentionally do NOT match.
func isDirectMessage(ch goslack.Channel) bool {
	return IsDirectMessage(ch)
}

// IsDirectMessage is the exported form of the DM detector, for callers
// outside this package (e.g. the digest ranker, which gives DMs a
// priority tier so they survive the max_chars cap ahead of log/git
// feeds). Detection logic — flags first, channel-ID prefix as the
// fallback for stale-listing DMs — is documented on the unexported
// wrapper above.
func IsDirectMessage(ch goslack.Channel) bool {
	if ch.IsIM || ch.IsMpIM {
		return true
	}
	if strings.HasPrefix(ch.ID, "D") {
		return true
	}
	if strings.HasPrefix(ch.ID, "G") && strings.HasPrefix(ch.Name, "mpdm-") {
		return true
	}
	return false
}

// UnreadThreadMentions catches mentions in thread replies whose parent
// is already read — `fetchReplies` only iterates new top-level messages,
// so a reply tagging the operator in an old thread never enters the
// unread sweep. This method backstops that gap using Slack's own
// `search.messages to:me` index, which DOES catch the reply.
//
// Returns one `*ChannelUnread` per affected channel with the mentioning
// search hits attached. Caller is responsible for merging into the
// regular `UnreadAll` result. `hours <= 0` is a no-op.
func (s *UnreadService) UnreadThreadMentions(ctx context.Context, hours int) ([]*ChannelUnread, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	if hours <= 0 || s.search == nil {
		return nil, nil
	}

	after := s.nowUnix() - int64(hours)*3600
	afterDate := time.Unix(after, 0).Format("2006-01-02")
	// Slack's `to:me` matches messages where the operator is the explicit
	// recipient — DMs to them, plus `<@SELFID>` mentions. That's exactly
	// the gap UnreadAll's reply-fetch can't see for old-thread replies.
	query := "to:me after:" + afterDate

	matches, err := s.search.Messages(ctx, query, 100)
	if err != nil {
		return nil, fmt.Errorf("search to:me: %w", err)
	}

	byChannel := make(map[string]*ChannelUnread)
	for _, m := range matches {
		if m.Channel.ID == "" {
			continue
		}
		// Filter to the actual time window — Slack's `after:` is
		// strictly date-granular, so messages from earlier in the
		// same day could leak in. Drop anything that predates our
		// hour-precise cutoff.
		ts, _ := strconv.ParseFloat(m.Timestamp, 64)
		if int64(ts) < after {
			continue
		}
		cu, ok := byChannel[m.Channel.ID]
		if !ok {
			cu = &ChannelUnread{}
			cu.Channel.ID = m.Channel.ID
			cu.Channel.Name = m.Channel.Name
			byChannel[m.Channel.ID] = cu
		}
		msg := searchHitToMessage(m)
		// Decide whether to attach as a top-level message or as a
		// reply under its parent's ts. Replies go under Replies[ts]
		// so ChannelMentions traverses them via the same loop it
		// uses for regular thread replies.
		if msg.ThreadTimestamp != "" && msg.ThreadTimestamp != msg.Timestamp {
			if cu.Replies == nil {
				cu.Replies = make(map[string][]goslack.Message)
			}
			cu.Replies[msg.ThreadTimestamp] = append(cu.Replies[msg.ThreadTimestamp], msg)
		} else {
			cu.Messages = append(cu.Messages, msg)
		}
	}

	out := make([]*ChannelUnread, 0, len(byChannel))
	for _, cu := range byChannel {
		out = append(out, cu)
	}
	return out, nil
}

// UnreadOwnThreads catches the OTHER blind spot UnreadThreadMentions
// doesn't: replies in a thread the operator STARTED or already replied
// in, where the new replies do NOT @-mention them. Slack auto-follows
// such threads (they show in the "Threads" view) but never marks the
// channel unread, and `fetchReplies` skips them because the parent is
// already read — so a colleague answering your own request is silently
// missed. `to:me` can't see it (no mention); this uses `from:me` to
// discover the threads you're active in, then fetches each thread and
// surfaces replies newer than your last message in it. `hours <= 0` (or
// no search backend) is a no-op.
func (s *UnreadService) UnreadOwnThreads(ctx context.Context, hours int) ([]*ChannelUnread, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	if hours <= 0 || s.search == nil {
		return nil, nil
	}
	selfID, err := s.Self(ctx)
	if err != nil || selfID == "" {
		// Without our own id we can't tell our messages from others', so
		// we can't compute "newer than mine". Degrade to no-op rather
		// than guess.
		return nil, err
	}

	after := s.nowUnix() - int64(hours)*3600
	afterDate := time.Unix(after, 0).Format("2006-01-02")
	matches, err := s.search.Messages(ctx, "from:me after:"+afterDate, 100)
	if err != nil {
		return nil, fmt.Errorf("search from:me: %w", err)
	}

	// Discover the unique (channel, thread-root) threads we're active in.
	// A hit that is a reply carries thread_ts (parsed from its permalink);
	// a hit that is a parent we authored has none, so the root is its ts.
	type threadKey struct{ channelID, root string }
	roots := make(map[threadKey]string) // key -> channel name
	for _, m := range matches {
		if m.Channel.ID == "" {
			continue
		}
		if ts, _ := strconv.ParseFloat(m.Timestamp, 64); int64(ts) < after {
			continue
		}
		hit := searchHitToMessage(m)
		root := hit.ThreadTimestamp
		if root == "" {
			root = hit.Timestamp
		}
		roots[threadKey{m.Channel.ID, root}] = m.Channel.Name
	}

	byChannel := make(map[string]*ChannelUnread)
	for k, chName := range roots {
		var thread []goslack.Message
		rerr := ratelimit.Do(ctx, s.log, 0, func() error {
			r, _, _, e := s.api.GetConversationRepliesContext(ctx, &goslack.GetConversationRepliesParameters{
				ChannelID: k.channelID,
				Timestamp: k.root,
				Limit:     100,
			})
			if e != nil {
				return e
			}
			thread = r
			return nil
		})
		if rerr != nil {
			// Best-effort: a single unreadable thread shouldn't sink the
			// backstop.
			s.log.Warn("own-thread replies fetch failed", "channel", k.channelID, "thread", k.root, "err", rerr)
			continue
		}
		unseen := unseenAfterMine(thread, selfID)
		if len(unseen) == 0 {
			continue
		}
		cu, ok := byChannel[k.channelID]
		if !ok {
			cu = &ChannelUnread{}
			cu.Channel.ID = k.channelID
			cu.Channel.Name = chName
			byChannel[k.channelID] = cu
		}
		if cu.Replies == nil {
			cu.Replies = make(map[string][]goslack.Message)
		}
		cu.Replies[k.root] = append(cu.Replies[k.root], unseen...)
	}

	out := make([]*ChannelUnread, 0, len(byChannel))
	for _, cu := range byChannel {
		out = append(out, cu)
	}
	return out, nil
}

// unseenAfterMine returns the messages in a thread that were authored by
// someone OTHER than selfID and posted after the operator's own most
// recent message in that thread — i.e. replies that arrived since the
// operator last participated. Returns nil when the operator has no
// message in the thread (they aren't actually a participant, so nothing
// is "theirs to have missed"). Pure, for unit testing without a live
// conversations.replies call.
func unseenAfterMine(thread []goslack.Message, selfID string) []goslack.Message {
	var myLatest float64
	for _, m := range thread {
		if m.User != selfID {
			continue
		}
		if ts, _ := strconv.ParseFloat(m.Timestamp, 64); ts > myLatest {
			myLatest = ts
		}
	}
	if myLatest == 0 {
		return nil
	}
	var out []goslack.Message
	for _, m := range thread {
		if m.User == selfID {
			continue
		}
		if ts, _ := strconv.ParseFloat(m.Timestamp, 64); ts > myLatest {
			out = append(out, m)
		}
	}
	return out
}

// searchHitToMessage adapts a SearchMessage (the shape returned by
// search.messages) to the Message shape the rest of the digest
// pipeline expects. We only fill the fields downstream renderers and
// mention-detection actually read.
func searchHitToMessage(h goslack.SearchMessage) goslack.Message {
	m := goslack.Message{}
	m.Msg.User = h.User
	m.Msg.Username = h.Username
	m.Msg.Text = h.Text
	m.Msg.Timestamp = h.Timestamp
	m.Msg.Permalink = h.Permalink
	// Slack permalinks for thread replies include `?thread_ts=...`;
	// parse it back to populate ThreadTimestamp so downstream code
	// (ChannelMentions, rendering) treats this hit as a reply.
	if i := indexThreadTS(h.Permalink); i >= 0 {
		rest := h.Permalink[i+len("thread_ts="):]
		if amp := indexAmp(rest); amp >= 0 {
			m.Msg.ThreadTimestamp = rest[:amp]
		} else {
			m.Msg.ThreadTimestamp = rest
		}
	}
	return m
}

func indexThreadTS(s string) int {
	for i := 0; i+10 <= len(s); i++ {
		if s[i:i+10] == "thread_ts=" {
			return i
		}
	}
	return -1
}

func indexAmp(s string) int {
	for i, r := range s {
		if r == '&' {
			return i
		}
	}
	return -1
}

// MarkRead marks the channel as read up to the given timestamp.
func (s *UnreadService) MarkRead(ctx context.Context, channelID, ts string) error {
	if !s.Enabled() {
		return ErrNoUserToken
	}
	return ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.MarkConversationContext(ctx, channelID, ts)
	})
}

// ParticipationChannels returns the distinct channels and DMs the
// operator actually posted in during the window, newest activity first.
//
// This is the selection primitive for an export: "channels I work in" is
// not "channels I joined" (which includes dozens of read-only feeds) and
// not "channels with unread" (which is about what arrived, not about
// where the operator acts). A `from:me` search answers it directly.
func (s *UnreadService) ParticipationChannels(ctx context.Context, hours int) ([]goslack.Channel, error) {
	if !s.Enabled() {
		return nil, ErrNoUserToken
	}
	if hours <= 0 || s.search == nil {
		return nil, nil
	}
	after := s.nowUnix() - int64(hours)*3600
	afterDate := time.Unix(after, 0).Format("2006-01-02")
	matches, err := s.search.Messages(ctx, "from:me after:"+afterDate, 200)
	if err != nil {
		return nil, fmt.Errorf("search from:me: %w", err)
	}

	seen := make(map[string]struct{}, len(matches))
	var out []goslack.Channel
	for _, m := range matches {
		if m.Channel.ID == "" {
			continue
		}
		// search's `after:` is day-granular, so re-filter to the exact
		// window the caller asked for.
		if ts, _ := strconv.ParseFloat(m.Timestamp, 64); int64(ts) < after {
			continue
		}
		if _, dup := seen[m.Channel.ID]; dup {
			continue
		}
		seen[m.Channel.ID] = struct{}{}
		ch := goslack.Channel{}
		ch.ID = m.Channel.ID
		ch.Name = m.Channel.Name
		out = append(out, ch)
	}
	return out, nil
}
