package tools

import "testing"

// The heading used to be "#"+ref, which doubled the sigil on a channel
// name that already carried one and stuck a channel sigil on a DM.
func TestConversationLabel(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"example-channel", "#example-channel"},
		{"#example-channel", "#example-channel"},
		{"@example.person", "@example.person"},
		{"U0EXAMPLE01", "@U0EXAMPLE01"},
		{"C0EXAMPLE01", "C0EXAMPLE01"},
		{"D0EXAMPLE01", "D0EXAMPLE01"},
		{"", ""},
	}
	for _, c := range cases {
		if got := conversationLabel(c.ref); got != c.want {
			t.Errorf("conversationLabel(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestConversationLabel_neverDoublesSigil(t *testing.T) {
	for _, ref := range []string{"example-channel", "#example-channel", "@example.person", "U0EXAMPLE01"} {
		got := conversationLabel(ref)
		if len(got) > 1 && (got[:2] == "##" || got[:2] == "#@") {
			t.Errorf("conversationLabel(%q) produced a doubled sigil: %q", ref, got)
		}
	}
}
