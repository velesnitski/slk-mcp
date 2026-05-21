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
