package tools

import (
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

func mkChannel(id string, isIM, isMpIM bool) *slack.ChannelUnread {
	cu := &slack.ChannelUnread{}
	cu.Channel.ID = id
	cu.Channel.IsIM = isIM
	cu.Channel.IsMpIM = isMpIM
	return cu
}

func TestMergeDMOverride_empty(t *testing.T) {
	base := []*slack.ChannelUnread{mkChannel("C1", false, false)}
	got := mergeDMOverride(base, nil)
	if len(got) != 1 || got[0].Channel.ID != "C1" {
		t.Fatalf("empty override should preserve base; got %v", got)
	}
}

func TestMergeDMOverride_replacesDMInBase(t *testing.T) {
	// Existing DM in base (unread fetch) is replaced by the
	// time-window version (which has fresher / fuller content).
	baseDM := mkChannel("D1", true, false)
	baseDM.Messages = []goslack.Message{{Msg: goslack.Msg{Text: "stale", Timestamp: "1.0"}}}
	override := mkChannel("D1", true, false)
	override.Messages = []goslack.Message{
		{Msg: goslack.Msg{Text: "fresh-a", Timestamp: "1.0"}},
		{Msg: goslack.Msg{Text: "fresh-b", Timestamp: "2.0"}},
	}
	got := mergeDMOverride([]*slack.ChannelUnread{baseDM}, []*slack.ChannelUnread{override})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry; got %d", len(got))
	}
	if len(got[0].Messages) != 2 || got[0].Messages[0].Text != "fresh-a" {
		t.Fatalf("expected override replacement; got %+v", got[0])
	}
}

func TestMergeDMOverride_replacesEvenWhenBaseIsIMNotSet(t *testing.T) {
	// The silent-miss bug we shipped in v0.4.7 (fixed in v0.4.12):
	// users.conversations sometimes returns DM channels with
	// IsIM=false (especially for outgoing-only DMs the operator
	// already marked read). Before the fix, the merge over-defensively
	// required base.IsIM/IsMpIM to be true before replacing — so
	// override was silently ignored and the truncated unread-only
	// view persisted. Trust the override side: it already filtered
	// to DMs via the new isDirectMessage helper.
	baseStale := mkChannel("D_RUSLAN", false, false) // IsIM not set!
	baseStale.Messages = []goslack.Message{
		{Msg: goslack.Msg{Text: "stale incoming", Timestamp: "1.0"}},
	}
	override := mkChannel("D_RUSLAN", true, false)
	override.Messages = []goslack.Message{
		{Msg: goslack.Msg{Text: "stale incoming", Timestamp: "1.0"}},
		{Msg: goslack.Msg{Text: "fresh outgoing reply", Timestamp: "2.0"}},
	}
	got := mergeDMOverride([]*slack.ChannelUnread{baseStale}, []*slack.ChannelUnread{override})
	if len(got) != 1 {
		t.Fatalf("expected 1 entry after merge; got %d", len(got))
	}
	if len(got[0].Messages) != 2 {
		t.Fatalf("expected merge to surface override's 2 msgs (incl outgoing); got %d", len(got[0].Messages))
	}
	if got[0].Messages[1].Text != "fresh outgoing reply" {
		t.Fatalf("expected fresh outgoing reply in merge; got %q", got[0].Messages[1].Text)
	}
}

func TestMergeDMOverride_appendsNewDMs(t *testing.T) {
	// A DM that wasn't in base (because it was already read) but is
	// in the override is appended — that's the whole point of the
	// feature.
	baseRegular := mkChannel("C1", false, false)
	overrideNewDM := mkChannel("D2", true, false)
	overrideNewDM.Messages = []goslack.Message{
		{Msg: goslack.Msg{Text: "private decision", Timestamp: "3.0"}},
	}
	got := mergeDMOverride(
		[]*slack.ChannelUnread{baseRegular},
		[]*slack.ChannelUnread{overrideNewDM},
	)
	if len(got) != 2 {
		t.Fatalf("expected base + appended DM; got %d", len(got))
	}
	if got[1].Channel.ID != "D2" {
		t.Fatalf("expected D2 appended last; got %s", got[1].Channel.ID)
	}
}

func TestMergeDMOverride_skipsNilAndEmptyIDs(t *testing.T) {
	base := []*slack.ChannelUnread{nil, mkChannel("", true, false), mkChannel("C1", false, false)}
	override := []*slack.ChannelUnread{nil, mkChannel("", true, false)}
	got := mergeDMOverride(base, override)
	// nil and empty-ID entries are skipped on the base side; merge
	// must not panic.
	if len(got) == 0 {
		t.Fatalf("expected at least the valid C1 entry; got %v", got)
	}
	for _, e := range got {
		if e == nil {
			t.Fatal("nil entries must not survive merge")
		}
	}
}

func TestMergeDMOverride_mpdmAlsoReplaced(t *testing.T) {
	// Multi-party DMs (IsMpIM) trigger the same replacement path as
	// 1:1 DMs.
	baseMpDM := mkChannel("G_MPDM", false, true)
	baseMpDM.Messages = []goslack.Message{{Msg: goslack.Msg{Text: "old", Timestamp: "1.0"}}}
	override := mkChannel("G_MPDM", false, true)
	override.Messages = []goslack.Message{{Msg: goslack.Msg{Text: "new", Timestamp: "2.0"}}}
	got := mergeDMOverride([]*slack.ChannelUnread{baseMpDM}, []*slack.ChannelUnread{override})
	if got[0].Messages[0].Text != "new" {
		t.Fatalf("mpdm override must replace base; got %q", got[0].Messages[0].Text)
	}
}
