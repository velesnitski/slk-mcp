package slack

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack/ratelimit"
)

// MessageService reads channel history, thread replies, and posts messages.
type MessageService struct {
	api      *goslack.Client
	channels *ChannelService
	users    *UserService
	log      *slog.Logger
}

func newMessageService(api *goslack.Client, channels *ChannelService, users *UserService, log *slog.Logger) *MessageService {
	return &MessageService{api: api, channels: channels, users: users, log: log}
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
	return nil, fmt.Errorf("no recent message with a matching attachment in this conversation")
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
func (s *MessageService) DownloadFile(ctx context.Context, downloadURL string, w io.Writer) error {
	err := ratelimit.Do(ctx, s.log, 0, func() error {
		return s.api.GetFileContext(ctx, downloadURL, w)
	})
	if err != nil {
		return fmt.Errorf("file download: %w", err)
	}
	return nil
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
