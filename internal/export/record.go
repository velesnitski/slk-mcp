// Package export writes conversation records to append-only JSONL.
//
// Capture is deliberately lossless: no summarising, no dropping of
// low-signal messages, no classification. Anything decided here cannot
// be revisited later, because the source is gone by then. Judgement
// belongs to whatever reads the corpus.
package export

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SchemaVersion is stamped on every record. A corpus accumulates for
// months and will outlive at least one format change, so readers must be
// able to tell which shape they are looking at.
const SchemaVersion = 1

// Record is one captured message.
//
// Fields exist to preserve what cannot be reconstructed after the fact:
// thread structure, who reacted (often the only act of agreement in a
// channel), whether a message was edited, and how much of a thread was
// actually fetched.
type Record struct {
	V         int    `json:"v"`
	Workspace string `json:"ws"`
	ChannelID string `json:"ch"`
	Channel   string `json:"ch_name,omitempty"`
	Kind      string `json:"ch_kind"` // channel | private | im | mpim
	TS        string `json:"ts"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	User      string `json:"user,omitempty"`
	UserName  string `json:"user_name,omitempty"`
	Text      string `json:"text"`
	Subtype   string `json:"subtype,omitempty"`
	Edited    bool   `json:"edited,omitempty"`

	Reactions []Reaction `json:"reactions,omitempty"`
	Files     []FileRef  `json:"files,omitempty"`

	// ReplyCount is Slack's own count on a thread parent; RepliesFetched
	// is how many this export actually holds. Unequal means the record of
	// that thread is partial -- without both numbers a later reader
	// cannot distinguish a short thread from a truncated one.
	ReplyCount     int `json:"reply_count,omitempty"`
	RepliesFetched int `json:"replies_fetched,omitempty"`

	Permalink string `json:"permalink,omitempty"`

	// Redacted counts secret-shaped spans replaced in Text.
	Redacted int `json:"redacted,omitempty"`
}

// Reaction keeps the actors, not just the count.
type Reaction struct {
	Name  string   `json:"name"`
	Users []string `json:"users,omitempty"`
}

// FileRef references an attachment without copying its bytes.
type FileRef struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
}

// Key is the dedup identity of a record. Re-running an export over an
// overlapping window must not double-write, so callers load existing
// keys and skip them.
func (r Record) Key() string {
	return r.Workspace + "\x00" + r.ChannelID + "\x00" + r.TS
}

// Permalink builds Slack's deterministic archive URL. Deriving it beats
// a chat.getPermalink call per message, which would make an export of a
// few thousand messages rate-limit-bound. Empty teamURL yields "".
func Permalink(teamURL, channelID, ts, threadTS string) string {
	if teamURL == "" || channelID == "" || ts == "" {
		return ""
	}
	base := strings.TrimRight(teamURL, "/")
	link := fmt.Sprintf("%s/archives/%s/p%s", base, channelID, strings.Replace(ts, ".", "", 1))
	if threadTS != "" && threadTS != ts {
		link += "?thread_ts=" + threadTS + "&cid=" + channelID
	}
	return link
}

// Write appends records as JSONL. One line per record, flushed once.
func Write(w io.Writer, recs []Record) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	for _, r := range recs {
		r.V = SchemaVersion
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("export: encode %s: %w", r.TS, err)
		}
	}
	return bw.Flush()
}

// ReadKeys scans an existing corpus and returns the keys already in it.
// A malformed line is skipped rather than fatal: a truncated last line
// from an interrupted run must not block every future append.
func ReadKeys(r io.Reader) map[string]struct{} {
	seen := make(map[string]struct{})
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var rec Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			continue
		}
		if rec.TS == "" {
			continue
		}
		seen[rec.Key()] = struct{}{}
	}
	return seen
}

// Dedup drops records whose key is already present, preserving order.
func Dedup(recs []Record, seen map[string]struct{}) []Record {
	out := make([]Record, 0, len(recs))
	for _, r := range recs {
		k := r.Key()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	return out
}
