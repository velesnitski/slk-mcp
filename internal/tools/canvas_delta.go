package tools

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/velesnitski/slk-mcp/internal/slack"
)

const (
	// canvasDeltaLimit caps how many changed canvases the sweep reports.
	// Canvas edits are rare compared to messages; a dozen is already an
	// unusually busy day.
	canvasDeltaLimit = 12

	// canvasProbeLimit caps how many of those get DOWNLOADED to look for a
	// mention of the operator. Each probe is one HTTP fetch, so the cheap
	// listing stays unbounded (within canvasDeltaLimit) while the
	// expensive body read is bounded independently.
	canvasProbeLimit = 6

	// canvasProbeBytes bounds each probed body. A mention lives in prose,
	// not in a megabyte of embedded content.
	canvasProbeBytes = 200_000
)

// canvasHit is one changed canvas as the unread sweep reports it.
type canvasHit struct {
	Ref         slack.CanvasRef
	MentionsYou bool
	Probed      bool // body was fetched, so MentionsYou is meaningful
	Labels      []string
}

// canvasDelta lists canvases updated since `since` (Unix seconds) and,
// for the newest few, checks whether the operator is @-mentioned inside.
//
// This exists because a canvas edit produces NO channel message: Slack
// notifies the person tagged inside the document, but conversations.history
// has nothing to return and search.messages does not index canvas bodies.
// Every other backstop in the sweep works off messages, so before this
// the operator's own "you were tagged" ping was structurally invisible.
//
// Best-effort throughout: any failure returns what it has. A canvas
// section is a bonus on top of the message sweep, never a reason to fail it.
func (h *Hub) canvasDelta(ctx context.Context, since int64, selfID string) []canvasHit {
	refs, err := h.Canvas().RecentCanvases(ctx, since, canvasDeltaLimit)
	if err != nil {
		h.log.Warn("canvas delta failed; canvases omitted from sweep", "err", err)
		return nil
	}
	if len(refs) == 0 {
		return nil
	}

	// Resolve channel IDs to names in one batch.
	ids := make([]string, 0, len(refs))
	for _, r := range refs {
		ids = append(ids, r.Channels...)
	}
	names := h.Channels().NamesForIDs(ctx, ids)

	hits := make([]canvasHit, 0, len(refs))
	for i, r := range refs {
		hit := canvasHit{Ref: r, Labels: canvasChannelLabels(r.Channels, names)}
		if selfID != "" && i < canvasProbeLimit && r.DownloadURL != "" {
			var buf bytes.Buffer
			if derr := h.Messages().DownloadFile(ctx, r.DownloadURL, &buf); derr != nil {
				h.log.Debug("canvas probe download failed", "canvas", r.ID, "err", derr)
			} else {
				body, _ := canvasToText(buf.Bytes(), r.Mimetype, canvasProbeBytes)
				hit.Probed = true
				hit.MentionsYou = canvasMentionsSelf(body, selfID)
			}
		}
		hits = append(hits, hit)
	}
	return hits
}

// canvasChannelLabels maps channel IDs to "#name" labels, falling back to
// the raw ID when the name is unknown (private channel the token can't
// name, or a DM). Pure.
func canvasChannelLabels(ids []string, names map[string]string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if n := names[id]; n != "" {
			out = append(out, "#"+n)
			continue
		}
		out = append(out, id)
	}
	return out
}

// canvasMentionsSelf reports whether a rendered canvas body @-mentions the
// operator. Slack canvases carry mentions as the same `<@Uxxxx>` token
// messages use; the markdown export sometimes drops the angle brackets,
// so the bare ID is accepted too. Pure.
func canvasMentionsSelf(body, selfID string) bool {
	if selfID == "" || body == "" {
		return false
	}
	return strings.Contains(body, "<@"+selfID+">") || strings.Contains(body, "@"+selfID)
}

// renderCanvasDelta renders the canvas section, mention-carrying canvases
// first. `now` is Unix seconds, used for the relative "edited Nh ago"
// stamp. Returns "" for no hits. Pure.
func renderCanvasDelta(hits []canvasHit, now int64) string {
	if len(hits) == 0 {
		return ""
	}
	ordered := make([]canvasHit, len(hits))
	copy(ordered, hits)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].MentionsYou != ordered[j].MentionsYou {
			return ordered[i].MentionsYou
		}
		return ordered[i].Ref.Updated > ordered[j].Ref.Updated
	})

	mentions := 0
	for _, hit := range ordered {
		if hit.MentionsYou {
			mentions++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "CANVASES — %d updated", len(ordered))
	if mentions > 0 {
		fmt.Fprintf(&b, ", %d mentioning you", mentions)
	}
	b.WriteString("\n")

	for _, hit := range ordered {
		title := strings.TrimSpace(hit.Ref.Title)
		if title == "" {
			title = "(untitled canvas)"
		}
		b.WriteString("  • ")
		if hit.MentionsYou {
			b.WriteString("@you — ")
		}
		b.WriteString(title)
		if len(hit.Labels) > 0 {
			fmt.Fprintf(&b, " — %s", strings.Join(hit.Labels, ", "))
		}
		verb := "created"
		if hit.Ref.ChangedAfterCreate() {
			verb = "edited"
		}
		fmt.Fprintf(&b, " — %s %s", verb, humanSince(now-hit.Ref.Updated))
		if !hit.Probed {
			b.WriteString(" — body not checked")
		}
		b.WriteString("\n")
		if hit.Ref.Permalink != "" {
			fmt.Fprintf(&b, "    %s\n", hit.Ref.Permalink)
		}
	}
	return b.String()
}

// humanSince renders an age in seconds as a compact relative stamp.
// Negative (clock skew) reads as "just now". Pure.
func humanSince(sec int64) string {
	switch {
	case sec < 60:
		return "just now"
	case sec < 3600:
		return strconv.FormatInt(sec/60, 10) + "m ago"
	case sec < 86400:
		return strconv.FormatInt(sec/3600, 10) + "h ago"
	default:
		return strconv.FormatInt(sec/86400, 10) + "d ago"
	}
}

// canvasSince resolves the canvas lookback start: the delta cursor when
// the caller passed one (so canvases follow the same delta as messages),
// otherwise now minus `hours`. Pure.
func canvasSince(afterTS string, hours int, now int64) int64 {
	if ts, err := strconv.ParseFloat(strings.TrimSpace(afterTS), 64); err == nil && ts > 0 {
		return int64(ts)
	}
	if hours <= 0 {
		return 0
	}
	return now - int64(hours)*3600
}
