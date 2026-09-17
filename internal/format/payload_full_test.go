package format

import (
	"strings"
	"testing"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"
)

func payloadMessage(text string) goslack.Message {
	var msg goslack.Message
	msg.Attachments = []goslack.Attachment{{Text: text}}
	return msg
}

// Slicing a string by byte index splits the final multi-byte rune, and
// the reader sees a replacement glyph where a letter should be. Every
// non-ASCII digest line hit this.
func TestTruncateRunes_doesNotSplitAMultiByteRune(t *testing.T) {
	body := strings.Repeat("я", 300)
	cut, over := truncateRunes(body, 220)
	if !utf8.ValidString(cut) {
		t.Fatal("truncation produced invalid UTF-8")
	}
	if got := utf8.RuneCountInString(cut); got != 220 {
		t.Fatalf("want 220 runes kept, got %d", got)
	}
	if over != 80 {
		t.Fatalf("want 80 runes dropped, got %d", over)
	}
}

// The suffix says "chars", so it must count characters. Measured in
// bytes a two-byte-per-rune body reports roughly double what was lost.
func TestTruncateRunes_overCountIsRunesNotBytes(t *testing.T) {
	body := strings.Repeat("я", 300) // 600 bytes, 300 runes
	if _, over := truncateRunes(body, 100); over != 200 {
		t.Fatalf("want 200 runes dropped, got %d", over)
	}
}

func TestTruncateRunes_shortBodyPassesThrough(t *testing.T) {
	cut, over := truncateRunes("коротко", 220)
	if cut != "коротко" || over != 0 {
		t.Fatalf("a short body must pass through untouched, got %q / %d", cut, over)
	}
}

func TestTruncateRunes_nonPositiveLimitMeansNoLimit(t *testing.T) {
	body := strings.Repeat("я", 300)
	for _, limit := range []int{0, -1} {
		if cut, over := truncateRunes(body, limit); cut != body || over != 0 {
			t.Errorf("limit %d must not truncate; %d runes dropped", limit, over)
		}
	}
}

// get_message promises full text and is documented as the drill-in a
// "(+N chars)" preview points at, so it must render the payload whole.
// A forwarded message carries its entire body in the attachment, so
// clipping it there left the only reachable copy unreadable.
func TestHiddenPayload_rendersTheWholePayload(t *testing.T) {
	body := strings.Repeat("я", 2000)
	got := HiddenPayload(payloadMessage(body))
	if strings.Contains(got, "chars)") {
		t.Fatal("get_message must not truncate the payload")
	}
	if n := utf8.RuneCountInString(got); n != 2000 {
		t.Fatalf("want the full 2000 runes, got %d", n)
	}
}

// The digest keeps its cap — one verbose attachment must not dominate
// a line — and its truncation stays valid UTF-8.
func TestRenderHiddenPayloadMarker_digestStillCaps(t *testing.T) {
	body := strings.Repeat("я", 2000)
	got := renderHiddenPayloadMarker(payloadMessage(body), HiddenPayloadLimit)
	if !strings.Contains(got, "(+1780 chars)") {
		t.Errorf("digest must still cap the payload and report runes dropped, got %d runes", utf8.RuneCountInString(got))
	}
	if !utf8.ValidString(got) {
		t.Error("digest truncation produced invalid UTF-8")
	}
}
