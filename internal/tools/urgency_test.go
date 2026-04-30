package tools

import (
	"reflect"
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
			got := messageUrgency(msgWithText(c.text), time.Time{}, urgencyOpts{})
			if got != c.want {
				t.Fatalf("text=%q: messageUrgency=%d; want %d", c.text, got, c.want)
			}
		})
	}
}

func TestMessageUrgency_FullWidthQuestionMarkCounts(t *testing.T) {
	got := messageUrgency(msgWithText("что？？"), time.Time{}, urgencyOpts{})
	want := 2 * urgencyQuestionWeight
	if got != want {
		t.Fatalf("CJK ？ should count: got %d, want %d", got, want)
	}
}

func TestMessageUrgency_KeywordEnglish(t *testing.T) {
	for _, kw := range []string{"urgent", "URGENT", "asap", "blocker", "critical", "important", "stuck"} {
		t.Run(kw, func(t *testing.T) {
			got := messageUrgency(msgWithText("hey we have a "+kw+" issue"), time.Time{}, urgencyOpts{})
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
			got := messageUrgency(msgWithText(c.text), time.Time{}, urgencyOpts{})
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
	got := messageUrgency(m, time.Time{}, urgencyOpts{})
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
			got := messageUrgency(m, now, urgencyOpts{})
			if got != c.want {
				t.Fatalf("ts=%s: got %d; want %d", c.ts, got, c.want)
			}
		})
	}
}

func TestMessageUrgency_ZeroNowDisablesRecency(t *testing.T) {
	m := msgWithText("recent body")
	m.Timestamp = tsOffset(-10 * time.Minute) // very recent
	if got := messageUrgency(m, time.Time{}, urgencyOpts{}); got != 0 {
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
	got := urgencyScore(cu, time.Time{}, urgencyOpts{})
	want := (urgencyKeywordWeight + 1*urgencyQuestionWeight) + // top-level
		urgencyKeywordWeight // reply
	if got != want {
		t.Fatalf("urgencyScore = %d; want %d", got, want)
	}
}

func TestUrgencyScore_EmptyChannel(t *testing.T) {
	cu := mkChannelUnread("a", nil, nil)
	if got := urgencyScore(cu, fixedNow(), urgencyOpts{}); got != 0 {
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

	if rankUnread(urgentBomb, "U_SELF", time.Time{}, urgencyOpts{}) >= rankUnread(mentioned, "U_SELF", time.Time{}, urgencyOpts{}) {
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

	if rankUnread(urgent, "U_SELF", time.Time{}, urgencyOpts{}) <= rankUnread(noisy, "U_SELF", time.Time{}, urgencyOpts{}) {
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

	if rankUnread(fresh, "", now, urgencyOpts{}) <= rankUnread(stale, "", now, urgencyOpts{}) {
		t.Fatalf("fresh channel must rank above stale one of equal volume")
	}
}

// ----------------------- urgency tuning (v0.2.8) -----------------------

func TestUrgencyOpts_Weight(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{msgWithText("urgent blocker критично")}, nil)
	// Three keywords × 10 = 30 raw; no other signals because timestamp empty.
	rawDefault := urgencyScore(cu, time.Time{}, urgencyOpts{})
	if rawDefault != 30 {
		t.Fatalf("default weight: urgencyScore = %d; want 30", rawDefault)
	}

	cases := []struct {
		name   string
		weight float64
		want   int
	}{
		{"explicit 1.0 == default", 1.0, 30},
		{"halve", 0.5, 15},
		{"double", 2.0, 60},
		{"zero falls back to default", 0, 30},
		{"negative falls back to default", -1, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := urgencyScore(cu, time.Time{}, urgencyOpts{Weight: c.weight})
			if got != c.want {
				t.Fatalf("weight=%v: got %d; want %d", c.weight, got, c.want)
			}
		})
	}
}

func TestUrgencyOpts_ExtraKeywordsAreAdditive(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{msgWithText("p0 incident in prod")}, nil)
	// "p0" and "incident" aren't in built-in list; without extras, score=0.
	if got := urgencyScore(cu, time.Time{}, urgencyOpts{}); got != 0 {
		t.Fatalf("expected 0 with no extras; got %d", got)
	}
	got := urgencyScore(cu, time.Time{}, urgencyOpts{
		ExtraKeywords: []string{"p0", "incident"},
	})
	want := 2 * urgencyKeywordWeight
	if got != want {
		t.Fatalf("extras matched: got %d; want %d", got, want)
	}
}

func TestUrgencyOpts_ExtraKeywordsCoexistWithBuiltins(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{msgWithText("urgent prod down, p0 incident")}, nil)
	got := urgencyScore(cu, time.Time{}, urgencyOpts{
		ExtraKeywords: []string{"prod down", "p0", "incident"},
	})
	// Built-ins: "urgent". Extras: "prod down", "p0", "incident".
	want := 4 * urgencyKeywordWeight
	if got != want {
		t.Fatalf("got %d; want %d", got, want)
	}
}

func TestUrgencyOpts_ExtraKeywordsSkipEmpty(t *testing.T) {
	cu := mkChannelUnread("a",
		[]goslack.Message{msgWithText("hello world")}, nil)
	// Empty / whitespace extras must not match anything (which would
	// match every message because strings.Contains(_, "") is always
	// true).
	got := urgencyScore(cu, time.Time{}, urgencyOpts{
		ExtraKeywords: []string{"", "   "},
	})
	if got != 0 {
		t.Fatalf("empty extras must be ignored, got %d", got)
	}
}

func TestParseExtraKeywords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{",,,", nil},
		{"asap", []string{"asap"}},
		{"ASAP, Critical, P0", []string{"asap", "critical", "p0"}},
		{"  prod down ,  ,  fire-now  ", []string{"prod down", "fire-now"}},
		{"внимание, тревога", []string{"внимание", "тревога"}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := parseExtraKeywords(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseExtraKeywords(%q) = %v; want %v", c.in, got, c.want)
			}
		})
	}
}

func TestMessageUrgency_LogSeverityKeywords(t *testing.T) {
	// Bot-driven log/alert channels (monitoring, ci, registry, cloud)
	// publish English-only severity terms. The built-in keyword list
	// must catch them without the operator having to pass urgency_keywords.
	cases := []struct {
		text    string
		minHits int // we assert >= rather than == because some
		// terms appear in pairs ("error errors") in the wild.
	}{
		{"GitLab pipeline #1234 failed on stage build", 1},
		{"Zabbix trigger: ERROR — service unreachable", 1},
		{"FATAL: connection refused", 1},
		{"Harbor alert: image scan exception", 2}, // alert + exception
		{"AWS outage detected in us-east-1", 1},
		{"connection timed out after 30s", 1},
		{"panic: runtime error: invalid memory address", 2}, // panic + error
		// Ru log-style: not pure en, but should still fire.
		{"приложение не отвечает 5 минут", 1},
		// Negative: routine info should NOT score.
		{"GitLab pipeline #1234 succeeded on stage build", 0},
		{"merged MR !42 by alex", 0},
	}
	for _, c := range cases {
		t.Run(c.text, func(t *testing.T) {
			got := messageUrgency(msgWithText(c.text), time.Time{}, urgencyOpts{})
			wantMin := c.minHits * urgencyKeywordWeight
			if c.minHits == 0 {
				if got != 0 {
					t.Fatalf("text=%q: got %d; want 0 (no severity match)", c.text, got)
				}
				return
			}
			if got < wantMin {
				t.Fatalf("text=%q: got %d; want >= %d", c.text, got, wantMin)
			}
		})
	}
}

func TestRankUnread_TuningBeatsBuiltinDefault(t *testing.T) {
	// Two channels: one matched only by an extra keyword ("p0"), one
	// with three plain messages. Without the extra keyword, the noisy
	// plain channel wins. With the extra keyword, the p0 channel wins.
	noise := mkChannelUnread("noise", nil, nil)
	for i := 0; i < 5; i++ {
		noise.Messages = append(noise.Messages, msgWithText("status update"))
	}
	p0 := mkChannelUnread("p0",
		[]goslack.Message{msgWithText("we have a p0")}, nil)

	noExtra := urgencyOpts{}
	withExtra := urgencyOpts{ExtraKeywords: []string{"p0"}}

	if rankUnread(p0, "", time.Time{}, noExtra) >= rankUnread(noise, "", time.Time{}, noExtra) {
		t.Fatalf("baseline: p0 should NOT outrank noise without the extra keyword")
	}
	if rankUnread(p0, "", time.Time{}, withExtra) <= rankUnread(noise, "", time.Time{}, withExtra) {
		t.Fatalf("with extra keyword: p0 must outrank noise")
	}
}
