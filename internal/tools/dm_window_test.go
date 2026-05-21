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

func TestMergeDMOverride_preservesNonDMInBase(t *testing.T) {
	// A regular channel in base with the same ID as a DM in override
	// must not be replaced — only DM entries get the override
	// treatment.
	baseRegular := mkChannel("C1", false, false)
	override := mkChannel("C1", true, false) // Slack would never re-emit a public channel as DM, but guard anyway
	got := mergeDMOverride([]*slack.ChannelUnread{baseRegular}, []*slack.ChannelUnread{override})
	if got[0].Channel.IsIM {
		t.Fatalf("non-DM base must not be replaced by DM override; got IsIM=true")
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
