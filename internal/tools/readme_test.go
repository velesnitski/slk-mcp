package tools

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ADR 135: the README is the tool catalogue people read first, so every
// registered tool appears in it and the badge counts them.
func TestREADME_ListsEveryTool(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("*.go")
	names := map[string]bool{}
	toolRe := regexp.MustCompile(`mcp\.NewTool\("([a-z_]+)"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range toolRe.FindAllStringSubmatch(string(src), -1) {
			names[m[1]] = true
		}
	}
	for n := range names {
		if !strings.Contains(string(readme), "`"+n+"`") {
			t.Errorf("README does not list tool %s", n)
		}
	}
	badge := regexp.MustCompile(`tools-(\d+)-purple`).FindStringSubmatch(string(readme))
	if badge == nil || badge[1] != strconv.Itoa(len(names)) {
		t.Errorf("README tools badge = %v, want %d", badge, len(names))
	}
}
