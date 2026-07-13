package slack

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
	// fileURLRe captures the file ID from a Slack file URL:
	//   https://example.slack.com/files/<USER_ID>/<FILE_ID>/<name>
	// File IDs are F-prefixed alphanumerics.
	fileURLRe = regexp.MustCompile(`/files/[^/]+/(F[A-Z0-9]+)`)
)

// ParseSlackFileURL extracts the file ID from a Slack file URL, e.g.
// the "Copy link" you get on an uploaded attachment
// (…/files/U…/F…/name.m4a). Returns ("", false) for anything that is
// not a file URL, so callers can fall through to message-permalink
// handling. A file URL points straight at an attachment, so it needs no
// channel/message resolution — files.info(fileID) yields the download.
func ParseSlackFileURL(raw string) (string, bool) {
	m := fileURLRe.FindStringSubmatch(strings.TrimSpace(raw))
	if len(m) < 2 {
		return "", false
	}
	return m[1], true
}

// ErrNotASlackPermalink lets callers distinguish "user passed a
// permalink that did not parse" from "user did not pass a permalink at
// all" — the second is fine, the first is a validation error.
var ErrNotASlackPermalink = errors.New("not a slack permalink")

// ParsedPermalink holds the fields extracted from a Slack message URL.
//
// ChannelID is the canonical channel ID (`C…` for public channels,
// `G…` for private, `D…` for DMs).
//
// TS is the message's own timestamp.
//
// ThreadTS is the thread root timestamp: equal to TS when the message
// is a top-level message, and equal to the `thread_ts` query parameter
// when the message is a reply.
type ParsedPermalink struct {
	ChannelID string
	TS        string
	ThreadTS  string
}

// ParseSlackPermalink extracts (channel_id, ts, thread_ts) from a Slack
// message permalink. Returns ErrNotASlackPermalink for inputs that are
// missing the channel or "p<ts>" segments. Empty input is a no-op:
// returns nil pointer and no error so callers can treat "no permalink"
// as "no override".
func ParseSlackPermalink(raw string) (*ParsedPermalink, error) {
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
		return nil, ErrNotASlackPermalink
	}

	ts := DecodePermalinkTS(tsMatch[1])
	threadTS := ts

	if u, err := url.Parse(raw); err == nil {
		if v := u.Query().Get("thread_ts"); v != "" {
			threadTS = v
		}
		if v := u.Query().Get("cid"); v != "" && chMatch[1] == "" {
			chMatch[1] = v
		}
	}

	return &ParsedPermalink{
		ChannelID: chMatch[1],
		TS:        ts,
		ThreadTS:  threadTS,
	}, nil
}

// DecodePermalinkTS turns the dot-stripped permalink form
// ("1714000000000123") back into the canonical Slack ts
// ("1714000000.000123"). Slack always pads the fractional part to six
// digits, so the decimal sits six characters from the end.
func DecodePermalinkTS(packed string) string {
	if len(packed) <= 6 {
		return packed
	}
	cut := len(packed) - 6
	return packed[:cut] + "." + packed[cut:]
}
