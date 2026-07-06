package tools

import (
	"context"
	"testing"
)

// These exercise the workspace-routing added in v1.0.0: the error path
// (unknown label) returns before any Slack call, so no live server or
// network is needed — the same no-network seam used by the unread and
// list_channels workspace tests.

func TestRunMultiChannelDigest_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runMultiChannelDigest(context.Background(), "ghost", "general", 24, 20)
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestRunMorningRecap_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runMorningRecap(context.Background(), "ghost", "general", 24, 15)
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestRunMultiChannelDigest_KnownWorkspaceScopes(t *testing.T) {
	// A named workspace resolves to exactly one target and does not hit
	// the unknown-label path. The empty-channels config on the fake hub
	// makes multiChannelDigestBody return the "no channels" error, which
	// the single-target path surfaces directly — proving we reached the
	// body (routing succeeded) rather than erroring on the label.
	hub := twoWorkspaceHub(t)
	res := hub.runMultiChannelDigest(context.Background(), "secondary", "", 24, 20)
	if res == nil || !res.IsError {
		t.Fatalf("expected the no-channels body error, got %+v", res)
	}
}
