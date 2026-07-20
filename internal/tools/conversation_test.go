package tools

import "testing"

func TestClassifyConversationRef(t *testing.T) {
	cases := []struct {
		in       string
		wantKind convRefKind
		wantTok  string
	}{
		// DMs by handle — leading '#' tolerated (digests prefix everything).
		{"@jbravo", refHandle, "@jbravo"},
		{"#@jbravo", refHandle, "@jbravo"},
		{"  @jbravo ", refHandle, "@jbravo"},

		// DMs by bare user id — the shape unread-summary DM headers print.
		{"U0ABC1234DE", refUserID, "U0ABC1234DE"},
		{"#U0ABC1234DE", refUserID, "U0ABC1234DE"},
		{"W0ABC1234DE", refUserID, "W0ABC1234DE"}, // enterprise-grid ids

		// Channels: names and canonical conversation ids stay ResolveID's
		// problem (it already short-circuits C/G/D ids).
		{"general", refChannel, "general"},
		{"#general", refChannel, "#general"},
		{"C0ABC1234DE", refChannel, "C0ABC1234DE"},
		{"D0ABCDEF123", refChannel, "D0ABCDEF123"},
		{"", refChannel, ""},

		// Lowercase u… is a name, not an id.
		{"u0abc1234de", refChannel, "u0abc1234de"},
	}
	for _, c := range cases {
		kind, tok := classifyConversationRef(c.in)
		if kind != c.wantKind || tok != c.wantTok {
			t.Errorf("classifyConversationRef(%q) = (%v, %q); want (%v, %q)",
				c.in, kind, tok, c.wantKind, c.wantTok)
		}
	}
}
