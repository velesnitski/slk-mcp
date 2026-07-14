package tools

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestLooksLikeScopeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"missing_scope", errors.New("conversations.history: missing_scope"), true},
		{"token type", errors.New("files.info: not_allowed_token_type"), true},
		{"invalid_auth", fmt.Errorf("wrap: %w", errors.New("invalid_auth")), true},
		{"case-insensitive", errors.New("MISSING_SCOPE"), true},
		{"not found is not scope", errors.New("channel #ops not found"), false},
		{"transient is not scope", errors.New("conversations.history: rate limited"), false},
		{"needs-user-token custom msg", errors.New("cannot resolve from=me: <nil> (needs a user token)"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := looksLikeScopeError(c.err); got != c.want {
			t.Errorf("looksLikeScopeError(%v) = %v; want %v", c.err, got, c.want)
		}
	}
}

func TestAudioScopeError_DecoratesScopeFailure(t *testing.T) {
	// A scope failure gets the workspace label and the actionable scope
	// list; the original error text is preserved as the prefix.
	got := audioScopeError(" [secondary]", errors.New("conversations.history: missing_scope"))
	for _, want := range []string{"missing_scope", "[secondary]", "files:read", "im:history", "reinstall the app"} {
		if !strings.Contains(got, want) {
			t.Errorf("decorated message missing %q; got: %s", want, got)
		}
	}
}

func TestAudioScopeError_PassesThroughNonScope(t *testing.T) {
	// A genuine not-found error must not be reframed as a scope problem.
	in := errors.New("channel #ops not found")
	got := audioScopeError(" [secondary]", in)
	if got != in.Error() {
		t.Errorf("non-scope error should pass through verbatim; got: %s", got)
	}
	if strings.Contains(got, "scope") {
		t.Errorf("non-scope error must not mention scopes; got: %s", got)
	}
}

func TestAudioScopeError_SingleWorkspaceNoLabel(t *testing.T) {
	// Empty wsLabel (single-workspace mode) renders cleanly with no
	// stray brackets.
	got := audioScopeError("", errors.New("missing_scope"))
	if strings.Contains(got, "[]") || strings.Contains(got, "the  workspace") {
		t.Errorf("empty label should not leave stray brackets/double space; got: %s", got)
	}
	if !strings.Contains(got, "the workspace token") {
		t.Errorf("expected clean 'the workspace token' phrasing; got: %s", got)
	}
}

func TestErrFilesReadScope_IsMatchable(t *testing.T) {
	// The HTML-sign-in sentinel must survive wrapping so finishFetch can
	// detect it with errors.Is and swap in the files:read hint.
	wrapped := fmt.Errorf("download voice.m4a: %w", errFilesReadScope)
	if !errors.Is(wrapped, errFilesReadScope) {
		t.Fatal("wrapped errFilesReadScope must be matchable via errors.Is")
	}
	// And it should NOT be classified by looksLikeScopeError — it's an
	// HTML 200, not a Slack API scope error, so it takes the dedicated
	// files:read branch in finishFetch instead.
	if looksLikeScopeError(errFilesReadScope) {
		t.Error("errFilesReadScope should route via the sentinel branch, not looksLikeScopeError")
	}
}
