package slack

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

func TestScheduledService_RequiresUserToken(t *testing.T) {
	// A bot-only workspace has a nil user client: List must fail loudly
	// with ErrNoUserTokenScheduled rather than returning an empty list
	// that reads as "you have nothing scheduled".
	s := newScheduledService(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if s.Enabled() {
		t.Fatal("Enabled() must be false with no user token")
	}
	if _, err := s.List(context.Background()); !errors.Is(err, ErrNoUserTokenScheduled) {
		t.Fatalf("List without a user token should return ErrNoUserTokenScheduled; got %v", err)
	}
}
