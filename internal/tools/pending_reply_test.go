package tools

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// fakeMsgClient is a minimal MessageClient stand-in scoped to a single
// channel: callers set History/Replies fixtures and operatorRepliedSince
// runs against the in-memory data — no goslack, no HTTP.
//
// Errors are opt-in (set ErrHistory / ErrReplies) so the same fake
// drives both happy-path and partial-failure tests.
type fakeMsgClient struct {
	History       []goslack.Message
	Replies       map[string][]goslack.Message
	ErrHistory    error
	ErrReplies    error
	HistoryCalls  int
	RepliesCalls  int
	WantThreadTS  string
	WantChannelID string
}

func (f *fakeMsgClient) historyCall(_ context.Context, p slack.HistoryParams) ([]goslack.Message, error) {
	f.HistoryCalls++
	if f.WantChannelID != "" && p.ChannelID != f.WantChannelID {
		return nil, errors.New("unexpected channel in History call")
	}
	if f.ErrHistory != nil {
		return nil, f.ErrHistory
	}
	return f.History, nil
}

func (f *fakeMsgClient) repliesCall(_ context.Context, channelID, threadTS string) ([]goslack.Message, error) {
	f.RepliesCalls++
	if f.WantChannelID != "" && channelID != f.WantChannelID {
		return nil, errors.New("unexpected channel in ThreadReplies call")
	}
	if f.WantThreadTS != "" && threadTS != f.WantThreadTS {
		return nil, errors.New("unexpected thread_ts in ThreadReplies call")
	}
	if f.ErrReplies != nil {
		return nil, f.ErrReplies
	}
	if f.Replies == nil {
		return nil, nil
	}
	return f.Replies[threadTS], nil
}

func (f *fakeMsgClient) postCall(_ context.Context, _, _, _ string) (string, error) {
	return "", errors.New("Post not supported in fake")
}

func (f *fakeMsgClient) addReactionCall(_ context.Context, _, _, _ string) error {
	return errors.New("AddReaction not supported in fake")
}

// MessageClient adapter — keeps the fake's fields exported and named
// for clarity instead of inlining the contract.
type fakeMessageClient struct{ inner *fakeMsgClient }

func (f *fakeMessageClient) History(ctx context.Context, p slack.HistoryParams) ([]goslack.Message, error) {
	return f.inner.historyCall(ctx, p)
}
func (f *fakeMessageClient) ThreadReplies(ctx context.Context, channelID, threadTS string) ([]goslack.Message, error) {
	return f.inner.repliesCall(ctx, channelID, threadTS)
}
func (f *fakeMessageClient) Post(ctx context.Context, channelID, text, threadTS string) (string, error) {
	return f.inner.postCall(ctx, channelID, text, threadTS)
}
func (f *fakeMessageClient) AddReaction(ctx context.Context, channelID, timestamp, emoji string) error {
	return f.inner.addReactionCall(ctx, channelID, timestamp, emoji)
}
func (f *fakeMessageClient) Delete(ctx context.Context, channelID, timestamp string) error {
	return nil
}
func (f *fakeMessageClient) MessageAt(ctx context.Context, channelID, ts string) (*goslack.Message, error) {
	return nil, errors.New("MessageAt not supported in fake")
}
func (f *fakeMessageClient) DownloadFile(ctx context.Context, downloadURL string, w io.Writer) error {
	return errors.New("DownloadFile not supported in fake")
}
func (f *fakeMessageClient) FileInfo(ctx context.Context, fileID string) (goslack.File, error) {
	return goslack.File{}, errors.New("FileInfo not supported in fake")
}
func (f *fakeMessageClient) LatestFileMessage(ctx context.Context, channelID string, accept func(goslack.File) bool, fromUserID string) (*goslack.Message, error) {
	return nil, errors.New("LatestFileMessage not supported in fake")
}

var _ MessageClient = (*fakeMessageClient)(nil)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// permalink builds a synthetic Slack permalink for fixtures. When
// threadTS == "" we omit the thread_ts query parameter — matches the
// shape Slack emits for a top-level message that has no replies yet.
func permalink(channelID, ts, threadTS string) string {
	base := "https://example.slack.com/archives/" + channelID + "/p" + ts
	if threadTS == "" {
		return base
	}
	return base + "?thread_ts=" + threadTS
}

// mention wires up the SearchMessage fields the under-test code reads.
func mention(channelID, ts, threadTSinPermalink, text string) goslack.SearchMessage {
	m := goslack.SearchMessage{}
	m.Channel.ID = channelID
	m.Timestamp = ts
	m.Text = text
	m.Permalink = permalink(channelID, ts, threadTSinPermalink)
	return m
}

// TestOperatorRepliedSince_TopLevelReply guards the pre-existing
// happy path: peer pings at top level, operator replies at top level,
// conversations.history sees both.
func TestOperatorRepliedSince_TopLevelReply(t *testing.T) {
	const ch = "D_TEST"
	fake := &fakeMsgClient{
		WantChannelID: ch,
		History: []goslack.Message{
			mkMsg("100.002", "U_SELF", "ack"),
		},
	}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention(ch, "100.001", "", "ping"), "U_SELF")
	if !got {
		t.Fatalf("expected NOT pending (top-level reply present)")
	}
	if fake.RepliesCalls != 0 {
		t.Fatalf("history reply should short-circuit; got %d ThreadReplies calls", fake.RepliesCalls)
	}
}

// TestOperatorRepliedSince_ThreadReplyClosesMention covers the
// regression we are fixing: the mention is itself a thread reply
// (permalink carries thread_ts), and the operator replied later in
// the same thread. conversations.history will not surface the
// in-thread reply; conversations.replies must.
func TestOperatorRepliedSince_ThreadReplyClosesMention(t *testing.T) {
	const ch, root = "D_TEST", "100.000"
	fake := &fakeMsgClient{
		WantChannelID: ch,
		WantThreadTS:  root,
		History:       nil, // history sees nothing past the mention
		Replies: map[string][]goslack.Message{
			root: {
				mkMsg(root, "U_PEER", "thread root"),
				mkMsg("100.001", "U_PEER", "ping <@U_SELF>"),
				mkMsg("100.002", "U_SELF", "yes, on it"),
			},
		},
	}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention(ch, "100.001", root, "ping <@U_SELF>"), "U_SELF")
	if !got {
		t.Fatalf("expected NOT pending: operator reply lives in the same thread (got pending)")
	}
	if fake.RepliesCalls != 1 {
		t.Fatalf("expected exactly 1 ThreadReplies call; got %d", fake.RepliesCalls)
	}
}

// TestOperatorRepliedSince_TopLevelMentionRepliedInThread covers a
// neighbouring case: the mention is top-level, operator reacted by
// opening a thread on it. Without the thread-aware second pass we
// would miss the reply entirely.
func TestOperatorRepliedSince_TopLevelMentionRepliedInThread(t *testing.T) {
	const ch, mentionTS = "C_TEST", "200.001"
	fake := &fakeMsgClient{
		WantChannelID: ch,
		WantThreadTS:  mentionTS,
		History:       nil,
		Replies: map[string][]goslack.Message{
			mentionTS: {
				mkMsg(mentionTS, "U_PEER", "ping <@U_SELF>"),
				mkMsg("200.002", "U_SELF", "noted"),
			},
		},
	}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention(ch, mentionTS, "", "ping <@U_SELF>"), "U_SELF")
	if !got {
		t.Fatalf("expected NOT pending: operator opened a thread under the mention")
	}
}

// TestOperatorRepliedSince_NoReplyAnywhere is the canonical positive
// case — the mention is truly pending. Both history and replies are
// empty (or only contain the peer's messages).
func TestOperatorRepliedSince_NoReplyAnywhere(t *testing.T) {
	const ch, root = "D_TEST", "300.000"
	fake := &fakeMsgClient{
		WantChannelID: ch,
		WantThreadTS:  root,
		Replies: map[string][]goslack.Message{
			root: {
				mkMsg(root, "U_PEER", "root"),
				mkMsg("300.001", "U_PEER", "ping <@U_SELF>"),
			},
		},
	}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention(ch, "300.001", root, "ping <@U_SELF>"), "U_SELF")
	if got {
		t.Fatalf("expected pending: no operator reply exists")
	}
}

// TestOperatorRepliedSince_OlderReplyDoesNotCount guards against a
// timestamp-ordering regression: replies older than the mention are
// not credit toward "answered".
func TestOperatorRepliedSince_OlderReplyDoesNotCount(t *testing.T) {
	const ch, root = "D_TEST", "400.000"
	fake := &fakeMsgClient{
		WantChannelID: ch,
		WantThreadTS:  root,
		Replies: map[string][]goslack.Message{
			root: {
				mkMsg(root, "U_PEER", "root"),
				mkMsg("400.001", "U_SELF", "earlier note"),
				mkMsg("400.002", "U_PEER", "ping <@U_SELF>"),
			},
		},
	}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention(ch, "400.002", root, "ping <@U_SELF>"), "U_SELF")
	if got {
		t.Fatalf("expected pending: only older operator messages exist")
	}
}

// TestOperatorRepliedSince_EmptyReplyDoesNotCount documents the rule
// inherited from the original implementation — a text-empty reply
// (reactions, file-only) does not close a mention.
func TestOperatorRepliedSince_EmptyReplyDoesNotCount(t *testing.T) {
	const ch, root = "D_TEST", "500.000"
	fake := &fakeMsgClient{
		WantChannelID: ch,
		WantThreadTS:  root,
		Replies: map[string][]goslack.Message{
			root: {
				mkMsg(root, "U_PEER", "root"),
				mkMsg("500.001", "U_PEER", "ping <@U_SELF>"),
				mkMsg("500.002", "U_SELF", "   "),
			},
		},
	}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention(ch, "500.001", root, "ping <@U_SELF>"), "U_SELF")
	if got {
		t.Fatalf("expected pending: operator's only follow-up is whitespace-only")
	}
}

// TestOperatorRepliedSince_HistoryErrorFallsThroughToThreads documents
// resilience: if conversations.history fails (rate limit, transient
// error) the thread sweep still has a chance to close the mention.
func TestOperatorRepliedSince_HistoryErrorFallsThroughToThreads(t *testing.T) {
	const ch, root = "D_TEST", "600.000"
	fake := &fakeMsgClient{
		WantChannelID: ch,
		WantThreadTS:  root,
		ErrHistory:    errors.New("simulated history failure"),
		Replies: map[string][]goslack.Message{
			root: {
				mkMsg(root, "U_PEER", "root"),
				mkMsg("600.001", "U_PEER", "ping <@U_SELF>"),
				mkMsg("600.002", "U_SELF", "got it"),
			},
		},
	}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention(ch, "600.001", root, "ping <@U_SELF>"), "U_SELF")
	if !got {
		t.Fatalf("expected NOT pending: thread sweep should still catch the reply")
	}
}

// TestOperatorRepliedSince_EmptySelfBails is a guard for malformed
// upstream input — auth.test should never return an empty self id,
// but if it ever did, we must not crash and must not falsely close
// arbitrary mentions.
func TestOperatorRepliedSince_EmptySelfBails(t *testing.T) {
	fake := &fakeMsgClient{}
	got := operatorRepliedSince(context.Background(), &fakeMessageClient{fake}, quietLog(),
		mention("D_TEST", "700.001", "", "x"), "")
	if got {
		t.Fatalf("empty selfID must report pending (false), not closed (true)")
	}
	if fake.HistoryCalls != 0 || fake.RepliesCalls != 0 {
		t.Fatalf("empty selfID should be an early no-op: history=%d replies=%d",
			fake.HistoryCalls, fake.RepliesCalls)
	}
}
