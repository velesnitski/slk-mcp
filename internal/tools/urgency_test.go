package tools

import (
	"strconv"
	"testing"
	"time"

	goslack "github.com/slack-go/slack"
)

// fixedNow returns a deterministic reference time used to fix
// recency bands across the urgency tests.
func fixedNow() time.Time {
	return time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
}

// tsOffset returns a Slack-style timestamp at a given offset from
// fixedNow. Negative offsets land in the past.
func tsOffset(d time.Duration) string {
	return strconv.FormatInt(fixedNow().Add(d).Unix(), 10) + ".000000"
}

func msgWithText(text string) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = "" // recency disabled by default
	m.User = "U2"
	m.Text = text
	return m
}

// ----------------------- per-signal tests -----------------------

func TestMessageUrgency_QuestionMarksCappedAtThree(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"plain", 0},
		{"hi?", 1 * urgencyQuestionWeight},
		{"what??", 2 * urgencyQuestionWeight},
		{"why???", 3 * urgencyQuestionWeight},
		{"why????", 3 * urgencyQuestionWeight},     // capped
		{"why??????????", 3 * urgencyQuestionWeight}, // still capped
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			got := messageUrgency(msgWithText(c.text), time.Time{})
			if got != c.want {
				t.Fatalf("text=%q: messageUrgency=%d; want %d", c.text, got, c.want)
			}
		})
	}
}

func TestMessageUrgency_FullWidthQuestionMarkCounts(t *testing.T) {
	got := messageUrgency(msgWithText("что？？"), time.Time{})
	want := 2 * urgencyQuestionWeight
	if got != want {
		t.Fatalf("CJK ？ should count: got %d, want %d", got, want)
	}
}

func TestMessageUrgency_KeywordEnglish(t *testing.T) {
	for _, kw := range []string{"urgent", "URGENT", "asap", "blocker", "critical", "important", "stuck"} {
		t.Run(kw, func(t *testing.T) {
			got := messageUrgency(msgWithText("hey we have a "+kw+" issue"), time.Time{})
			if got < urgencyKeywordWeight {
				t.Fatalf("keyword %q: got %d; want >= %d", kw, got, urgencyKeywordWeight)
			}
		})
	}
}

func TestMessageUrgency_KeywordRussian(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"всё срочно надо чинить", urgencyKeywordWeight},
		{"СРОЧНО упало, помогите", urgencyKeywordWeight * 3}, // срочно + упало + помогите
		{"критично сломалось", urgencyKeywordWeight * 2},
		{"всё хорошо", 0},
		{"не работает приложение", urgencyKeywordWeight},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			got := messageUrgency(msgWithText(c.text), time.Time{})
			if got != c.want {
				t.Fatalf("text=%q: got %d; want %d", c.text, got, c.want)
			}
		})
	}
}

func TestMessageUrgency_Reactions(t *testing.T) {
	m := msgWithText("see thread")
	m.Reactions = []goslack.ItemReaction{
		{Name: "rotating_light", Count: 1},
		{Name: "thumbsup", Count: 5}, // not in the urgency set
		{Name: "fire", Count: 2},
	}
	got := messageUrgency(m, time.Time{})
	want := 2 * urgencyReactionWeight // rotating_light + fire
	if got != want {
		t.Fatalf("urgency reactions: got %d; want %d", got, want)
	}
}

func TestMessageUrgency_RecencyBands(t *testing.T) {
	now := fixedNow()
	cases := []struct {
		name string
		ts   string
		want int
	}{
		{"30m ago", tsOffset(-30 * time.Minute), urgencyRecentBonus},
		{"3h ago", tsOffset(-3 * time.Hour), urgencyFreshBonus},
		{"24h ago", tsOffset(-24 * time.Hour), 0},
		{"future (clock skew)", tsOffset(1 * time.Hour), 0},
		{"empty ts", "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := msgWithText("plain body")
			m.Timestamp = c.ts
			got := messageUrgency(m, now)
			if got != c.want {
				t.Fatalf("ts=%s: got %d; want %d", c.ts, got, c.want)
			}
		})
	}
}

func TestMessageUrgency_ZeroNowDisablesRecency(t *testing.T) {
	m := msgWithText("recent body")
	m.Timestamp = tsOffset(-10 * time.Minute) // very recent
	if got := messageUrgency(m, time.Time{}); got != 0 {
		t.Fatalf("zero now should disable recency, got %d", got)
	}
}

// ----------------------- urgencyScore aggregate -----------------------

func TestUrgencyScore_SumsMessagesAndReplies(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{msgWithText("urgent thing?")},
		map[string][]goslack.Message{
			"1": {msgWithText("срочно нужно")},
		})
	got := urgencyScore(cu, time.Time{})
	want := (urgencyKeywordWeight + 1*urgencyQuestionWeight) + // top-level
		urgencyKeywordWeight // reply
	if got != want {
		t.Fatalf("urgencyScore = %d; want %d", got, want)
	}
}

func TestUrgencyScore_EmptyChannel(t *testing.T) {
	cu := mkChannelUnread("a", nil, nil)
	if got := urgencyScore(cu, fixedNow()); got != 0 {
		t.Fatalf("empty channel urgency = %d; want 0", got)
	}
}

// ----------------------- ranking interaction -----------------------

func TestRankUnread_MentionStillBeatsUrgency(t *testing.T) {
	mentioned := mkChannelUnread("m",
		[]goslack.Message{msgWithText("ping <@U_SELF>")}, nil)

	urgentBomb := mkChannelUnread("u", nil, nil)
	for i := 0; i < 100; i++ {
		urgentBomb.Messages = append(urgentBomb.Messages,
			msgWithText("urgent blocker critical срочно сломалось"))
	}

	if rankUnread(urgentBomb, "U_SELF", time.Time{}) >= rankUnread(mentioned, "U_SELF", time.Time{}) {
		t.Fatalf("a mention must outrank any amount of urgency in non-mention channels")
	}
}

func TestRankUnread_UrgencyBeatsRawVolume(t *testing.T) {
	noisy := mkChannelUnread("n", nil, nil)
	for i := 0; i < 30; i++ {
		noisy.Messages = append(noisy.Messages, msgWithText("plain status update"))
	}
	urgent := mkChannelUnread("u",
		[]goslack.Message{msgWithText("срочно сломалось критично????")}, nil)

	if rankUnread(urgent, "U_SELF", time.Time{}) <= rankUnread(noisy, "U_SELF", time.Time{}) {
		t.Fatalf("urgent low-volume channel must outrank noisy chat")
	}
}

func TestRankUnread_RecencyShiftsRanking(t *testing.T) {
	now := fixedNow()

	stale := mkChannelUnread("stale", nil, nil)
	{
		m := msgWithText("plain")
		m.Timestamp = tsOffset(-48 * time.Hour)
		stale.Messages = append(stale.Messages, m)
	}

	fresh := mkChannelUnread("fresh", nil, nil)
	{
		m := msgWithText("plain")
		m.Timestamp = tsOffset(-15 * time.Minute)
		fresh.Messages = append(fresh.Messages, m)
	}

	if rankUnread(fresh, "", now) <= rankUnread(stale, "", now) {
		t.Fatalf("fresh channel must rank above stale one of equal volume")
	}
}
