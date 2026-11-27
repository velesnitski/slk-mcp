package main

import (
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// manifestLinkRe finds pre-filled "create app" links in the docs.
var manifestLinkRe = regexp.MustCompile(`https://api\.slack\.com/apps\?new_app=1&manifest_yaml=([^)\s"'` + "`" + `]+)`)

// compactManifest is a manifest without comment and blank lines – what
// `make manifest-url` encodes, short enough for a link.
func compactManifest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, l := range strings.Split(string(b), "\n") {
		if s := strings.TrimSpace(l); s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		out.WriteString(l + "\n")
	}
	return out.String()
}

// TestManifestLinksMatchManifests keeps the one-click links in the docs
// equal to the manifests they encode (ADR 157): a scope added to a
// manifest without regenerating the link fails here.
func TestManifestLinksMatchManifests(t *testing.T) {
	want := map[string]bool{
		compactManifest(t, "slack-app-manifest.yaml"):          false,
		compactManifest(t, "slack-app-manifest.readonly.yaml"): false,
	}
	for _, doc := range []string{"docs/INSTALL.md", "README.md"} {
		b, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range manifestLinkRe.FindAllStringSubmatch(string(b), -1) {
			got, err := url.QueryUnescape(m[1])
			if err != nil {
				t.Errorf("%s: undecodable link: %v", doc, err)
				continue
			}
			if _, ok := want[got]; !ok {
				t.Errorf("%s: a create-app link matches no manifest; run make manifest-url and replace it", doc)
				continue
			}
			want[got] = true
		}
	}
	for m, seen := range want {
		if !seen {
			t.Errorf("no create-app link in the docs for the manifest named %q", strings.SplitN(strings.SplitN(m, "name: ", 2)[1], "\n", 2)[0])
		}
	}
}
