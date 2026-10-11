package format

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

func pinClock(t *testing.T, now time.Time) {
	t.Helper()
	old := clockNow
	clockNow = func() time.Time { return now }
	t.Cleanup(func() { clockNow = old })
}

func TestClockLabel_TodayIsShortOtherDaysCarryTheDate(t *testing.T) {
	now := time.Date(2026, 10, 11, 18, 0, 0, 0, time.Local)
	pinClock(t, now)

	if got := ClockLabel(time.Date(2026, 10, 11, 9, 5, 0, 0, time.Local)); got != "09:05" {
		t.Fatalf("today: got %q", got)
	}
	if got := ClockLabel(time.Date(2026, 10, 2, 17, 17, 0, 0, time.Local)); got != "02.10 17:17" {
		t.Fatalf("another day: got %q", got)
	}
}

func TestMessageLine_OldMessageShowsItsDate(t *testing.T) {
	pinClock(t, time.Date(2026, 10, 11, 18, 0, 0, 0, time.Local))
	ts := time.Date(2026, 10, 2, 17, 17, 0, 0, time.Local).Unix()
	msg := goslack.Message{Msg: goslack.Msg{Timestamp: strconv.FormatInt(ts, 10) + ".000100", Text: "hi"}}

	if got := MessageLine(msg, "sam"); !strings.HasPrefix(got, "[02.10 17:17 sam]") {
		t.Fatalf("got %q", got)
	}
}

func richTextMessage(t *testing.T, blocksJSON string) goslack.Message {
	t.Helper()
	var m goslack.Message
	if err := json.Unmarshal([]byte(`{"type":"message","ts":"1700000000.000100","text":"","blocks":`+blocksJSON+`}`), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBlocksText_RichTextBodyIsRendered(t *testing.T) {
	m := richTextMessage(t, `[{"type":"rich_text","elements":[
		{"type":"rich_text_section","elements":[
			{"type":"text","text":"Reminder: "},
			{"type":"user","user_id":"U0PEER0001"},
			{"type":"text","text":" check "},
			{"type":"link","url":"https://example.com/x","text":"the report"},
			{"type":"emoji","name":"eyes"}]},
		{"type":"rich_text_list","style":"bullet","elements":[
			{"type":"rich_text_section","elements":[{"type":"text","text":"first item"}]},
			{"type":"rich_text_section","elements":[{"type":"text","text":"second item"}]}]}]}]`)

	got := renderHiddenPayloadMarker(m, 0)

	for _, want := range []string{"Reminder: <@U0PEER0001> check the report:eyes:", "• first item", "• second item"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "[blocks:") {
		t.Fatalf("a readable rich_text body must not fall back to a count: %q", got)
	}
}

func TestBlocksText_EmptyRichTextStillCounts(t *testing.T) {
	m := richTextMessage(t, `[{"type":"rich_text","elements":[]}]`)

	if got := renderHiddenPayloadMarker(m, 0); got != "[blocks: 1]" {
		t.Fatalf("no prose must keep the honest count, got %q", got)
	}
}
