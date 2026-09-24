package tools

import (
	"context"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// How conversations.history pages, measured against the live API (see
// ADR 111) rather than assumed:
//
//   - `latest` set, `oldest` unset → the page ADJACENT TO `latest`: the
//     newest `limit` messages at or before it.
//   - `oldest` set, `latest` unset → the page ADJACENT TO `oldest`: the
//     OLDEST `limit` messages after it.
//
// Both pages come back newest-first, which is what made the second case
// look safe: the order is right, the slice is not. Any caller that wants
// "the recent part of a window" and passes `oldest` gets the stale end of
// the window instead whenever the window holds more than one page — in a
// busy DM or channel the newest messages silently never arrive.
//
// So windows are fetched from their upper edge and trimmed to their lower
// edge locally. A window larger than one page loses its OLDEST part,
// which is the part a digest can most afford to lose.

// recentHistory returns the newest messages of a conversation that fall
// in [oldest, latest], at most `limit` fetched. A zero latest means "now".
func recentHistory(ctx context.Context, mc MessageClient, channelID string, oldest, latest time.Time, limit int) ([]goslack.Message, error) {
	p := slack.HistoryParams{ChannelID: channelID, Limit: limit}
	if !latest.IsZero() {
		p.LatestTS = float64(latest.Unix())
	}
	page, err := mc.History(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]goslack.Message, 0, len(page))
	for _, m := range page {
		if tsWithin(m.Timestamp, oldest, latest) {
			out = append(out, m)
		}
	}
	return out, nil
}

// threadParentsBefore returns thread parents posted in the `lookback`
// before `oldest` whose newest reply landed at or after it — the threads a
// window can only be understood through, because conversations.history
// never returns replies and a reply's parent may predate the window.
//
// Fetched from the window's lower edge downward, so the page is the one
// adjacent to the window, not the one adjacent to `oldest - lookback`.
// Messages already in `have` (the window page) are skipped: `latest` is
// inclusive, so the boundary message can appear on both pages.
func threadParentsBefore(ctx context.Context, mc MessageClient, channelID string, oldest time.Time, lookback time.Duration, limit int, have []goslack.Message) ([]goslack.Message, error) {
	page, err := mc.History(ctx, slack.HistoryParams{
		ChannelID: channelID,
		LatestTS:  float64(oldest.Unix()),
		Limit:     limit,
	})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(have))
	for _, m := range have {
		seen[m.Timestamp] = struct{}{}
	}
	floor := oldest.Add(-lookback)
	var out []goslack.Message
	for _, m := range page {
		if _, dup := seen[m.Timestamp]; dup {
			continue
		}
		if !tsWithin(m.Timestamp, floor, oldest) {
			continue
		}
		if isThreadParent(m) && threadMovedInWindow(m, oldest) {
			out = append(out, m)
		}
	}
	return out, nil
}
