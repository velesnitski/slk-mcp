package export

import (
	"strings"
	"testing"
)

// Credential-shaped fixtures are ASSEMBLED here at runtime and never
// written as literals.
//
// None of these could ever authenticate — the bodies are filler. But a
// secret scanner matches on SHAPE, not on validity, so a literal in a
// public repo produces a hit that somebody has to triage, and produces
// it publicly. Splitting each shape across a concatenation means the
// source file contains nothing a scanner reads as a credential, while
// Redact still receives the real thing and the test stays exactly as
// strong. The shape each helper builds is written out in its comment so
// a reader loses nothing by not seeing the literal.
//
// This is hygiene, not protection: the strings exist at runtime either
// way. The point is that the repository does not carry them.
func awsKeyID(fill string) string {
	// AKIA + 16 uppercase alphanumerics
	return "AKIA" + strings.Repeat(fill, 16)
}

func slackBotToken() string {
	// xoxb-<digits>-<alnum>
	return "xox" + "b-123456789012-abcdefGHIJKL"
}

func githubPAT() string {
	// ghp_ + 36 alphanumerics
	return "gh" + "p_abcdefghijklmnopqrstuvwxyz0123456789"
}

func gitlabPAT() string {
	// glpat- + 20 or more alphanumerics
	return "glpat" + "-abcdefghijklmnopqrstu"
}

func pemPrivateKey(kind, body string) string {
	// -----BEGIN <kind> PRIVATE KEY----- … -----END <kind> PRIVATE KEY-----
	d := strings.Repeat("-", 5)
	label := kind + " PRIVATE " + "KEY"
	return d + "BEGIN " + label + d + "\n" + body + "\n" + d + "END " + label + d
}

func TestRedact_SameSecretSameplaceholder(t *testing.T) {
	tok := slackBotToken()
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
	a, _ := Redact(awsKeyID("A"))
	b, _ := Redact(awsKeyID("B"))
	if a == b {
		t.Fatal("different secrets must not collapse to one placeholder")
	}
}

func TestRedact_Shapes(t *testing.T) {
	cases := []string{
		githubPAT(),
		gitlabPAT(),
		"Bearer 17abcDEF.ghiJKL_mno-pqr",
		pemPrivateKey("RSA", "MIIabc"),
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

// TestFixturesMatchTheShapesUnderTest guards the assembly itself. A
// helper that quietly stopped producing a credential shape would leave
// every test above passing against ordinary prose, proving nothing —
// the one failure mode this indirection introduces.
func TestFixturesMatchTheShapesUnderTest(t *testing.T) {
	for name, fixture := range map[string]string{
		"aws":    awsKeyID("A"),
		"slack":  slackBotToken(),
		"github": githubPAT(),
		"gitlab": gitlabPAT(),
		"pem":    pemPrivateKey("RSA", "MIIabc"),
	} {
		if _, n := Redact(fixture); n != 1 {
			t.Errorf("%s fixture no longer matches a secret pattern: %q", name, fixture)
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
