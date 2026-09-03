package slack

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// MessageService reads channel history, thread replies, and posts messages.
type MessageService struct {
	api      *goslack.Client
	fallback *goslack.Client // user client, when it differs from api
	channels *ChannelService
	users    *UserService
	log      *slog.Logger
}

func newMessageService(api, fallback *goslack.Client, channels *ChannelService, users *UserService, log *slog.Logger) *MessageService {
	if fallback == api {
		fallback = nil
	}
	return &MessageService{api: api, fallback: fallback, channels: channels, users: users, log: log}
}

// HistoryParams controls a conversations.history fetch.
type HistoryParams struct {
	ChannelID string
	OldestTS  float64 // unix seconds; 0 means no lower bound
	LatestTS  float64 // unix seconds; 0 means no upper bound
	Limit     int
}

// History returns messages between OldestTS and LatestTS, up to Limit,
// newest first.
func (s *MessageService) History(ctx context.Context, p HistoryParams) ([]goslack.Message, error) {
	if p.Limit <= 0 {
		p.Limit = 200
	}
	params := &goslack.GetConversationHistoryParameters{
		ChannelID: p.ChannelID,
		Limit:     p.Limit,
	}
	if p.OldestTS > 0 {
		params.Oldest = strconv.FormatFloat(p.OldestTS, 'f', 6, 64)
	}
	if p.LatestTS > 0 {
		params.Latest = strconv.FormatFloat(p.LatestTS, 'f', 6, 64)
		params.Inclusive = true
	}

	resp, err := ratelimit.DoR(ctx, s.log, func() (*goslack.GetConversationHistoryResponse, error) {
		return s.api.GetConversationHistoryContext(ctx, params)
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.history: %w", err)
	}
	return resp.Messages, nil
}

// ThreadReplies returns all messages in a thread rooted at threadTS.
func (s *MessageService) ThreadReplies(ctx context.Context, channelID, threadTS string) ([]goslack.Message, error) {
	params := &goslack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     200,
	}
	msgs, err := ratelimit.DoR(ctx, s.log, func() ([]goslack.Message, error) {
		m, _, _, err := s.api.GetConversationRepliesContext(ctx, params)
		return m, err
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.replies: %w", err)
	}
	return msgs, nil
}

// Post sends a message to channelID. Pass threadTS to reply in a thread.
// Returns the posted message timestamp.
func (s *MessageService) Post(ctx context.Context, channelID, text, threadTS string) (string, error) {
	opts := []goslack.MsgOption{goslack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, goslack.MsgOptionTS(threadTS))
	}

	ts, err := ratelimit.DoR(ctx, s.log, func() (string, error) {
		_, t, err := s.api.PostMessageContext(ctx, channelID, opts...)
		return t, err
	})
	if err != nil {
		return "", fmt.Errorf("chat.postMessage: %w", err)
	}
	return ts, nil
}

// Delete removes a message via chat.delete. With a user token Slack only
// permits deleting a message the token's own user authored (otherwise it
// returns cant_delete_message); with a bot token, only the bot's own
// messages. That server-side ownership check is the safety boundary — we
// don't second-guess it client-side.
func (s *MessageService) Delete(ctx context.Context, channelID, timestamp string) error {
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		_, _, derr := s.api.DeleteMessageContext(ctx, channelID, timestamp)
		return derr
	})
	if err != nil {
		return fmt.Errorf("chat.delete: %w", err)
	}
	return nil
}

// MessageAt returns the single message posted at ts in channelID. A
// point lookup via conversations.history (inclusive latest, limit 1)
// covers top-level messages; thread replies never appear in channel
// history, so on a ts mismatch we fall back to conversations.replies
// rooted at ts and scan for the exact match.
func (s *MessageService) MessageAt(ctx context.Context, channelID, ts string) (*goslack.Message, error) {
	params := &goslack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Latest:    ts,
		Inclusive: true,
		Limit:     1,
	}
	resp, err := ratelimit.DoR(ctx, s.log, func() (*goslack.GetConversationHistoryResponse, error) {
		return s.api.GetConversationHistoryContext(ctx, params)
	})
	if err != nil {
		return nil, fmt.Errorf("conversations.history: %w", err)
	}
	if len(resp.Messages) > 0 && resp.Messages[0].Timestamp == ts {
		return &resp.Messages[0], nil
	}
	replies, err := s.ThreadReplies(ctx, channelID, ts)
	if err != nil {
		return nil, fmt.Errorf("no message at ts %s (and thread lookup failed: %w)", ts, err)
	}
	for i := range replies {
		if replies[i].Timestamp == ts {
			return &replies[i], nil
		}
	}
	return nil, fmt.Errorf("no message found at ts %s", ts)
}

// LatestFileMessage returns the most recent message in channelID whose
// attachments satisfy accept — the "grab my last voice note in this DM"
// engine. History returns newest-first, so the first qualifying message
// is the latest. fromUserID, when non-empty, restricts to that author
// (so "my" voice memo wins over the other party's newer one). Returns
// an error when nothing in the recent window qualifies.
func (s *MessageService) LatestFileMessage(ctx context.Context, channelID string, accept func(goslack.File) bool, fromUserID string) (*goslack.Message, error) {
	msgs, err := s.History(ctx, HistoryParams{ChannelID: channelID, Limit: 60})
	if err != nil {
		return nil, err
	}
	if m := selectLatestFileMessage(msgs, accept, fromUserID); m != nil {
		return m, nil
	}

	// Nothing at the top level. Voice notes are routinely posted as THREAD
	// REPLIES, and conversations.history returns top-level messages only,
	// so the note is invisible to the scan above and latest-mode reports
	// "no recent message" while the file plainly sits in the conversation.
	// Walk the recent threads and keep the newest qualifying reply — a
	// match in an older thread can still be newer than one in a younger
	// thread, so compare timestamps rather than taking the first hit.
	var best *goslack.Message
	for _, root := range threadRoots(msgs, threadScanLimit) {
		replies, rerr := s.ThreadReplies(ctx, channelID, root)
		if rerr != nil {
			continue
		}
		if m := selectLastFileMessage(replies, accept, fromUserID); m != nil {
			if best == nil || tsLess(best.Timestamp, m.Timestamp) {
				best = m
			}
		}
	}
	if best != nil {
		return best, nil
	}
	return nil, fmt.Errorf("no recent message with a matching attachment in this conversation")
}

// RecentFileMessages returns up to limit messages carrying an accepted
// attachment, newest first. LatestFileMessage answers "the newest one",
// which is the wrong question when a caller needs the file from the
// message *before* it — two documents posted seconds apart made the
// earlier one unreachable without hunting for its timestamp by hand.
// Both the top level and recent threads are scanned, so a document
// posted as a reply is a first-class candidate. fromUserID, when
// non-empty, restricts to that author.
func (s *MessageService) RecentFileMessages(ctx context.Context, channelID string, accept func(goslack.File) bool, fromUserID string, limit int) ([]goslack.Message, error) {
	msgs, err := s.History(ctx, HistoryParams{ChannelID: channelID, Limit: 60})
	if err != nil {
		return nil, err
	}
	out := matchingFileMessages(msgs, accept, fromUserID)
	for _, root := range threadRoots(msgs, threadScanLimit) {
		replies, rerr := s.ThreadReplies(ctx, channelID, root)
		if rerr != nil {
			continue
		}
		out = append(out, matchingFileMessages(replies, accept, fromUserID)...)
	}
	out = sortedUniqueByTS(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no recent message with a matching attachment in this conversation")
	}
	return out, nil
}

// matchingFileMessages keeps the messages that carry an accepted
// attachment, preserving input order and honouring the author filter.
// Pure.
func matchingFileMessages(msgs []goslack.Message, accept func(goslack.File) bool, fromUserID string) []goslack.Message {
	var out []goslack.Message
	for i := range msgs {
		m := msgs[i]
		if fromUserID != "" && m.User != fromUserID {
			continue
		}
		for _, f := range m.Files {
			if accept(f) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// sortedUniqueByTS drops duplicate timestamps (conversations.replies
// repeats the thread parent, which history already returned) and orders
// newest first. Pure.
func sortedUniqueByTS(msgs []goslack.Message) []goslack.Message {
	seen := make(map[string]bool, len(msgs))
	var out []goslack.Message
	for _, m := range msgs {
		if seen[m.Timestamp] {
			continue
		}
		seen[m.Timestamp] = true
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return tsLess(out[j].Timestamp, out[i].Timestamp)
	})
	return out
}

// threadScanLimit caps how many threads latest-mode opens when the top
// level carries no match — one conversations.replies call each, on a
// fallback path only.
const threadScanLimit = 12

// threadRoots lists the thread-root timestamps present in msgs,
// newest-first and de-duplicated, capped at limit (0 = uncapped). A root
// that has replies reports ThreadTimestamp == its own ts; a broadcast
// reply reports its parent's. Messages that belong to no thread are
// skipped. Pure.
func threadRoots(msgs []goslack.Message, limit int) []string {
	var roots []string
	seen := make(map[string]bool)
	for i := range msgs {
		m := &msgs[i]
		ts := m.ThreadTimestamp
		if ts == "" && m.ReplyCount > 0 {
			ts = m.Timestamp
		}
		if ts == "" || seen[ts] {
			continue
		}
		seen[ts] = true
		roots = append(roots, ts)
		if limit > 0 && len(roots) >= limit {
			break
		}
	}
	return roots
}

// tsLess reports whether Slack timestamp a is older than b. Unparseable
// values sort as oldest so a malformed ts can never win a comparison.
// Pure.
func tsLess(a, b string) bool {
	af, aerr := strconv.ParseFloat(a, 64)
	bf, berr := strconv.ParseFloat(b, 64)
	if berr != nil {
		return false
	}
	if aerr != nil {
		return true
	}
	return af < bf
}

// selectLatestFileMessage returns the first message in msgs (which
// conversations.history delivers newest-first) that carries an accepted
// attachment and, when fromUserID is set, was authored by that user.
// Split out from LatestFileMessage so the selection rule is unit-tested
// without a live history fetch. Returns nil when nothing qualifies.
func selectLatestFileMessage(msgs []goslack.Message, accept func(goslack.File) bool, fromUserID string) *goslack.Message {
	for i := range msgs {
		m := &msgs[i]
		if fromUserID != "" && m.User != fromUserID {
			continue
		}
		for _, f := range m.Files {
			if accept(f) {
				return m
			}
		}
	}
	return nil
}

// LatestFileInThread returns the most recent message in the thread rooted
// at threadTS whose attachments satisfy accept — the "read the voice note
// posted as a reply" engine, for when the caller only holds a permalink
// to the thread root (or a sibling) rather than the reply itself.
// conversations.replies returns the parent followed by replies
// oldest-first, so the LAST qualifying message is the newest. Returns an
// error when nothing in the thread qualifies.
func (s *MessageService) LatestFileInThread(ctx context.Context, channelID, threadTS string, accept func(goslack.File) bool) (*goslack.Message, error) {
	replies, err := s.ThreadReplies(ctx, channelID, threadTS)
	if err != nil {
		return nil, err
	}
	if m := selectLastFileMessage(replies, accept, ""); m != nil {
		return m, nil
	}
	return nil, fmt.Errorf("no message in this thread has a matching attachment")
}

// selectLastFileMessage returns the LAST message in msgs that carries an
// accepted attachment — msgs is chronological oldest-first (as
// conversations.replies delivers), so the last match is the newest.
// fromUserID, when non-empty, restricts to that author, mirroring
// selectLatestFileMessage so a thread scan honours the same `from`
// filter as the top-level one. Split out from LatestFileInThread for
// unit testing without a live fetch. Returns nil when nothing qualifies.
func selectLastFileMessage(msgs []goslack.Message, accept func(goslack.File) bool, fromUserID string) *goslack.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if fromUserID != "" && msgs[i].User != fromUserID {
			continue
		}
		for _, f := range msgs[i].Files {
			if accept(f) {
				return &msgs[i]
			}
		}
	}
	return nil
}

// FileInfo resolves a Slack file by its ID (files.info), returning the
// file object with its private download URL and mimetype — the direct
// path for a /files/<user>/<F…> URL that needs no message lookup.
func (s *MessageService) FileInfo(ctx context.Context, fileID string) (goslack.File, error) {
	f, err := ratelimit.DoR(ctx, s.log, func() (*goslack.File, error) {
		file, _, _, ferr := s.api.GetFileInfoContext(ctx, fileID, 0, 0)
		return file, ferr
	})
	if err != nil {
		return goslack.File{}, fmt.Errorf("files.info: %w", err)
	}
	if f == nil {
		return goslack.File{}, fmt.Errorf("files.info: no file for %s", fileID)
	}
	return *f, nil
}

// DownloadFile streams a url_private / url_private_download asset into
// w, authenticating with this client's token. The token never leaves
// the server process — callers receive bytes, not credentials. The
// caller owns w (creation and close).
//
// Falls back to the USER token when the primary is refused. A bot is a
// member of the channels it was invited to; the operator is a member of
// everything they can see. Files that matter most — HR material, board
// documents, anything in a private channel or group DM — live exactly
// where the bot was never invited, so a bot-only download reports "401
// Unauthorized" for a file the operator is looking at in their client.
// The fallback only fires on an auth refusal, so a genuinely missing
// file still fails as missing rather than being retried pointlessly.
func (s *MessageService) DownloadFile(ctx context.Context, downloadURL string, w io.Writer) error {
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.GetFileContext(ctx, downloadURL, w)
	})
	if err == nil {
		return nil
	}
	if s.fallback == nil || !isAuthRefusal(err) {
		return fmt.Errorf("file download: %w", err)
	}
	// w may hold a partial body from the refused attempt; the caller
	// owns it, so signal rather than silently appending to it.
	if t, ok := w.(interface{ Truncate(int64) error }); ok {
		if terr := t.Truncate(0); terr != nil {
			return fmt.Errorf("file download: %w (user-token retry needed but the sink could not be reset: %v)", err, terr)
		}
		if sk, ok := w.(io.Seeker); ok {
			if _, serr := sk.Seek(0, io.SeekStart); serr != nil {
				return fmt.Errorf("file download: %w (user-token retry needed but the sink could not be rewound: %v)", err, serr)
			}
		}
	}
	s.log.Debug("file download refused for the primary token, retrying with the user token")
	ferr := ratelimit.Do(ctx, s.log, 0, func() error {
		return s.fallback.GetFileContext(ctx, downloadURL, w)
	})
	if ferr != nil {
		return fmt.Errorf("file download: %w (user token also refused: %v)", err, ferr)
	}
	return nil
}

// isAuthRefusal reports whether an error is Slack refusing the token,
// as opposed to the file being absent or the network failing. Slack
// answers a file request from a non-member with a bare HTTP status, so
// this matches on the status text rather than a typed error. Pure.
func isAuthRefusal(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"401", "403", "unauthorized", "forbidden",
		"not_allowed_token_type", "missing_scope", "access_denied",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// AddReaction attaches an emoji reaction to a message.
func (s *MessageService) AddReaction(ctx context.Context, channelID, timestamp, emoji string) error {
	return ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.AddReactionContext(ctx, emoji, goslack.ItemRef{
			Channel:   channelID,
			Timestamp: timestamp,
		})
	})
}
