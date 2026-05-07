package tools

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Slack permalinks come in two shapes:
//
//	https://example.slack.com/archives/<CHANNEL_ID>/p<TS_NO_DOTS>
//	https://example.slack.com/archives/<CHANNEL_ID>/p<TS_NO_DOTS>?thread_ts=<TS>&cid=<CHANNEL_ID>
//
// The "p" timestamp is the message's own ts with the decimal removed and
// padded to 6 fractional digits — i.e. "1714000000.000123" becomes
// "p1714000000000123". When the message is a thread reply, the
// `thread_ts` query parameter carries the root message's ts; otherwise
// the message itself is the thread root and parsedTS doubles as
// threadTS.
//
// permalinkChannelRe captures the channel ID from the path; the
// permalinkTSRe captures the "p<digits>" segment. Both are anchored
// loosely so we do not depend on the workspace subdomain.
var (
	permalinkChannelRe = regexp.MustCompile(`/archives/([A-Z][A-Z0-9]{6,})/`)
	permalinkTSRe      = regexp.MustCompile(`/p(\d{10,})`)
)

// errNotASlackPermalink lets callers distinguish "user passed a
// permalink that did not parse" from "user did not pass a permalink at
// all" — the second is fine, the first is a validation error.
var errNotASlackPermalink = errors.New("not a slack permalink")

// parsedPermalink holds the fields extracted from a Slack message URL.
//
// ChannelID is the canonical channel ID (`C…` for public channels,
// `G…` for private, `D…` for DMs).
//
// TS is the message's own timestamp.
//
// ThreadTS is the thread root timestamp: equal to TS when the message
// is a top-level message, and equal to the `thread_ts` query parameter
// when the message is a reply.
type parsedPermalink struct {
	ChannelID string
	TS        string
	ThreadTS  string
}

// parseSlackPermalink extracts (channel_id, ts, thread_ts) from a Slack
// message permalink. Returns errNotASlackPermalink for inputs that are
// missing the channel or "p<ts>" segments. Empty input is a no-op:
// returns nil pointer and no error so callers can treat "no permalink"
// as "no override".
func parseSlackPermalink(raw string) (*parsedPermalink, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// We do not require the URL to be valid — the relevant captures are
	// in the path and a single query parameter. Falling through to the
	// regex on a non-URL string just means no match.
	chMatch := permalinkChannelRe.FindStringSubmatch(raw)
	tsMatch := permalinkTSRe.FindStringSubmatch(raw)
	if len(chMatch) < 2 || len(tsMatch) < 2 {
		return nil, errNotASlackPermalink
	}

	ts := decodePermalinkTS(tsMatch[1])
	threadTS := ts

	if u, err := url.Parse(raw); err == nil {
		if v := u.Query().Get("thread_ts"); v != "" {
			threadTS = v
		}
		if v := u.Query().Get("cid"); v != "" && chMatch[1] == "" {
			chMatch[1] = v
		}
	}

	return &parsedPermalink{
		ChannelID: chMatch[1],
		TS:        ts,
		ThreadTS:  threadTS,
	}, nil
}

// decodePermalinkTS turns the dot-stripped permalink form
// ("1714000000000123") back into the canonical Slack ts
// ("1714000000.000123"). Slack always pads the fractional part to six
// digits, so the decimal sits six characters from the end.
func decodePermalinkTS(packed string) string {
	if len(packed) <= 6 {
		return packed
	}
	cut := len(packed) - 6
	return packed[:cut] + "." + packed[cut:]
}
