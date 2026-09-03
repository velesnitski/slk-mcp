package slack

import (
	"errors"
	"testing"
)

func TestIsAuthRefusal(t *testing.T) {
	// Slack answers a file request from a non-member with a bare HTTP
	// status, so the check is on the message. These are the shapes seen
	// in practice.
	refusals := []string{
		"slack server error: 401 Unauthorized",
		"slack server error: 403 Forbidden",
		"not_allowed_token_type",
		"missing_scope",
		"access_denied",
	}
	for _, m := range refusals {
		if !isAuthRefusal(errors.New(m)) {
			t.Errorf("%q should read as an auth refusal", m)
		}
	}
	// Everything else must NOT trigger a pointless retry with a second
	// token: a missing file is missing for both.
	others := []string{
		"slack server error: 404 Not Found",
		"slack server error: 500 Internal Server Error",
		"dial tcp: connection refused",
		"context deadline exceeded",
		"file_not_found",
	}
	for _, m := range others {
		if isAuthRefusal(errors.New(m)) {
			t.Errorf("%q must not be treated as an auth refusal", m)
		}
	}
	if isAuthRefusal(nil) {
		t.Fatal("nil is not a refusal")
	}
}

func TestNewMessageService_NoSelfFallback(t *testing.T) {
	// When one token serves both roles, retrying with "the other client"
	// would repeat the identical refused request.
	api := &struct{}{}
	_ = api
	s := newMessageService(nil, nil, nil, nil, nil)
	if s.fallback != nil {
		t.Fatal("a nil user client must not become a fallback")
	}
}
