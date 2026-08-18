package export

import (
	"strings"
	"testing"
)

func TestRedact_SameSecretSameplaceholder(t *testing.T) {
	tok := "xoxb-123456789012-abcdefGHIJKL" // sweep:allow — synthetic, must match the shape under test
	a, na := Redact("deploy with " + tok)
	b, nb := Redact("still broken, token " + tok + " again")
	if na != 1 || nb != 1 {
		t.Fatalf("want 1 replacement each; got %d and %d", na, nb)
	}
	if strings.Contains(a, tok) || strings.Contains(b, tok) {
		t.Fatal("secret must not survive in the output")
	}
	// The whole point of hashing over blanking: two sightings of the same
	// secret stay linkable in the corpus.
	pa := a[strings.Index(a, "[secret:"):]
	pb := b[strings.Index(b, "[secret:") : strings.Index(b, "]")+1]
	if !strings.HasPrefix(pa, pb) {
		t.Fatalf("same secret must hash identically: %q vs %q", pa, pb)
	}
}

func TestRedact_DistinctSecretsDiffer(t *testing.T) {
	a, _ := Redact("AKIAAAAAAAAAAAAAAAAA") // sweep:allow
	b, _ := Redact("AKIABBBBBBBBBBBBBBBB") // sweep:allow
	if a == b {
		t.Fatal("different secrets must not collapse to one placeholder")
	}
}

func TestRedact_Shapes(t *testing.T) {
	cases := []string{
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789", // sweep:allow
		"glpat-abcdefghijklmnopqrstu",              // sweep:allow
		"Bearer 17abcDEF.ghiJKL_mno-pqr",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----", // sweep:allow
	}
	for _, c := range cases {
		got, n := Redact("before " + c + " after")
		if n != 1 {
			t.Fatalf("want 1 replacement for %.20q; got %d", c, n)
		}
		if !strings.HasPrefix(got, "before ") || !strings.HasSuffix(got, " after") {
			t.Fatalf("surrounding text must survive: %q", got)
		}
	}
}

func TestRedact_LeavesOrdinaryTextAlone(t *testing.T) {
	in := "deployed to staging, see MR !42 and the sk- prefix discussion"
	got, n := Redact(in)
	if n != 0 || got != in {
		t.Fatalf("ordinary prose must pass through untouched; got %d, %q", n, got)
	}
}
