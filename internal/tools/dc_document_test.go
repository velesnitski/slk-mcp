package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// dcDoc builds the Slack file JSON for an attachment served by the fake.
// url is the path the body is routed at (relative to the fake's base);
// pass "" for a file that is never meant to be downloaded.
func dcDoc(f *fakeSlack, id, name, mimetype, body string) map[string]any {
	file := map[string]any{
		"id":       id,
		"name":     name,
		"title":    name,
		"mimetype": mimetype,
		"size":     len(body),
	}
	if body != "" {
		path := "dl/" + id
		f.On(path, body)
		file["url_private_download"] = f.URL() + path
	}
	return file
}

// dcHistory routes conversations.history to one page of messages.
func dcHistory(t *testing.T, f *fakeSlack, msgs ...map[string]any) {
	t.Helper()
	f.On("conversations.history", jsonBody(t, map[string]any{
		"ok": true, "messages": msgs, "has_more": false,
	}))
}

// dcMsg builds a top-level message carrying files.
func dcMsg(ts, user string, files ...map[string]any) map[string]any {
	return map[string]any{
		"type": "message", "ts": ts, "user": user, "text": "", "files": files,
	}
}

func TestReadDocument_ListOnlyShowsEveryAttachment(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	linked := dcDoc(f, "F3", "spec.docx", "text/plain", "")
	linked["is_external"] = true // no external_type: the host is unknown
	legacy := dcDoc(f, "F4", "budget.xls", "application/vnd.ms-excel", "")
	dcHistory(t, f,
		dcMsg("1700000002.000100", "U1",
			dcDoc(f, "F1", "runbook.md", "text/markdown", ""),
			dcDoc(f, "F2", "diagram.png", "image/png", ""),
			linked,
			legacy,
		),
	)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "list_only": true})
	got := res.Text()

	if res.IsError {
		t.Fatalf("list_only failed: %s", got)
	}
	if !strings.Contains(got, "4 attachment(s)") {
		t.Errorf("count missing:\n%s", got)
	}
	// A listing that hid the image would read as "there is nothing else",
	// which is the one thing it must never imply falsely.
	if !strings.Contains(got, "runbook.md") || !strings.Contains(got, "diagram.png") {
		t.Errorf("listing dropped an attachment:\n%s", got)
	}
	if !strings.Contains(got, "[not text — try view_image or transcribe_audio]") {
		t.Errorf("the unreadable attachment is not marked:\n%s", got)
	}
	// An external file whose host Slack did not name still has to be
	// marked, or it reads as an ordinary upload that simply failed.
	if !strings.Contains(got, "[linked external document — not stored in Slack, cannot be read here]") {
		t.Errorf("the unnamed external attachment is not marked:\n%s", got)
	}
	if !strings.Contains(got, "[legacy .xls — re-save as .xlsx to read it here]") {
		t.Errorf("the legacy workbook is not marked:\n%s", got)
	}
	if !strings.Contains(got, "ts=1700000002.000100") {
		t.Errorf("listing must hand back the ts needed to fetch a file:\n%s", got)
	}
	if f.Called("dl/F1") || f.Called("dl/F2") {
		t.Errorf("list_only must not download anything; calls=%v", f.Calls())
	}
}

func TestReadDocument_MatchPicksTheNamedDocument(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f,
		dcMsg("1700000002.000100", "U1", dcDoc(f, "F2", "notes.txt", "text/plain", "later file")),
		dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "runbook.md", "text/markdown", "# Runbook\n\nStep one.")),
	)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "match": "runbook"})
	got := res.Text()

	if res.IsError {
		t.Fatalf("match read failed: %s", got)
	}
	if !strings.Contains(got, "Step one.") {
		t.Errorf("matched document body missing:\n%s", got)
	}
	// "The newest attachment" is the wrong answer when the caller named
	// an older one.
	if strings.Contains(got, "later file") {
		t.Errorf("match returned the wrong document:\n%s", got)
	}
	if !strings.Contains(got, "1 document(s)") {
		t.Errorf("header missing:\n%s", got)
	}
}

func TestReadDocument_MatchMissSaysHowToLook(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "runbook.md", "text/markdown", "x")))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "match": "invoice"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a match miss must be an error, got %q", got)
	}
	if !strings.Contains(got, `no recent document matches "invoice"`) {
		t.Errorf("error must echo the needle: %q", got)
	}
	if !strings.Contains(got, "list_only=true") {
		t.Errorf("error must name the way forward: %q", got)
	}
}

func TestReadDocument_LimitReadsSeveralInOneCall(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f,
		dcMsg("1700000003.000100", "U1", dcDoc(f, "F3", "third.txt", "text/plain", "third body")),
		dcMsg("1700000002.000100", "U1", dcDoc(f, "F2", "second.txt", "text/plain", "second body")),
		dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "first.txt", "text/plain", "first body")),
	)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "limit": float64(2)}).Text()

	if !strings.Contains(got, "2 document(s)") {
		t.Errorf("limit not honoured in the header:\n%s", got)
	}
	if !strings.Contains(got, "third body") || !strings.Contains(got, "second body") {
		t.Errorf("the two newest documents were not both read:\n%s", got)
	}
	if strings.Contains(got, "first body") {
		t.Errorf("limit=2 read a third document:\n%s", got)
	}
}

func TestReadDocument_ExternalDocumentIsRefusedBeforeFetching(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	linked := dcDoc(f, "F1", "proposal.docx", "text/plain", "")
	linked["is_external"] = true
	linked["external_type"] = "gdrive"
	linked["url_private"] = "https://example.invalid/doc/F1"
	dcHistory(t, f, dcMsg("1700000001.000100", "U1", linked))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "match": "proposal"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a linked document must be refused, got %q", got)
	}
	// A 401 from the third party reads exactly like a Slack permissions
	// failure, so the refusal has to name the real reason and the link.
	if !strings.Contains(got, "not stored in Slack") || !strings.Contains(got, "gdrive") {
		t.Errorf("refusal does not explain itself: %q", got)
	}
	if !strings.Contains(got, "https://example.invalid/doc/F1") {
		t.Errorf("refusal must hand back the link: %q", got)
	}
}

func TestReadDocument_ExternalIsSkippedBesideALocalRead(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	linked := dcDoc(f, "F2", "proposal.txt", "text/plain", "")
	linked["is_external"] = true
	linked["external_type"] = "box"
	dcHistory(t, f,
		dcMsg("1700000002.000100", "U1", linked),
		dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "runbook.md", "text/markdown", "local body")),
	)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "limit": float64(5)}).Text()

	if !strings.Contains(got, "local body") {
		t.Errorf("the readable document was not read:\n%s", got)
	}
	if !strings.Contains(got, "Skipped:") || !strings.Contains(got, "box") {
		t.Errorf("the linked document was dropped silently:\n%s", got)
	}
}

func TestReadDocument_LatestModeReadsTheNewestAttachment(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f,
		dcMsg("1700000002.000100", "U1", dcDoc(f, "F2", "notes.txt", "text/plain", "newest body")),
		dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "old.txt", "text/plain", "older body")),
	)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"})
	got := res.Text()

	if res.IsError {
		t.Fatalf("latest-mode failed: %s", got)
	}
	if !strings.Contains(got, "newest body") || strings.Contains(got, "older body") {
		t.Errorf("latest-mode picked the wrong message:\n%s", got)
	}
	if !strings.Contains(got, "notes.txt (text/plain,") {
		t.Errorf("per-document header missing:\n%s", got)
	}
}

func TestReadDocument_NonDocumentAttachmentsAreNamedNotDropped(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "notes.txt", "text/plain", "the text"),
		dcDoc(f, "F2", "clip.mp4", "video/mp4", ""),
	))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"}).Text()

	if !strings.Contains(got, "the text") {
		t.Errorf("the document was not read:\n%s", got)
	}
	if !strings.Contains(got, "not a document, skipped:") || !strings.Contains(got, "clip.mp4") {
		t.Errorf("the skipped attachment must be named:\n%s", got)
	}
}

func TestReadDocument_TruncationIsReportedNotSilent(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "notes.txt", "text/plain", strings.Repeat("ab", 50))))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{
		"channel": "alpha", "max_chars": float64(10),
	}).Text()

	if !strings.Contains(got, "TRUNCATED to 10 chars") {
		t.Errorf("truncation must be announced:\n%s", got)
	}
	if strings.Contains(got, strings.Repeat("ab", 50)) {
		t.Errorf("the body was not actually truncated:\n%s", got)
	}
}

func TestReadDocument_NonPositiveMaxCharsFallsBackToTheDefault(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	// Over 1 KiB so the HTML sniffer has to work on a bounded prefix.
	body := strings.Repeat("plain text line\n", 200)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "notes.txt", "text/plain", body)))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{
		"channel": "alpha", "max_chars": float64(0),
	}).Text()

	if strings.Contains(got, "TRUNCATED") {
		t.Error("max_chars=0 must mean the default, not zero")
	}
	if !strings.Contains(got, strings.TrimSpace(body)) {
		t.Errorf("the body was not returned whole (%d chars back)", len(got))
	}
}

func TestReadDocument_HTMLAttachmentIsFlattened(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "proposal.html", "text/html",
		`<html><body><h1>Proposal</h1><p>Budget &amp; scope</p><script>var x=1</script></body></html>`)))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"}).Text()

	if !strings.Contains(got, "Proposal") || !strings.Contains(got, "Budget & scope") {
		t.Errorf("HTML was not flattened to readable text:\n%s", got)
	}
	if strings.Contains(got, "var x=1") || strings.Contains(got, "<h1>") {
		t.Errorf("script or markup survived:\n%s", got)
	}
}

func TestReadDocument_MalformedWorkbookIsReportedNotSilent(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	// Named .xlsx, but the bytes are not a ZIP container at all.
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "budget.xlsx",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"not a workbook at all")))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"}).Text()

	if !strings.Contains(got, "budget.xlsx") {
		t.Errorf("the failing file must be named:\n%s", got)
	}
	// The failure has to appear in the body; a workbook that silently
	// rendered as nothing is the defect this pins.
	if strings.Contains(got, "--- budget.xlsx (application/vnd.openxmlformats-officedocument.spreadsheetml.sheet, 21 bytes) ---\n\n") {
		t.Errorf("workbook failure rendered as empty content:\n%s", got)
	}
}

func TestReadDocument_XLSNamedTextIsDeclinedWithAnExplanation(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	// Slack served it as text, but the name says .xls — renderDocuments
	// checks the bytes rather than trusting either.
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "budget.xls", "text/plain", "id,amount\n1,2\n")))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"}).Text()

	if !strings.Contains(got, "budget.xls") {
		t.Errorf("the file must be named:\n%s", got)
	}
	if !strings.Contains(got, "not a\nworkbook at all") && !strings.Contains(got, "Legacy .xls workbook") {
		t.Errorf("an .xls must be declined with a reason, got:\n%s", got)
	}
}

func TestReadDocument_NoDocumentsInConversationIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1"))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "limit": float64(3)})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("an empty conversation must be an error, got %q", got)
	}
	if !strings.Contains(got, "no recent message with a matching attachment") {
		t.Errorf("error does not say what was looked for: %q", got)
	}
}

func TestReadDocument_FromFilterRestrictsTheAuthor(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	f.On("users.list", `{"ok":true,"members":[{"id":"U1","name":"sam"},{"id":"U2","name":"pat"}]}`)
	dcHistory(t, f, dcMsg("1700000001.000100", "U2", dcDoc(f, "F1", "notes.txt", "text/plain", "pat's file")))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{
		"channel": "alpha", "from": "@sam", "limit": float64(3),
	})
	got := res.Text()

	// The only document belongs to someone else, so the filter must hold
	// rather than quietly returning it.
	if !res.IsError {
		t.Fatalf("from= must exclude other authors, got %q", got)
	}
	if strings.Contains(got, "pat's file") {
		t.Errorf("the author filter leaked another user's document: %q", got)
	}
}

func TestReadDocument_UnknownHandleIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	f.On("users.list", `{"ok":true,"members":[{"id":"U1","name":"sam"}]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{
		"channel": "alpha", "from": "@nobody", "list_only": true,
	})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("an unresolvable handle must be an error, got %q", got)
	}
	if !strings.Contains(got, "nobody") {
		t.Errorf("error must name the handle: %q", got)
	}
	if f.Called("conversations.history") {
		t.Errorf("history must not be fetched once the author is unresolvable; calls=%v", f.Calls())
	}
}

func TestReadDocument_UnknownChannelIsReported(t *testing.T) {
	f := newFakeSlack(t)
	f.On("conversations.list", `{"ok":true,"channels":[{"id":"C1","name":"alpha"}]}`)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "beta", "list_only": true})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("an unknown channel must be an error, got %q", got)
	}
	if !strings.Contains(got, "#beta") {
		t.Errorf("error must name the channel asked for: %q", got)
	}
}

func TestReadDocument_HistoryFailureIsDecoratedWithTheScopeHint(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	f.OnError("conversations.history", "missing_scope")
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "list_only": true})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a history failure must surface, got %q", got)
	}
	if !strings.Contains(got, "missing_scope") {
		t.Errorf("the Slack code must survive: %q", got)
	}
	// A scope failure is actionable only if the message says which scope.
	if !strings.Contains(got, "OAuth scope") {
		t.Errorf("a scope failure must carry the hint: %q", got)
	}
}

func TestReadDocument_UnknownWorkspaceIsRefusedBeforeAnyCall(t *testing.T) {
	f := newFakeSlack(t)
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{
		"channel": "alpha", "workspace": "nowhere", "list_only": true,
	})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("an unknown workspace must be an error, got %q", got)
	}
	if !strings.Contains(got, "nowhere") {
		t.Errorf("error must name the label asked for: %q", got)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("workspace scoping must fail before any API call; calls=%v", f.Calls())
	}
}

func TestReadDocument_LatestModeWithNoDocumentIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	// Only a picture: read_document must say it found nothing rather
	// than hand back an empty-looking success.
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "diagram.png", "image/png", "")))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a conversation with no document must be an error, got %q", got)
	}
	if !strings.Contains(got, "no recent message with a matching attachment") {
		t.Errorf("error does not say what was looked for: %q", got)
	}
}

func TestReadDocument_NonPositiveLimitStillReadsOne(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f,
		dcMsg("1700000002.000100", "U1", dcDoc(f, "F2", "runbook-b.md", "text/markdown", "second body")),
		dcMsg("1700000001.000100", "U1", dcDoc(f, "F1", "runbook-a.md", "text/markdown", "first body")),
	)
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{
		"channel": "alpha", "match": "runbook", "limit": float64(-3),
	}).Text()

	if !strings.Contains(got, "1 document(s)") {
		t.Errorf("a nonsensical limit must fall back to one document:\n%s", got)
	}
	if strings.Contains(got, "first body") {
		t.Errorf("a negative limit read more than one document:\n%s", got)
	}
}

// dcPDF wraps text-drawing operators in the minimum PDF scaffolding the
// extractor needs: it looks for stream/endstream pairs, not a valid
// xref table.
func dcPDF(lines ...string) string {
	var content strings.Builder
	content.WriteString("BT\n")
	for i, line := range lines {
		if i > 0 {
			content.WriteString("0 -14 Td\n")
		}
		content.WriteString("(" + line + ") Tj\n")
	}
	content.WriteString("ET\n")
	body := content.String()
	return "%PDF-1.4\n1 0 obj\n<< /Length " + strconv.Itoa(len(body)) +
		" >>\nstream\n" + body + "\nendstream\nendobj\n%%EOF\n"
}

func TestReadDocument_PDFTextIsExtractedAndTheFileRemoved(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "report.pdf", "application/pdf",
			dcPDF("Quarterly summary for the team", "Second line of the report body"))))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"}).Text()

	if !strings.Contains(got, "Quarterly summary for the team") ||
		!strings.Contains(got, "Second line of the report body") {
		t.Errorf("PDF text was not extracted:\n%s", got)
	}
	// Extracted text can drift from the document's own layout, so the
	// header has to caveat it rather than present it as a faithful copy.
	if !strings.Contains(got, "text extracted, check exact figures against the document") {
		t.Errorf("the extraction caveat is missing:\n%s", got)
	}
	// A flattened PDF must not outlive the call: every contract and
	// report ever opened used to accumulate in the temp dir.
	path := filepath.Join(os.TempDir(), "slk-doc-F1-report.pdf")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		os.Remove(path)
		t.Errorf("the flattened PDF was left on disk at %s (stat err %v)", path, err)
	}
}

func TestReadDocument_PDFTruncationIsAnnounced(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "report.pdf", "application/pdf",
			dcPDF("Quarterly summary for the team", "Second line of the report body"))))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{
		"channel": "alpha", "max_chars": float64(12),
	}).Text()

	if !strings.Contains(got, "TRUNCATED to 12 chars") {
		t.Errorf("a truncated PDF must say so:\n%s", got)
	}
	if strings.Contains(got, "Second line of the report body") {
		t.Errorf("the PDF text was not actually truncated:\n%s", got)
	}
}

func TestReadDocument_PDFWithNoTextLayerIsHandedBackByPath(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	// A stream carrying no text-drawing operators: a scan, in effect.
	body := "%PDF-1.4\n1 0 obj\n<< >>\nstream\n0 0 612 792 re f\nendstream\nendobj\n%%EOF\n"
	dcHistory(t, f, dcMsg("1700000001.000100", "U1",
		dcDoc(f, "F1", "scan.pdf", "application/pdf", body)))
	h := newFakeHub(t, f)

	got := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha"}).Text()

	path := filepath.Join(os.TempDir(), "slk-doc-F1-scan.pdf")
	t.Cleanup(func() { os.Remove(path) })

	if !strings.Contains(got, "No extractable text layer") {
		t.Errorf("a scan must be reported as such:\n%s", got)
	}
	if !strings.Contains(got, "saved to: "+path) {
		t.Errorf("a scan must be handed back by path:\n%s", got)
	}
	// For this one the file IS the only way to read the content, so it
	// has to survive the call.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the unflattened PDF must be kept: %v", err)
	}
}

func TestReadDocument_DownloadFailureIsReported(t *testing.T) {
	f := newFakeSlack(t)
	dcChannelAlpha(f)
	doc := dcDoc(f, "F1", "notes.txt", "text/plain", "")
	doc["url_private_download"] = "http://127.0.0.1:1/dl/F1"
	dcHistory(t, f, dcMsg("1700000001.000100", "U1", doc))
	h := newFakeHub(t, f)

	res := dcCallTool(t, h, "read_document", map[string]any{"channel": "alpha", "limit": float64(2)})
	got := res.Text()

	if !res.IsError {
		t.Fatalf("a failed download must surface as an error, got %q", got)
	}
	if !strings.Contains(got, "notes.txt") {
		t.Errorf("error must name the file it failed on: %q", got)
	}
}
