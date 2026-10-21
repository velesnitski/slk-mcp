package format

import "testing"

func TestLinkText(t *testing.T) {
	const doc = "https://docs.example.com/document/d/1abcDEFghiJKLmnoPQRstu/edit?tab=t.0"
	cases := []struct{ url, label, want string }{
		// Slack's display of a long pasted link: the id is elided.
		{doc, "docs.example.com/document/d/1abcDEF…/edit?tab=t.0", "docs.example.com/document/d/1abcDEFghiJKLmnoPQRstu/edit?tab=t.0"},
		// The label is the URL itself.
		{"https://example.com/a", "example.com/a", "example.com/a"},
		{"https://example.com/a", "https://example.com/a", "example.com/a"},
		// Real text stays.
		{doc, "the analysis", "the analysis"},
		{"https://example.com/issue/ABC-1", "ABC-1", "ABC-1"},
		// An ellipsis that does not abbreviate this URL is text.
		{doc, "see the doc…", "see the doc…"},
		{doc, "other.example.com/…/edit?tab=t.0", "other.example.com/…/edit?tab=t.0"},
	}
	for _, c := range cases {
		if got := linkText(c.url, c.label); got != c.want {
			t.Errorf("linkText(%q, %q) = %q, want %q", c.url, c.label, got, c.want)
		}
	}
}

func TestRenderText_KeepsTheIdOfAnElidedLink(t *testing.T) {
	in := "short analysis <https://docs.example.com/document/d/1abcDEFghiJKLmnoPQRstu/edit|docs.example.com/document/d/1abcDEF…/edit>"
	want := "short analysis docs.example.com/document/d/1abcDEFghiJKLmnoPQRstu/edit"
	if got := RenderText(in, nil); got != want {
		t.Fatalf("RenderText = %q, want %q", got, want)
	}
}
