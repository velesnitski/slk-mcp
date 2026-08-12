package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func docFile(name, mimetype, filetype string) goslack.File {
	f := goslack.File{}
	f.ID = "F1"
	f.Name = name
	f.Mimetype = mimetype
	f.Filetype = filetype
	f.URLPrivateDownload = "https://example.invalid/f"
	return f
}

func TestIsDocumentFile(t *testing.T) {
	yes := []goslack.File{
		docFile("proposal.html", "text/html", "html"),
		docFile("spec.md", "text/markdown", "markdown"),
		docFile("notes.txt", "text/plain", "text"),
		docFile("rows.csv", "text/csv", "csv"),
		docFile("payload.json", "application/json", "json"),
		// Slack snippets often arrive with no mimetype at all.
		docFile("snippet", "", "python"),
	}
	for _, f := range yes {
		if !isDocumentFile(f) {
			t.Errorf("%s (%q/%q) should be a document", f.Name, f.Mimetype, f.Filetype)
		}
	}
	no := []goslack.File{
		docFile("clip.m4a", "audio/mp4", "m4a"),
		docFile("pic.png", "image/png", "png"),
		docFile("movie.mp4", "video/mp4", "mp4"),
		docFile("archive.zip", "application/zip", "zip"),
		{},
	}
	for _, f := range no {
		if isDocumentFile(f) {
			t.Errorf("%s (%q/%q) must not be a document", f.Name, f.Mimetype, f.Filetype)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	in := `<!doctype html><html><head><title>T</title>
<style>body{color:red}</style><script>var x = "<p>not prose</p>";</script></head>
<body><!-- hidden --><h1>Proposal</h1><p>First&nbsp;line &amp; more</p>
<ul><li>one</li><li>two</li></ul></body></html>`
	got := htmlToText(in)
	for _, want := range []string{"Proposal", "First line & more", "one", "two"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output, got:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"color:red", "var x", "hidden", "<p>", "&amp;"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("did not expect %q in output, got:\n%s", unwanted, got)
		}
	}
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("blank runs should collapse, got:\n%q", got)
	}
}

func TestDocumentToText_SniffsHTMLDespiteMimetype(t *testing.T) {
	// Exported pages are frequently served as text/plain; the body decides.
	body := "<!DOCTYPE html><html><body><p>Hello</p></body></html>"
	got := documentToText(body, "text/plain")
	if got != "Hello" {
		t.Fatalf("HTML body should be flattened regardless of mimetype, got %q", got)
	}
	// Plain text must survive untouched, angle brackets and all.
	plain := "a < b and c > d\n"
	if got := documentToText(plain, "text/plain"); got != "a < b and c > d" {
		t.Fatalf("plain text must pass through, got %q", got)
	}
}

func TestTruncateText(t *testing.T) {
	if got, cut := truncateText("abc", 10); got != "abc" || cut {
		t.Fatalf("short text must pass through untouched, got %q cut=%v", got, cut)
	}
	// Multi-byte input must not be cut mid-rune.
	got, cut := truncateText("привет", 3)
	if !cut || got != "при" {
		t.Fatalf("want first 3 runes cut=true, got %q cut=%v", got, cut)
	}
	if got, cut := truncateText("abc", 0); got != "abc" || cut {
		t.Fatalf("a non-positive cap disables truncation, got %q cut=%v", got, cut)
	}
}

func TestHeadIsSignInPage(t *testing.T) {
	signIn := `<!DOCTYPE html><html><head><title>Slack</title></head>
<body><form id="signin_form">Sign in to your workspace</form></body></html>`
	if !headIsSignInPage(signIn) {
		t.Fatal("Slack's sign-in page should be detected")
	}
	// A genuine HTML attachment must NOT read as a sign-in page, otherwise
	// read_document rejects exactly the files it exists to read.
	proposal := `<!doctype html><html><body><h1>Proposal</h1><p>Scope and cost.</p></body></html>`
	if headIsSignInPage(proposal) {
		t.Fatal("a real HTML document must not be mistaken for the sign-in page")
	}
	if headIsSignInPage("just some text, sign in to nothing") {
		t.Fatal("non-HTML content is never the sign-in page")
	}
}

func TestDownloadFiles_HTMLDocumentIsNotAScopeError(t *testing.T) {
	// Regression: the sign-in guard rejects any HTML body, which is right
	// for audio/video but would reject a legitimate .html attachment.
	body := `<!doctype html><html><body><h1>Proposal</h1></body></html>`
	fake := &fakeAudioClient{payload: []byte(body)}
	f := docFile("proposal.html", "text/html", "html")

	saved, _, err := downloadFiles(context.Background(), fake, []goslack.File{f}, t.TempDir(), "slk-doc", isDocumentFile)
	if err != nil {
		t.Fatalf("an HTML document must download cleanly, got %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("expected the document to be saved, got %d files", len(saved))
	}
}

func TestDownloadFiles_SignInPageStillRejectedForDocuments(t *testing.T) {
	body := `<!DOCTYPE html><html><body>Sign in to your workspace</body></html>`
	fake := &fakeAudioClient{payload: []byte(body)}
	f := docFile("proposal.html", "text/html", "html")

	if _, _, err := downloadFiles(context.Background(), fake, []goslack.File{f}, t.TempDir(), "slk-doc", isDocumentFile); err == nil {
		t.Fatal("a sign-in page must still be reported as a scope failure")
	}
}

func docMsg(ts string, files ...goslack.File) goslack.Message {
	m := goslack.Message{}
	m.Timestamp = ts
	m.Files = files
	return m
}

func TestCollectDocuments(t *testing.T) {
	proposal := docFile("quarterly-proposal.html", "text/html", "html")
	playbook := docFile("team playbook.html", "text/html", "html")
	pic := docFile("shot.png", "image/png", "png")
	// Newest first, as RecentFileMessages delivers.
	msgs := []goslack.Message{
		docMsg("300.0", proposal),
		docMsg("200.0", playbook, pic),
	}

	all := collectDocuments(msgs, "")
	if len(all) != 2 {
		t.Fatalf("want both documents (image skipped), got %d", len(all))
	}
	if all[0].File.Name != "quarterly-proposal.html" || all[0].TS != "300.0" {
		t.Fatalf("newest document should come first, got %+v", all[0])
	}

	// The whole point: reach the EARLIER of two documents by name.
	got := collectDocuments(msgs, "playbook")
	if len(got) != 1 || got[0].File.Name != "team playbook.html" {
		t.Fatalf("match should select the earlier document, got %+v", got)
	}
	if got[0].TS != "200.0" {
		t.Fatalf("candidate must carry its message ts, got %q", got[0].TS)
	}
	// Matching is case-insensitive and returns nothing when absent.
	if len(collectDocuments(msgs, "PROPOSAL")) != 1 {
		t.Fatal("match should be case-insensitive")
	}
	if len(collectDocuments(msgs, "nope")) != 0 {
		t.Fatal("a non-matching needle must select nothing")
	}
}

func TestRenderDocumentList(t *testing.T) {
	f := docFile("team playbook.html", "text/html", "html")
	f.Size = 4096
	out := renderDocumentList([]docCandidate{{File: f, TS: "200.0"}}, " [primary]")
	for _, want := range []string{"team playbook.html", "text/html", "4096", "ts=200.0", "[primary]"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in listing, got:\n%s", want, out)
		}
	}
}

func TestIsPDFAndReadableDocument(t *testing.T) {
	byMime := docFile("deck.pdf", "application/pdf", "")
	byType := docFile("deck", "", "pdf")
	if !isPDFFile(byMime) || !isPDFFile(byType) {
		t.Fatal("a PDF must be detected by mimetype and by Slack filetype")
	}
	// A PDF is readable but must NOT be treated as text: the sign-in
	// guard stays strict for it, and it is never flattened.
	if isDocumentFile(byMime) {
		t.Fatal("a PDF must not count as a text document")
	}
	if !isReadableDocument(byMime) {
		t.Fatal("read_document must accept PDFs")
	}
	if expectsTextBody(byMime) {
		t.Fatal("a PDF does not expect a text body; the strict HTML guard must apply")
	}
	// Text documents keep working, images stay out.
	if !isReadableDocument(docFile("spec.md", "text/markdown", "markdown")) {
		t.Fatal("markdown must remain readable")
	}
	if isReadableDocument(docFile("pic.png", "image/png", "png")) {
		t.Fatal("images are view_image's job")
	}
}

func TestRenderDocuments_PDFIsReportedByPathAndKept(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "slk-doc-F1-deck.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.7 binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(dir, "slk-doc-F2-notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := renderDocuments([]savedFile{
		{Path: pdf, Mimetype: "application/pdf", Size: 15},
		{Path: txt, Mimetype: "text/plain", Size: 5},
	}, " [secondary]", 100)

	if !strings.Contains(out, pdf) {
		t.Fatalf("the PDF path must be reported so it can be opened, got:\n%s", out)
	}
	if strings.Contains(out, "%PDF-1.7") {
		t.Fatalf("PDF bytes must never be inlined, got:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("text documents must still render inline, got:\n%s", out)
	}
	// The PDF survives the call; the text file does not.
	if _, err := os.Stat(pdf); err != nil {
		t.Fatalf("the PDF must stay on disk for the caller: %v", err)
	}
	if _, err := os.Stat(txt); !os.IsNotExist(err) {
		t.Fatalf("a rendered text file must be cleaned up, stat err = %v", err)
	}
}
