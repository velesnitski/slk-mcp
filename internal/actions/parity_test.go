package actions

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"
)

// TestSharedDeadlineVectors reads the phrase table wispr-mcp also tests
// (its tests/data/deadline_vectors.tsv): the same commitment said in a
// meeting and in Slack must get the same due date, or a merged action
// list shows it twice with two deadlines. Only the date is compared;
// wispr-mcp also flags fuzzy spans as approximate.
func TestSharedDeadlineVectors(t *testing.T) {
	f, err := os.Open("testdata/deadline_vectors.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		col := strings.Split(line, "\t")
		anchor, err := time.ParseInLocation("2006-01-02", col[1], time.UTC)
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		got := "-"
		if d := Due(col[0], anchor.Add(10*time.Hour)); !d.Day.IsZero() {
			got = d.Day.Format("2006-01-02")
		}
		if got != col[2] {
			t.Errorf("%q said %s: got %s, want %s", col[0], col[1], got, col[2])
		}
	}
}
