package export

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

// Redaction replaces credential-shaped spans with a stable hash rather
// than a blank marker.
//
// A corpus is re-read later, so blanking destroys structure: two
// occurrences of "[redacted]" cannot be told apart. Hashing keeps the
// only property that matters analytically -- that these two spans are
// the SAME secret, seen here and there, then and later -- while the
// secret itself never reaches disk.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`xox[bpasr]-[0-9]{9,}-[0-9A-Za-z-]{10,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`sk-[A-Za-z0-9]{32,}`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-|]{16,}`),
}

// Redact replaces every credential-shaped span and reports how many it
// replaced. Pure.
//
// Deliberately limited to high-confidence SHAPES. Prose secrets ("пароль
// от рута: ...") are not matched: a heuristic loose enough to catch them
// would rewrite ordinary sentences, and silently corrupting the corpus
// is worse than leaving a span a human reader can still find.
func Redact(text string) (string, int) {
	n := 0
	out := text
	for _, re := range secretPatterns {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			n++
			return placeholder(m)
		})
	}
	return out, n
}

func placeholder(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return "[secret:sha256:" + hex.EncodeToString(sum[:])[:12] + "]"
}
