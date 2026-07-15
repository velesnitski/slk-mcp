package slack

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestDNDService_RequiresUserToken(t *testing.T) {
	// A bot-only workspace has a nil user client: DND must report
	// disabled and fail loudly rather than silently no-op'ing.
	s := newDNDService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s.Enabled() {
		t.Fatal("Enabled() must be false with no user token")
	}
	if err := s.Snooze(context.Background(), 30); !errors.Is(err, ErrNoUserTokenDND) {
		t.Fatalf("Snooze without a user token should return ErrNoUserTokenDND; got %v", err)
	}
	if err := s.EndSnooze(context.Background()); !errors.Is(err, ErrNoUserTokenDND) {
		t.Fatalf("EndSnooze without a user token should return ErrNoUserTokenDND; got %v", err)
	}
}
