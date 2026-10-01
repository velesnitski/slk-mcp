package tools

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// ADR 114: self-contained pages keep their content in non-executable
// script blocks or data-* attributes. The converter must keep content and
// drop code — and must not let one element's closer end another.

func TestHTMLToText_ScriptIsNotClosedByStyleCloserInsideIt(t *testing.T) {
	// A bundle that builds a stylesheet string contains "</style>". The
	// combined (script|style) pattern ended the script there and the rest
	// of the code leaked out as "text".
	page := `<html><body><p>Hello</p><script>var css = "<style>a{}</style>"; function secretBundleCode(){ return 42 }</script></body></html>`

	got := htmlToText(page)

	if strings.Contains(got, "secretBundleCode") {
		t.Fatalf("script source leaked into the text: %q", got)
	}
	tmWant(t, got, "Hello")
}

func TestHTMLToText_JSONScriptPayloadIsKept(t *testing.T) {
	page := `<html><body><div id="app"></div>` +
		`<script type="application/json" id="doc">{"title":"Startup timings","rows":[{"step":"db","seconds":41}]}</script>` +
		`<script>render(document.getElementById("doc"))</script></body></html>`

	got := htmlToText(page)

	tmWant(t, got, "Startup timings", `"seconds":41`, "[embedded application/json]")
	if strings.Contains(got, "render(") {
		t.Fatalf("executable script must still be dropped: %q", got)
	}
}

func TestHTMLToText_MarkdownScriptPayloadIsKept(t *testing.T) {
	page := `<body><script type="text/markdown"># Report

Cold start takes 9 minutes.</script></body>`

	tmWant(t, htmlToText(page), "# Report", "Cold start takes 9 minutes.")
}

func TestHTMLToText_Base64DataAttributeIsDecoded(t *testing.T) {
	doc := strings.Repeat("Local stack boot measured step by step. ", 8)
	enc := base64.StdEncoding.EncodeToString([]byte(doc))
	page := `<body><div id="root" data-payload="` + enc + `"></div></body>`

	tmWant(t, htmlToText(page), "Local stack boot measured step by step.", "[embedded data attribute]")
}

func TestHTMLToText_ShortDataAttributesAreIgnored(t *testing.T) {
	page := `<body><div data-id="row-12" data-theme="dark">Visible</div></body>`

	got := htmlToText(page)

	if strings.Contains(got, "embedded") || strings.Contains(got, "row-12") {
		t.Fatalf("short data-* values are markup, not content: %q", got)
	}
	tmWant(t, got, "Visible")
}

func TestHTMLToText_JavaScriptShellSaysSo(t *testing.T) {
	page := `<html><body><div id="app"></div><script>` + strings.Repeat("x=1;", 8000) + `</script></body></html>`

	tmWant(t, htmlToText(page), "renders its content with JavaScript", "keep_file")
}

func TestHTMLToText_OrdinaryPageUnchanged(t *testing.T) {
	page := `<html><head><style>p{color:red}</style><script>track()</script></head><body><h1>Title</h1><p>Body &amp; more</p></body></html>`

	got := htmlToText(page)

	if got != "Title\nBody & more" {
		t.Fatalf("plain page rendering changed: %q", got)
	}
}

func TestReadDocument_KeepFileReturnsTheRawBytes(t *testing.T) {
	// The converter can only guess; keep_file hands back the file itself,
	// byte for byte, so the caller can open it with the right tool.
	f := newFakeSlack(t)
	raw := `<html><body><script>build()</script><div data-x="1">raw page</div></body></html>`
	file := dcDoc(f, "F0KEEP0001", "viewer.html", "text/html", raw)
	f.On("files.info", jsonBody(t, map[string]any{"ok": true, "file": file}))
	s := tmServer(t, newFakeHub(t, f).registerDocumentTools)

	out := resultText(tmCall(t, s, "read_document", map[string]any{
		"permalink": tmHost + "/files/U0OWNER01/F0KEEP0001/viewer.html",
		"keep_file": true,
	}))

	tmWant(t, out, "1 file(s) kept", "saved to: ", "not redacted")
	path := strings.TrimSpace(out[strings.Index(out, "saved to: ")+len("saved to: "):])
	path = strings.SplitN(path, "\n", 2)[0]
	defer os.Remove(path)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the kept file must exist at the returned path: %v", err)
	}
	if string(got) != raw {
		t.Fatalf("keep_file must return the bytes unchanged, got %q", got)
	}
}
