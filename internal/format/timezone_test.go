package format

import (
	"regexp"
	"testing"
)

func TestTimeZoneNote_NamesTheZoneAndOffset(t *testing.T) {
	// Rendered clock times carry no zone. Correlating them against UTC
	// logs or a provider's timestamps is guesswork without this line.
	got := TimeZoneNote()
	re := regexp.MustCompile(`^times: \S+ \(UTC[+-]\d{2}:\d{2}\)$`)
	if !re.MatchString(got) {
		t.Fatalf("note must name zone and signed offset; got %q", got)
	}
}
