package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// dcImageBytes is a small, deterministic non-HTML payload standing in
// for picture bytes — downloadFiles only cares that it does not open
// like an HTML sign-in page.
var dcImageBytes = "\x89PNG\r\n\x1a\n dc test pixels"

func TestViewImage_InlinesThePicture(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "photo.png", "image/png", dcImageBytes)))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "view_image", map[string]any{"channel": "alpha"})

	if res.IsError {
		t.Fatalf("view_image failed: %s", res.Text())
	}
	summary := res.Text()
	if !strings.HasPrefix(summary, "1 image(s):") {
		t.Errorf("summary must lead with the count:\n%s", summary)
	}
	if !strings.Contains(summary, "photo.png") || !strings.Contains(summary, "inlined below") {
		t.Errorf("summary must name the picture and say it was inlined:\n%s", summary)
	}

	var images []dcContent
	for _, c := range res.Content {
		if c.Type == "image" {
			images = append(images, c)
		}
	}
	if len(images) != 1 {
		t.Fatalf("expected exactly one inline image, got %d (%+v)", len(images), res.Content)
	}
	if images[0].MIMEType != "image/png" {
		t.Errorf("mimetype = %q, want image/png", images[0].MIMEType)
	}
	if want := base64.StdEncoding.EncodeToString([]byte(dcImageBytes)); images[0].Data != want {
		t.Errorf("inline data does not match the downloaded bytes")
	}
	// The text block comes first so the result reads sensibly in a client
	// that lists content blocks linearly.
	if res.Content[0].Type != "text" {
		t.Errorf("first content block is %q, want text", res.Content[0].Type)
	}
	// Bytes are in the response now; the scratch file must be gone.
	if _, err := os.Stat(filepath.Join(os.TempDir(), "slk-image-F1-photo.png")); !os.IsNotExist(err) {
		t.Errorf("the temp file was left behind after inlining (stat err %v)", err)
	}
}

func TestViewImage_NamesTheAttachmentsItSkipped(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "photo.png", "image/png", dcImageBytes),
		dcDoc(f, "F2", "notes.txt", "text/plain", ""),
	))
	h := newFakeHub(t, f)

	summary := dcCallTool(t, h, "view_image", map[string]any{"channel": "alpha"}).Text()

	if !strings.Contains(summary, "skipped non-image: notes.txt (text/plain)") {
		t.Errorf("a skipped attachment must be named, not dropped:\n%s", summary)
	}
}

func TestViewImage_FileURLResolvesWithoutAMessageLookup(t *testing.T) {
	f := newFakeSlack(t)
	f.On("files.info", jsonBody(t, map[string]any{
		"ok":   true,
		"file": dcDoc(f, "F1", "photo.png", "image/png", dcImageBytes),
	}))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "view_image", map[string]any{
		"permalink": "https://example.invalid/files/U1/F1/photo.png",
	})

	if res.IsError {
		t.Fatalf("file-URL mode failed: %s", res.Text())
	}
	if !strings.Contains(res.Text(), "inlined below") {
		t.Errorf("picture was not inlined:\n%s", res.Text())
	}
	// A file URL points straight at an attachment: no channel or message
	// resolution should happen at all.
	if f.Called("conversations.list") || f.Called("conversations.history") {
		t.Errorf("file-URL mode did a message lookup; calls=%v", f.Calls())
	}
}

func TestViewImage_NonImageBehindAFileURLIsAnExplicitRefusal(t *testing.T) {
	f := newFakeSlack(t)
	f.On("files.info", jsonBody(t, map[string]any{
		"ok":   true,
		"file": dcDoc(f, "F1", "contract.pdf", "application/pdf", "%PDF-1.4 stub"),
	}))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "view_image", map[string]any{
		"permalink": "https://example.invalid/files/U1/F1/contract.pdf",
	})
	got := res.Text()

	// Nothing to show is an error, not a cheerful empty summary — and it
	// names what WAS there, so the caller can pick another tool.
	if !res.IsError {
		t.Fatalf("a non-image file URL must be refused, got %q", got)
	}
	if !strings.Contains(got, "no matching attachment") ||
		!strings.Contains(got, "contract.pdf (application/pdf)") {
		t.Errorf("refusal must name what was found instead: %q", got)
	}
}

func TestViewImage_OversizedPictureIsHandedBackByPath(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	big := strings.Repeat("p", maxInlineImageBytes+1)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "huge.png", "image/png", big)))
	h := newFakeHub(t, f)

	dir := t.TempDir()
	res := h.runViewImage(context.Background(), "", "alpha", "", "", "", dir)
	summary := resultText(res)

	if !strings.Contains(summary, "too large to inline; read the file at this path") {
		t.Errorf("an oversized picture must be reported by path:\n%s", summary)
	}
	path := filepath.Join(dir, "slk-image-F1-huge.png")
	if !strings.Contains(summary, path) {
		t.Errorf("summary must carry the path %q:\n%s", path, summary)
	}
	// The bytes are NOT in the response, so the file has to survive.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the oversized file must be left on disk: %v", err)
	}
	for _, c := range res.Content {
		if _, ok := c.(mcp.ImageContent); ok {
			t.Error("an oversized picture must not be inlined")
		}
	}
}

func TestViewImage_NoPictureInConversationIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "notes.txt", "text/plain", "")))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "view_image", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a conversation with no picture must be an error, got %q", got)
	}
	if !strings.Contains(got, "no recent message with a matching attachment") {
		t.Errorf("error does not say what was looked for: %q", got)
	}
}

func TestViewImage_MissingTargetIsRefused(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "view_image", map[string]any{})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("view_image with no target must be refused, got %q", got)
	}
	if !strings.Contains(got, "channel is required") {
		t.Errorf("refusal must name the missing argument: %q", got)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("an argument error must not reach Slack; calls=%v", f.Calls())
	}
}
