package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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

	selfMu sync.RWMutex
	selfID string
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
	s.selfMu.Unlock()
	return resp.UserID, nil
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
	params := &goslack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Oldest:    info.LastRead,
		Limit:     maxMessages,
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

	if err := s.fetchReplies(ctx, channelID, oldest, cu); err != nil {
		// Replies are best-effort context; a failure here should not
		// block the rest of the digest. Log and continue.
		s.log.Warn("fetch thread replies failed", "channel", channelID, "err", err)
	}
	return cu, nil
}

// fetchReplies populates cu.Replies for every top-level message in
// cu.Messages that is itself a thread parent with reply_count > 0.
// Only replies strictly newer than oldest (the channel's last_read)
// are kept.
func (s *UnreadService) fetchReplies(ctx context.Context, channelID string, oldest float64, cu *ChannelUnread) error {
	for _, m := range cu.Messages {
		// A thread parent has thread_ts == ts and reply_count > 0.
		// A reply has thread_ts != ts; replies do not appear in
		// conversations.history so we never see them at this layer.
		if m.ThreadTimestamp == "" || m.ThreadTimestamp != m.Timestamp || m.ReplyCount == 0 {
			continue
		}

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
				if !ch.IsIM && !ch.IsMpIM {
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
	if err := s.fetchReplies(ctx, ch.ID, oldestFloat, cu); err != nil {
		s.log.Warn("fetch thread replies failed", "channel", ch.ID, "err", err)
	}
	return cu, nil
}

// nowUnix is a seam for tests — overridden via a package-level var
// when deterministic time is needed.
var nowUnixFn = func() int64 { return time.Now().Unix() }

func (s *UnreadService) nowUnix() int64 { return nowUnixFn() }

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
