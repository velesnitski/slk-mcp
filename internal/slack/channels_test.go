package slack

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// newTestChannelService builds a ChannelService with a nil API client.
// Any code path that reaches the Slack API will panic — used to assert
// that short-circuit logic doesn't even attempt an API call.
func newTestChannelService() *ChannelService {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newChannelService(nil, nil, log)
}

func TestResolveID_shortCircuitsOnCanonicalID(t *testing.T) {
	// A permalink-derived channel ID should pass through ResolveID
	// without hitting the workspace listing API. Before this fix the
	// service tried to resolve the ID as a *name* and failed with
	// "channel #C0... not found".
	s := newTestChannelService()
	got, err := s.ResolveID(context.Background(), "C0ABC1234DE")
	if err != nil {
		t.Fatalf("canonical ID should pass through; got err=%v", err)
	}
	if got != "C0ABC1234DE" {
		t.Fatalf("got %q want %q", got, "C0ABC1234DE")
	}
}

func TestResolveID_shortCircuitsOnDMID(t *testing.T) {
	// A DM/permalink-derived `D…` ID must pass through ResolveID too,
	// so get_thread(permalink) and get_channel_digest can address a DM
	// directly. Before v0.4.19 this failed with "channel #D0… not
	// found" because IsChannelID excluded the DM prefix. The api here
	// is nil — a successful pass-through proves no listing call was made.
	s := newTestChannelService()
	got, err := s.ResolveID(context.Background(), "D0ABCDEF123")
	if err != nil {
		t.Fatalf("DM ID should pass through; got err=%v", err)
	}
	if got != "D0ABCDEF123" {
		t.Fatalf("got %q want %q", got, "D0ABCDEF123")
	}
}

func TestResolveID_shortCircuitsWithLeadingHash(t *testing.T) {
	// Some callers strip the `#` themselves, others don't. The
	// short-circuit must fire for both `#CID` and `CID` so behaviour
	// is consistent regardless of which path the channel arg took.
	s := newTestChannelService()
	got, err := s.ResolveID(context.Background(), "#G0PRIVATE12")
	if err != nil {
		t.Fatalf("canonical ID with leading # should pass through; got err=%v", err)
	}
	if got != "G0PRIVATE12" {
		t.Fatalf("got %q want %q", got, "G0PRIVATE12")
	}
}

func TestIsChannelID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Canonical IDs.
		{"C0ABC1234DE", true},
		{"C0XYZW9876", true},
		{"G0PRIVATE12", true},

		// Invalid: lowercase, too short, wrong prefix, DM prefix.
		{"c0abc1234de", false},
		{"C0", false},
		{"U0ABC1234DE", false}, // user id, not channel
		{"D0ABC1234DE", false}, // DM id, intentionally excluded
		{"general", false},
		{"#general", false},
		{"", false},
		{"C-WITH-DASH", false},
	}
	for _, c := range cases {
		if got := IsChannelID(c.in); got != c.want {
			t.Errorf("IsChannelID(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

func TestIsConversationID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Channels AND DMs/mpdms are all conversation IDs.
		{"C0ABC1234DE", true},
		{"G0PRIVATE12", true},
		{"D0ABCDEF123", true}, // DM — the case IsChannelID rejects

		// Still not conversations.
		{"U0ABC1234DE", false}, // user id
		{"d0abcdef123", false}, // lowercase
		{"D0", false},          // too short
		{"general", false},     // a name, not an ID
		{"#general", false},
		{"", false},
		{"D-WITH-DASH", false},
	}
	for _, c := range cases {
		if got := IsConversationID(c.in); got != c.want {
			t.Errorf("IsConversationID(%q) = %v; want %v", c.in, got, c.want)
		}
	}

	// The whole point of the split: a DM ID is a conversation but not
	// a "channel" (so name-resolution never short-circuits on it, but
	// permalink/drill-in paths do).
	if IsChannelID("D0ABCDEF123") {
		t.Fatal("IsChannelID must still reject DM ids")
	}
	if !IsConversationID("D0ABCDEF123") {
		t.Fatal("IsConversationID must accept DM ids")
	}
}
