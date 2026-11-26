package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scopeRe matches the OAuth scopes the code names in hints and errors.
var scopeRe = regexp.MustCompile(`\b(?:channels|groups|im|mpim|users|users\.profile|usergroups|search|chat|reactions|canvases|files|dnd|lists):[a-z.]+\b`)

// botOnlyAlternatives are scopes the code mentions as the bot-token way to
// do something the manifest already grants on the user token.
var botOnlyAlternatives = map[string]string{
	"channels:manage": "channels:write",
}

// writeScopes in the read-only manifest are refused, except im:write, which
// only opens a DM for transcribe_audio latest-mode.
var writeScopeRe = regexp.MustCompile(`:write\b|^chat:|^dnd:|^users:write$|^users\.profile:write$`)

func manifestScopes(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^\s*-\s+([a-z.]+:[a-z.]+)\s*$`)
	out := map[string]bool{}
	for _, m := range line.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatalf("%s: no scopes parsed", path)
	}
	return out
}

func codeScopes(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	err := filepath.WalkDir("internal", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, s := range scopeRe.FindAllString(string(b), -1) {
			out[s] = append(out[s], p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestManifestCoversCodeScopes keeps slack-app-manifest.yaml in step with
// the code: a scope a tool asks for must be in the manifest people install.
func TestManifestCoversCodeScopes(t *testing.T) {
	granted := manifestScopes(t, "slack-app-manifest.yaml")
	for scope, files := range codeScopes(t) {
		if alt, ok := botOnlyAlternatives[scope]; ok {
			if !granted[alt] {
				t.Errorf("%s (named in %s) needs its user-token equivalent %s in the manifest", scope, files[0], alt)
			}
			continue
		}
		if !granted[scope] {
			t.Errorf("scope %s is used in %s but missing from slack-app-manifest.yaml", scope, files[0])
		}
	}
}

func TestReadOnlyManifestHasNoWriteScopes(t *testing.T) {
	full := manifestScopes(t, "slack-app-manifest.yaml")
	for scope := range manifestScopes(t, "slack-app-manifest.readonly.yaml") {
		if !full[scope] {
			t.Errorf("read-only manifest grants %s, which the full manifest does not", scope)
		}
		if scope != "im:write" && writeScopeRe.MatchString(scope) {
			t.Errorf("read-only manifest grants write scope %s", scope)
		}
	}
}

func TestManifestsDisableTokenRotation(t *testing.T) {
	for _, p := range []string{"slack-app-manifest.yaml", "slack-app-manifest.readonly.yaml"} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "token_rotation_enabled: false") {
			t.Errorf("%s: token rotation must stay off (tokens are read once at start)", p)
		}
	}
}
