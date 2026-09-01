package format

import (
	"reflect"
	"strings"
	"testing"
)

func TestCollectRefIDsInText_BothForms(t *testing.T) {
	// A canvas mixes markup that survived with markup that did not: the
	// HTML flattening strips angle brackets, so the same document can
	// carry both spellings.
	in := "kickoff <@U0AAA1111AA> and @U0BBB2222BB, notes in <#C0CCC3333CC|planning> " +
		"and #C0DDD4444DD, cc <@U0AAA1111AA> again"
	got := CollectRefIDsInText(in)
	want := []string{"U0AAA1111AA", "C0CCC3333CC", "U0BBB2222BB", "C0DDD4444DD"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCollectRefIDsInText_NoFalsePositives(t *testing.T) {
	for _, in := range []string{
		"", "no refs here at all",
		"email me at name@example.invalid",
		"#123 and @bob and #release-notes",   // not Slack IDs
		"@U0AB is too short to be an ID",     // below the length floor
		"version @V1ABCDEFGHIJ is not a ref", // wrong prefix letter
	} {
		if got := CollectRefIDsInText(in); len(got) != 0 {
			t.Errorf("CollectRefIDsInText(%q) = %v, want none", in, got)
		}
	}
}

func TestRenderCanvasText_ResolvesBothForms(t *testing.T) {
	refs := map[string]string{
		"U0AAA1111AA": "Ada Lovelace",
		"U0BBB2222BB": "Grace Hopper",
		"C0CCC3333CC": "planning",
	}
	in := "owner <@U0AAA1111AA>, reviewer @U0BBB2222BB, thread in <#C0CCC3333CC> / #C0CCC3333CC"
	got := RenderCanvasText(in, refs)

	for _, want := range []string{"@Ada Lovelace", "@Grace Hopper", "#planning"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "U0AAA1111AA") || strings.Contains(got, "U0BBB2222BB") {
		t.Errorf("raw ids survived: %q", got)
	}
}

func TestRenderCanvasText_UnknownIDIsLeftAlone(t *testing.T) {
	// Never invent a name for an id we could not resolve — a wrong name
	// is worse than a raw id, because it reads as fact.
	in := "assigned to @U0EEE5555EE"
	if got := RenderCanvasText(in, map[string]string{"U0AAA1111AA": "Ada Lovelace"}); got != in {
		t.Fatalf("unknown id must pass through unchanged, got %q", got)
	}
}

func TestRenderCanvasText_LeavesOrdinaryProseAlone(t *testing.T) {
	in := "Discussed pricing with @bob, see #release-notes, budget is $500 (up 12%)."
	if got := RenderCanvasText(in, map[string]string{"U0AAA1111AA": "Ada Lovelace"}); got != in {
		t.Fatalf("prose must survive untouched, got %q", got)
	}
}

func TestRenderText_UnchangedByTheCanvasPass(t *testing.T) {
	// The bare-mention pass must stay out of message rendering, which
	// every other read surface depends on.
	refs := map[string]string{"U0AAA1111AA": "Ada Lovelace"}
	in := "ping @U0AAA1111AA about it"
	if got := RenderText(in, refs); got != in {
		t.Fatalf("RenderText must not resolve bare mentions, got %q", got)
	}
}
