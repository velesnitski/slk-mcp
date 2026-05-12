package tools

import (
	"context"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// fakeUsers is a hand-rolled mock that satisfies UserClient.
// Demonstrates the substitution pattern: a test builds whatever
// minimal fake it needs and feeds it into a handler-under-test
// without spinning up a real slack.Client or hitting the Slack API.
type fakeUsers struct {
	names map[string]string
}

func (f *fakeUsers) NamesFor(ctx context.Context, ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if n, ok := f.names[id]; ok {
			out[id] = n
		}
	}
	return out
}

func (f *fakeUsers) Name(ctx context.Context, id string) string {
	if n, ok := f.names[id]; ok {
		return n
	}
	return id
}

func (f *fakeUsers) List(ctx context.Context) ([]goslack.User, error) {
	return nil, nil
}

func TestUserClient_AcceptsFakeImplementation(t *testing.T) {
	// Smoke-test the contract: a hand-rolled fake satisfies the
	// interface declared in contracts.go. If this fails, the
	// contract diverged from the consumer-side spec.
	var c UserClient = &fakeUsers{names: map[string]string{"U001": "Alice"}}
	got := c.NamesFor(context.Background(), []string{"U001", "U_MISSING"})
	if got["U001"] != "Alice" {
		t.Errorf("fake returned %v; want U001 → Alice", got)
	}
	if _, ok := got["U_MISSING"]; ok {
		t.Errorf("fake returned a value for U_MISSING; should have been omitted")
	}
}

// The compile-time assertions in contracts.go cover the production
// side: `*slack.UserService` satisfies UserClient. This package-level
// declaration documents the same fact and produces a readable
// failure if the build assertion ever needs debugging.
var _ UserClient = (*slack.UserService)(nil)
