package tools

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/config"
)

// Shared placeholders for the media pipeline. The file URL is what
// Slack's "Copy link" on an attachment yields; the download URL is the
// url_private_download the API hands back.
const (
	usFileLink     = "https://example.slack.com/files/U1/F1/note.m4a"
	usDownloadURL  = "https://files.example.invalid/F1"
	usMsgPermalink = "https://example.slack.com/archives/C0ALPHA01/p1700000000000100"
	usMsgTS        = "1700000000.000100"
	usAudioBytes   = "ID3-not-really-audio"
)

// usFileInfoBody is a files.info response for one audio attachment.
func usFileInfoBody(mimetype, downloadURL string) string {
	return `{"ok":true,"file":{"id":"F1","name":"note.m4a","mimetype":"` + mimetype +
		`","url_private_download":"` + downloadURL + `"}}`
}

// usAudioFetch wires the shortest happy path to a downloaded attachment:
// a Slack file URL resolved by files.info, then the bytes themselves.
func usAudioFetch(f *usFakeSlack) {
	f.On("files.info", usFileInfoBody("audio/mp4", usDownloadURL))
	f.OnAsset(usDownloadURL, usAudioBytes)
}

func TestRegisterAudioTools_RegistersDownloadAudio(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t)
	if names := usToolNames(t, h.registerAudioTools); !usHasTool(names, "download_audio") {
		t.Fatalf("download_audio should be registered, got %v", names)
	}

	h = f.hub(t, func(c *config.Config) { c.DisabledTools = map[string]struct{}{"download_audio": {}} })
	if names := usToolNames(t, h.registerAudioTools); len(names) != 0 {
		t.Fatalf("a disabled tool must be unreachable, got %v", names)
	}
}

func TestRegisterTranscribeAndToneTools_HonourTheDisabledList(t *testing.T) {
	f := usNewFakeSlack(t)
	h := f.hub(t)
	if names := usToolNames(t, h.registerTranscribeTools); !usHasTool(names, "transcribe_audio") {
		t.Fatalf("transcribe_audio should be registered, got %v", names)
	}
	if names := usToolNames(t, h.registerToneTools); !usHasTool(names, "analyze_audio_tone") {
		t.Fatalf("analyze_audio_tone should be registered, got %v", names)
	}

	off := f.hub(t, func(c *config.Config) {
		c.DisabledTools = map[string]struct{}{"transcribe_audio": {}, "analyze_audio_tone": {}}
	})
	if names := usToolNames(t, off.registerTranscribeTools); len(names) != 0 {
		t.Fatalf("transcribe_audio should be gone, got %v", names)
	}
	if names := usToolNames(t, off.registerToneTools); len(names) != 0 {
		t.Fatalf("analyze_audio_tone should be gone, got %v", names)
	}
}

func TestRunDownloadAudio_FileURLResolvesWithoutAMessageLookup(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	h := f.hub(t)
	dir := t.TempDir()

	res := h.runDownloadAudio(context.Background(), "", "", "", usFileLink, "", dir)
	if res.IsError {
		t.Fatalf("file-URL fetch should succeed: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "downloaded 1 media file(s)") {
		t.Errorf("result should report the download: %q", text)
	}
	want := filepath.Join(dir, "slk-audio-F1-note.m4a")
	if !strings.Contains(text, want) {
		t.Errorf("result should name the local path %q, got %q", want, text)
	}
	if !strings.Contains(text, "whisper-cli") {
		t.Errorf("result should tell the caller how to transcribe: %q", text)
	}
	data, err := os.ReadFile(want)
	if err != nil || string(data) != usAudioBytes {
		t.Fatalf("attachment bytes not on disk: %q err=%v", data, err)
	}
	// A file URL points straight at the attachment — no channel or
	// message lookup should be needed to reach it.
	if f.Called("conversations.history") || f.Called("conversations.replies") {
		t.Errorf("file URL must skip message resolution, calls=%v", f.Calls())
	}
}

func TestRunDownloadAudio_FileInfoScopeErrorGetsTheScopeHint(t *testing.T) {
	f := usNewFakeSlack(t)
	f.OnError("files.info", "missing_scope")
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "", "", usFileLink, "", t.TempDir())
	if res == nil || !res.IsError {
		t.Fatalf("a refused files.info must be an error, got %+v", res)
	}
	text := resultText(res)
	if !strings.Contains(text, "missing_scope") || !strings.Contains(text, "files:read") {
		t.Errorf("an authorization failure should name the scopes to add: %q", text)
	}
}

func TestRunDownloadAudio_NonScopeErrorIsNotReframed(t *testing.T) {
	// Only authorization failures earn the scope hint; a plain not-found
	// must not be relabelled as a permissions problem.
	f := usNewFakeSlack(t)
	f.OnError("files.info", "file_not_found")
	h := f.hub(t)

	text := resultText(h.runDownloadAudio(context.Background(), "", "", "", usFileLink, "", t.TempDir()))
	if !strings.Contains(text, "file_not_found") {
		t.Fatalf("the real reason must survive: %q", text)
	}
	if strings.Contains(text, "OAuth scope") {
		t.Errorf("a not-found must not be reframed as a scope problem: %q", text)
	}
}

func TestRunDownloadAudio_SignInPageIsAFilesReadRefusal(t *testing.T) {
	// Slack answers 200 with its login page when the token lacks
	// files:read. Saving that as "audio" is the silent failure.
	f := usNewFakeSlack(t)
	f.On("files.info", usFileInfoBody("audio/mp4", usDownloadURL))
	f.OnAsset(usDownloadURL, `<!DOCTYPE html><html><head><title>Sign in</title></head></html>`)
	h := f.hub(t)
	dir := t.TempDir()

	res := h.runDownloadAudio(context.Background(), "", "", "", usFileLink, "", dir)
	if res == nil || !res.IsError {
		t.Fatalf("an HTML body must be refused, got %+v", res)
	}
	if !strings.Contains(resultText(res), "files:read") {
		t.Errorf("the refusal should name the missing scope: %q", resultText(res))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the sign-in page must not survive on disk: %v", entries)
	}
}

func TestRunDownloadAudio_MessagePermalinkUsesTheMessagesAttachment(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("conversations.history", `{"ok":true,"messages":[{"ts":"`+usMsgTS+`","user":"U1","text":"note",
		"files":[{"id":"F1","name":"note.m4a","mimetype":"audio/mp4","url_private_download":"`+usDownloadURL+`"}]}]}`)
	f.OnAsset(usDownloadURL, usAudioBytes)
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "", "", usMsgPermalink, "", t.TempDir())
	if res.IsError {
		t.Fatalf("permalink fetch should succeed: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "downloaded 1 media file(s)") {
		t.Errorf("result should report the download: %q", resultText(res))
	}
	// The message carried the file, so the thread never needs scanning.
	if f.Called("conversations.replies") {
		t.Errorf("a message with the attachment must not trigger a thread scan, calls=%v", f.Calls())
	}
}

func TestRunDownloadAudio_FallsBackToTheThreadWhenTheMessageHasNone(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("conversations.history", `{"ok":true,"messages":[{"ts":"`+usMsgTS+`","user":"U1","text":"see below",
		"thread_ts":"`+usMsgTS+`"}]}`)
	f.On("conversations.replies", `{"ok":true,"messages":[
		{"ts":"`+usMsgTS+`","user":"U1","text":"see below"},
		{"ts":"1700000001.000100","user":"U2","text":"here",
		 "files":[{"id":"F1","name":"note.m4a","mimetype":"audio/mp4","url_private_download":"`+usDownloadURL+`"}]}]}`)
	f.OnAsset(usDownloadURL, usAudioBytes)
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "", "", usMsgPermalink, "", t.TempDir())
	if res.IsError {
		t.Fatalf("a voice note on a reply must still be found: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "downloaded 1 media file(s)") {
		t.Errorf("result should report the download: %q", resultText(res))
	}
}

func TestRunDownloadAudio_NoAttachmentAnywhereSaysSo(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("conversations.history", `{"ok":true,"messages":[{"ts":"`+usMsgTS+`","user":"U1","text":"just text"}]}`)
	f.On("conversations.replies", `{"ok":true,"messages":[{"ts":"`+usMsgTS+`","user":"U1","text":"just text"}]}`)
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "", "", usMsgPermalink, "", t.TempDir())
	if res == nil || !res.IsError {
		t.Fatalf("nothing to download must be an error, got %+v", res)
	}
	if !strings.Contains(resultText(res), "neither does any reply in its thread") {
		t.Errorf("the message should say the thread was searched too: %q", resultText(res))
	}
}

func TestRunDownloadAudio_ForwardNamesTheOriginal(t *testing.T) {
	// A forward renders as an attachment carrying the original's ts and
	// no Files at all, so the file is unreachable from here. Saying "no
	// attachment" would send the caller hunting for a download bug.
	f := usNewFakeSlack(t)
	f.On("conversations.history", `{"ok":true,"messages":[{"ts":"`+usMsgTS+`","user":"U1","text":"",
		"attachments":[{"ts":"1699999999.000100","text":"forwarded"}]}]}`)
	f.OnError("conversations.replies", "thread_not_found")
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "", "", usMsgPermalink, "", t.TempDir())
	if res == nil || !res.IsError {
		t.Fatalf("a forward must be reported, got %+v", res)
	}
	text := resultText(res)
	if !strings.Contains(text, "forward") || !strings.Contains(text, "1699999999.000100") {
		t.Errorf("the original's ts must be named: %q", text)
	}
}

func TestRunDownloadAudio_LatestModeScansTheConversation(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("conversations.history", `{"ok":true,"messages":[
		{"ts":"1700000002.000100","user":"U1","text":"newest, no file"},
		{"ts":"1700000001.000100","user":"U1","text":"voice note",
		 "files":[{"id":"F1","name":"note.m4a","mimetype":"audio/mp4","url_private_download":"`+usDownloadURL+`"}]}]}`)
	f.OnAsset(usDownloadURL, usAudioBytes)
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "C0ALPHA01", "", "", "", t.TempDir())
	if res.IsError {
		t.Fatalf("latest-mode should find the newest attachment: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "downloaded 1 media file(s)") {
		t.Errorf("result should report the download: %q", resultText(res))
	}
}

func TestRunDownloadAudio_LatestModeUnknownAuthorIsReported(t *testing.T) {
	f := usNewFakeSlack(t)
	f.On("users.list", usRoster(usMemberAlex))
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "C0ALPHA01", "", "", "@nobody", t.TempDir())
	if res == nil || !res.IsError {
		t.Fatalf("an unresolvable from= must error, got %+v", res)
	}
	if !strings.Contains(resultText(res), "nobody") {
		t.Errorf("the error should name the handle that did not resolve: %q", resultText(res))
	}
}

func TestRunDownloadAudio_AttachmentWithoutAURLIsReportedNotSilentlyEmpty(t *testing.T) {
	// The file matches the filter but carries no private URL, so nothing
	// downloads. The result must say which files were present rather
	// than claiming a successful empty download.
	f := usNewFakeSlack(t)
	f.On("files.info", `{"ok":true,"file":{"id":"F1","name":"note.m4a","mimetype":"audio/mp4"}}`)
	h := f.hub(t)

	res := h.runDownloadAudio(context.Background(), "", "", "", usFileLink, "", t.TempDir())
	if res == nil || !res.IsError {
		t.Fatalf("an undownloadable attachment must be an error, got %+v", res)
	}
	text := resultText(res)
	if !strings.Contains(text, "no matching attachment") || !strings.Contains(text, "no private URL") {
		t.Errorf("the result should explain what was found: %q", text)
	}
}

func TestRunDownloadAudio_ReportsWhatItSkipped(t *testing.T) {
	// A voice note usually travels with a preview image. The image is
	// not an error, but dropping it silently hides that the message held
	// more than what came back.
	f := usNewFakeSlack(t)
	f.On("conversations.history", `{"ok":true,"messages":[{"ts":"`+usMsgTS+`","user":"U1","text":"note","files":[
		{"id":"F1","name":"note.m4a","mimetype":"audio/mp4","url_private_download":"`+usDownloadURL+`"},
		{"id":"F2","name":"shot.png","mimetype":"image/png","url_private_download":"https://files.example.invalid/F2"}]}]}`)
	f.OnAsset(usDownloadURL, usAudioBytes)
	h := f.hub(t)

	text := resultText(h.runDownloadAudio(context.Background(), "", "", "", usMsgPermalink, "", t.TempDir()))
	if !strings.Contains(text, "skipped non-audio: shot.png (image/png)") {
		t.Fatalf("the skipped attachment should be listed:\n%s", text)
	}
}

func TestRunDownloadAudio_DownloadFailureIsReportedVerbatim(t *testing.T) {
	// No asset is routed for the URL, so the fetch fails outright. That
	// is not a scope problem, so it must not be dressed up as one.
	f := usNewFakeSlack(t)
	f.On("files.info", usFileInfoBody("audio/mp4", usDownloadURL))
	h := f.hub(t)
	dir := t.TempDir()

	res := h.runDownloadAudio(context.Background(), "", "", "", usFileLink, "", dir)
	if res == nil || !res.IsError {
		t.Fatalf("a failed download must be an error, got %+v", res)
	}
	if !strings.Contains(resultText(res), "download note.m4a") {
		t.Errorf("the failing file should be named: %q", resultText(res))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a partial download must be cleaned up: %v", entries)
	}
}

func TestDetectSTT_MissingFFprobeIsNotFatal(t *testing.T) {
	// ffprobe only enriches the output with durations; its absence must
	// never block transcription.
	withSTTStubs(t, func(name string) (string, error) {
		if name == "ffprobe" {
			return "", errors.New("not found")
		}
		return "/opt/bin/" + name, nil
	}, nil)

	p, reason := detectSTT("", "", usWhisperModel(t))
	if p == nil {
		t.Fatalf("a missing ffprobe must not break the pipeline: %q", reason)
	}
	if p.ffprobe != "" {
		t.Errorf("ffprobe should be empty when absent, got %q", p.ffprobe)
	}
}

func TestMeanVolumeDB_FailedProbeFailsOpen(t *testing.T) {
	// A measurement that cannot be taken must not be read as silence —
	// that would refuse a perfectly good transcript.
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return nil, nil, errors.New("exit 1")
	})
	if _, ok := meanVolumeDB(context.Background(), "/bin/ffmpeg", "/tmp/a.wav"); ok {
		t.Fatal("a failed probe must report ok=false, not a level")
	}
}

func TestParseMeanVolumeDB_UnparseableNumberIsNoReading(t *testing.T) {
	if _, ok := parseMeanVolumeDB("mean_volume: 9999999999999999999999999999999999e999999 dB"); ok {
		t.Fatal("an out-of-range figure must report absence, not a value")
	}
}

func TestHasMatchingFile(t *testing.T) {
	audio := audioFile("F1", "note.m4a", "audio/mp4", "u")
	image := audioFile("F2", "shot.png", "image/png", "u")
	if !hasMatchingFile([]goslack.File{image, audio}, isAudioFile) {
		t.Error("a matching file anywhere in the list should count")
	}
	if hasMatchingFile([]goslack.File{image}, isAudioFile) {
		t.Error("no matching file must be false")
	}
	if hasMatchingFile(nil, isAudioFile) {
		t.Error("an empty list must be false")
	}
}

// ---------------------------------------------------------------------
// transcribe_audio
// ---------------------------------------------------------------------

// usWhisperModel writes a stand-in model file and returns its path, so
// detectSTT's os.Stat check passes without a real 470 MB download.
func usWhisperModel(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(path, []byte("stand-in"), 0o600); err != nil {
		t.Fatalf("write stand-in model: %v", err)
	}
	return path
}

func TestRunTranscribeAudio_MissingToolchainDegradesToDownload(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	withSTTStubs(t, func(string) (string, error) { return "", errors.New("not found") }, nil)
	h := f.hub(t)
	dir := t.TempDir()

	res := h.runTranscribeAudio(context.Background(), "", "", "", usFileLink, "", "ru", dir)
	if res.IsError {
		t.Fatalf("a missing toolchain must degrade, not fail: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "local speech-to-text unavailable") {
		t.Errorf("the reason should lead the result: %q", text)
	}
	if !strings.Contains(text, "brew install ffmpeg whisper-cpp") {
		t.Errorf("the install hint must be included so a client can self-heal: %q", text)
	}
	// The file has to still be there — it is all the caller gets.
	kept := filepath.Join(dir, "slk-audio-F1-note.m4a")
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("the downloaded file must survive the degraded path: %v", err)
	}
	if !strings.Contains(text, kept) {
		t.Errorf("the path must be reported: %q", text)
	}
}

func TestRunTranscribeAudio_TranscribesAndRemovesTheAudio(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	model := usWhisperModel(t)
	withSTTStubs(t,
		func(name string) (string, error) { return "/opt/bin/" + name, nil },
		func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
			joined := strings.Join(args, " ")
			switch {
			case strings.Contains(name, "ffprobe") && strings.Contains(joined, "format=duration"):
				return []byte("133.2\n"), nil, nil
			case strings.Contains(name, "ffprobe"):
				return []byte("0\n"), nil, nil
			case strings.Contains(name, "whisper"):
				return []byte("[00:00:00.000 --> 00:00:02.000]  hello team\n"), nil, nil
			}
			return nil, nil, nil
		})
	h := f.hub(t, func(c *config.Config) { c.WhisperModel = model })
	dir := t.TempDir()

	res := h.runTranscribeAudio(context.Background(), "", "", "", usFileLink, "", "en", dir)
	if res.IsError {
		t.Fatalf("transcription should succeed: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "hello team") {
		t.Errorf("the transcript is the point of the tool: %q", text)
	}
	if !strings.Contains(text, "language: en") || !strings.Contains(text, "duration: 2:13") {
		t.Errorf("metadata should name the language and duration: %q", text)
	}
	// The transcript is the artifact; the audio is cleaned up.
	if _, err := os.Stat(filepath.Join(dir, "slk-audio-F1-note.m4a")); !os.IsNotExist(err) {
		t.Errorf("a transcribed file should be removed, stat err = %v", err)
	}
}

func TestRunTranscribeAudio_FailureKeepsTheFileAndSaysWhy(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	model := usWhisperModel(t)
	withSTTStubs(t,
		func(name string) (string, error) { return "/opt/bin/" + name, nil },
		func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
			if strings.Contains(name, "ffprobe") {
				return nil, nil, errors.New("no ffprobe")
			}
			return nil, []byte("Invalid data found when processing input\nmore"), errors.New("exit 1")
		})
	h := f.hub(t, func(c *config.Config) { c.WhisperModel = model })
	dir := t.TempDir()

	res := h.runTranscribeAudio(context.Background(), "", "", "", usFileLink, "", "auto", dir)
	if res.IsError {
		t.Fatalf("a per-file failure is reported in the body, not as a tool error: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "transcription failed") || !strings.Contains(text, "Invalid data found") {
		t.Errorf("the failure and its reason should be reported: %q", text)
	}
	kept := filepath.Join(dir, "slk-audio-F1-note.m4a")
	if !strings.Contains(text, kept) {
		t.Errorf("a kept file must be named so the caller can retry by hand: %q", text)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("a failed transcription must keep the download: %v", err)
	}
}

func TestTranscribeAudioTool_DefaultsLanguageToAuto(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	withSTTStubs(t, func(string) (string, error) { return "", errors.New("not found") }, nil)
	h := f.hub(t)

	res := usCallTool(t, h.registerTranscribeTools, "transcribe_audio", map[string]any{
		"permalink": usFileLink,
	})
	if res.IsError {
		t.Fatalf("tool call failed: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "local speech-to-text unavailable") {
		t.Errorf("degraded path expected: %q", resultText(res))
	}
}

func TestDefaultWhisperModel_PointsAtTheDocumentedCacheLocation(t *testing.T) {
	got := defaultWhisperModel()
	if got == "" {
		t.Skip("no home directory in this environment")
	}
	if !strings.HasSuffix(got, filepath.Join(".cache", "whisper", "ggml-small.bin")) {
		t.Fatalf("default model path drifted from the README how-to: %q", got)
	}
}

// ---------------------------------------------------------------------
// analyze_audio_tone
// ---------------------------------------------------------------------

func TestRunAnalyzeTone_MissingFFmpegDegradesToDownload(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	withSTTStubs(t, func(string) (string, error) { return "", errors.New("not found") }, nil)
	h := f.hub(t)
	dir := t.TempDir()

	res := h.runAnalyzeTone(context.Background(), "", "", "", usFileLink, "", dir)
	if res.IsError {
		t.Fatalf("a missing ffmpeg must degrade, not fail: %q", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "ffmpeg not found") || !strings.Contains(text, "brew install ffmpeg") {
		t.Errorf("the result should say what is missing and how to fix it: %q", text)
	}
	if !strings.Contains(text, filepath.Join(dir, "slk-tone-F1-note.m4a")) {
		t.Errorf("the downloaded path must be reported: %q", text)
	}
}

func TestRunAnalyzeTone_ReportsLoudnessAndPitch(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	pcm := int16LE(sine(150, pitchSampleRate, pitchSampleRate))
	withSTTStubs(t,
		func(name string) (string, error) { return "/opt/bin/" + name, nil },
		func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "ebur128") {
				return nil, []byte(sampleToneStderr), nil
			}
			if strings.Contains(joined, "s16le") {
				return pcm, nil, nil
			}
			return nil, nil, nil
		})
	h := f.hub(t)
	dir := t.TempDir()

	res := h.runAnalyzeTone(context.Background(), "", "", "", usFileLink, "", dir)
	if res.IsError {
		t.Fatalf("tone analysis should succeed: %q", resultText(res))
	}
	text := resultText(res)
	for _, want := range []string{
		"loudness range (LRA): 4.2 LU",
		"crest factor:",
		"integrated loudness: -16.6 LUFS",
		"pitch (f0): mean 150 Hz",
		"proxy, not an emotion model",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	// The analysed file is cleaned up — the metrics are the artifact.
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("analysed files should be removed: %v", entries)
	}
}

func TestRunAnalyzeTone_NoVoicedFramesIsStated(t *testing.T) {
	// Silence must read as "no pitch", never as a pitch of 0 Hz.
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	withSTTStubs(t,
		func(name string) (string, error) { return "/opt/bin/" + name, nil },
		func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
			if strings.Contains(strings.Join(args, " "), "ebur128") {
				return nil, []byte(sampleToneStderr), nil
			}
			return make([]byte, 2*pitchWindow), nil, nil // digital silence
		})
	h := f.hub(t)

	text := resultText(h.runAnalyzeTone(context.Background(), "", "", "", usFileLink, "", t.TempDir()))
	if !strings.Contains(text, "no voiced frames detected") {
		t.Fatalf("silence should be stated outright:\n%s", text)
	}
}

func TestRunAnalyzeTone_AnalysisFailureIsReportedPerFile(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	withSTTStubs(t,
		func(name string) (string, error) { return "/opt/bin/" + name, nil },
		func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
			return nil, []byte("Invalid data found when processing input"), errors.New("exit 1")
		})
	h := f.hub(t)

	text := resultText(h.runAnalyzeTone(context.Background(), "", "", "", usFileLink, "", t.TempDir()))
	if !strings.Contains(text, "analysis failed") || !strings.Contains(text, "note.m4a") {
		t.Fatalf("the failing file should be named:\n%s", text)
	}
}

func TestAnalyzeToneTool_AcceptsAFileURL(t *testing.T) {
	f := usNewFakeSlack(t)
	usAudioFetch(f)
	withSTTStubs(t, func(string) (string, error) { return "", errors.New("not found") }, nil)
	h := f.hub(t)

	res := usCallTool(t, h.registerToneTools, "analyze_audio_tone", map[string]any{
		"permalink": usFileLink,
	})
	if res.IsError {
		t.Fatalf("tool call failed: %q", resultText(res))
	}
	if !strings.Contains(resultText(res), "ffmpeg not found") {
		t.Errorf("degraded path expected: %q", resultText(res))
	}
}

func TestFileBase(t *testing.T) {
	if got := fileBase("/tmp/slk-tone-F1-note.m4a"); got != "slk-tone-F1-note.m4a" {
		t.Errorf("fileBase = %q", got)
	}
	if got := fileBase("bare.m4a"); got != "bare.m4a" {
		t.Errorf("a path with no separator should pass through, got %q", got)
	}
}

func TestCrestDB_ZeroCrestIsZeroNotNegativeInfinity(t *testing.T) {
	// log10(0) is -Inf; rendering that as a dB figure would be nonsense.
	if got := (toneMetrics{}).CrestDB(); got != 0 {
		t.Fatalf("a missing crest factor must render as 0, got %v", got)
	}
	if got := (toneMetrics{CrestLinear: 10}).CrestDB(); math.Abs(got-20) > 0.001 {
		t.Fatalf("crest 10 should be 20 dB, got %v", got)
	}
}

func TestLraLabel_CoversEveryBand(t *testing.T) {
	bands := []struct {
		lra  float64
		want string
	}{
		{2, "монотон"},
		{5, "контролируемый"},
		{7, "оживлён"},
		{15, "крик"},
	}
	for _, b := range bands {
		if got := lraLabel(b.lra); !strings.Contains(got, b.want) {
			t.Errorf("lraLabel(%v) = %q, want it to mention %q", b.lra, got, b.want)
		}
	}
}

func TestParabolicMin_EdgesFallBackToTheIntegerTau(t *testing.T) {
	c := []float64{1, 0.5, 0.9}
	if got := parabolicMin(c, 0); got != 0 {
		t.Errorf("tau at the left edge must pass through, got %v", got)
	}
	if got := parabolicMin(c, 2); got != 2 {
		t.Errorf("tau at the right edge must pass through, got %v", got)
	}
	// A flat neighbourhood has no parabola to fit; tau passes through.
	if got := parabolicMin([]float64{1, 1, 1}, 1); got != 1 {
		t.Errorf("a zero denominator must pass through, got %v", got)
	}
}

func TestLastFloat_UnparseableCaptureIsNotAValue(t *testing.T) {
	// Guards the "returned less while looking successful" failure: a
	// malformed figure must report absence, not a silent zero that
	// renders as a real measurement.
	if _, ok := lastFloat(reLRA, "LRA: 4.2 LU\nLRA: 7.1 LU"); !ok {
		t.Fatal("a well-formed value should parse")
	}
	if v, _ := lastFloat(reLRA, "LRA: 4.2 LU\nLRA: 7.1 LU"); v != 7.1 {
		t.Fatalf("the summary (last) value should win, got %v", v)
	}
	if _, ok := lastFloat(reLRA, "no figures here"); ok {
		t.Fatal("no match must report absence")
	}
}
