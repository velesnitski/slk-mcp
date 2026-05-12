package slack

import "testing"

func TestIsChannelID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Canonical IDs.
		{"C0ABC1234DE", true},
		{"C0XYZW9876", true},
		{"G0PRIVATE12", true},

		// Invalid: lowercase, too short, wrong prefix, DM prefix.
		{"c0abc1234de", false},
		{"C0", false},
		{"U0ABC1234DE", false}, // user id, not channel
		{"D0ABC1234DE", false}, // DM id, intentionally excluded
		{"general", false},
		{"#general", false},
		{"", false},
		{"C-WITH-DASH", false},
	}
	for _, c := range cases {
		if got := IsChannelID(c.in); got != c.want {
			t.Errorf("IsChannelID(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}
