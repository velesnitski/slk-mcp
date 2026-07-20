package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

// Conversation-reference resolution, shared by every tool that takes a
// `channel` argument (digest, thread, audio/image latest-mode). A
// reference can be any of the shapes an operator actually has at hand:
//
//	@handle          → that person's DM (roster lookup + conversations.open)
//	U…/W… user id    → that person's DM (the unread digest's own DM
//	                   headers print `#U0AAAA1111B`, so a copy-paste of
//	                   that must Just Work)
//	#name / name     → channel by name
//	C…/G…/D… id      → canonical conversation id (pass-through)
//
// classifyConversationRef is the pure decision; resolveConversation
// performs the API calls it prescribes.

type convRefKind int

const (
	refChannel convRefKind = iota // name or canonical conversation id → ResolveID
	refHandle                     // @handle → IDForHandle + OpenDM
	refUserID                     // bare U…/W… user id → OpenDM
)

// classifyConversationRef decides how a channel reference should be
// resolved and returns the cleaned token to resolve. Pure — unit-tested
// without API calls. A leading '#' is tolerated on every form because
// rendered digests prefix everything with '#', including DM headers.
func classifyConversationRef(ref string) (convRefKind, string) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "@") {
		return refHandle, ref
	}
	bare := strings.TrimPrefix(ref, "#")
	if strings.HasPrefix(bare, "@") {
		return refHandle, bare
	}
	if slack.IsUserID(bare) {
		return refUserID, bare
	}
	return refChannel, ref
}

// resolveConversation turns a conversation reference into a conversation
// ID, per classifyConversationRef. Methods on the scoped Hub so every
// lookup targets the right workspace.
func (h *Hub) resolveConversation(ctx context.Context, ref string) (string, error) {
	kind, cleaned := classifyConversationRef(ref)
	switch kind {
	case refHandle:
		uid, err := h.Users().IDForHandle(ctx, cleaned)
		if err != nil {
			return "", err
		}
		return h.Channels().OpenDM(ctx, uid)
	case refUserID:
		return h.Channels().OpenDM(ctx, cleaned)
	default:
		return h.Channels().ResolveID(ctx, cleaned)
	}
}

// resolveAuthor turns the `from` filter into a user ID. Empty = no
// filter. "me" = the authenticated user (needs a user token). Anything
// else is treated as a handle.
func (h *Hub) resolveAuthor(ctx context.Context, from string) (string, error) {
	from = strings.TrimSpace(from)
	switch {
	case from == "":
		return "", nil
	case strings.EqualFold(from, "me"):
		self, err := h.Unread().Self(ctx)
		if err != nil || self == "" {
			return "", fmt.Errorf("cannot resolve from=me: %v (needs a user token)", err)
		}
		return self, nil
	default:
		return h.Users().IDForHandle(ctx, from)
	}
}
