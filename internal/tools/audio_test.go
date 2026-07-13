package tools

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/velesnitski/slk-mcp/internal/slack"
)

// fakeAudioClient satisfies MessageClient for the download loop: it
// writes canned bytes into the writer (or fails on demand).
type fakeAudioClient struct {
	payload []byte
	fail    bool
}

func (f *fakeAudioClient) History(ctx context.Context, p slack.HistoryParams) ([]goslack.Message, error) {
	return nil, errors.New("not used")
}
func (f *fakeAudioClient) ThreadReplies(ctx context.Context, channelID, threadTS string) ([]goslack.Message, error) {
	return nil, errors.New("not used")
}
func (f *fakeAudioClient) Post(ctx context.Context, channelID, text, threadTS string) (string, error) {
	return "", errors.New("not used")
}
func (f *fakeAudioClient) AddReaction(ctx context.Context, channelID, timestamp, emoji string) error {
	return errors.New("not used")
}
func (f *fakeAudioClient) Delete(ctx context.Context, channelID, timestamp string) error {
	return errors.New("not used")
}
func (f *fakeAudioClient) MessageAt(ctx context.Context, channelID, ts string) (*goslack.Message, error) {
	return nil, errors.New("not used")
}
func (f *fakeAudioClient) DownloadFile(ctx context.Context, downloadURL string, w io.Writer) error {
	if f.fail {
		return errors.New("boom")
	}
	_, err := w.Write(f.payload)
	return err
}
func (f *fakeAudioClient) FileInfo(ctx context.Context, fileID string) (goslack.File, error) {
	return goslack.File{}, errors.New("FileInfo not supported in fake")
}

var _ MessageClient = (*fakeAudioClient)(nil)

func audioFile(id, name, mimetype, url string) goslack.File {
	f := goslack.File{}
	f.ID = id
	f.Name = name
	f.Mimetype = mimetype
	f.URLPrivateDownload = url
	return f
}

func TestIsAudioFile(t *testing.T) {
	if !isAudioFile(audioFile("F1", "clip.m4a", "audio/mp4", "u")) {
		t.Fatal("audio/mp4 should be audio")
	}
	if isAudioFile(audioFile("F2", "pic.png", "image/png", "u")) {
		t.Fatal("image/png must not be audio")
	}
	if isAudioFile(goslack.File{}) {
		t.Fatal("empty mimetype must not be audio")
	}
}

func TestIsTranscribableFile(t *testing.T) {
	if !isTranscribableFile(audioFile("F1", "clip.m4a", "audio/mp4", "u")) {
		t.Fatal("audio must be transcribable")
	}
	if !isTranscribableFile(audioFile("F2", "huddle.mp4", "video/mp4", "u")) {
		t.Fatal("video (recorded huddle/clip) must be transcribable")
	}
	if isTranscribableFile(audioFile("F3", "pic.png", "image/png", "u")) {
		t.Fatal("image must not be transcribable")
	}
}

func TestDownloadAudioFiles_AcceptControlsVideo(t *testing.T) {
	// The same video file is skipped under isAudioFile (download_audio)
	// and saved under isTranscribableFile (transcribe_audio).
	video := audioFile("F7", "huddle.mp4", "video/mp4", "https://example.invalid/f7")

	fake := &fakeAudioClient{payload: []byte("mp4data")}
	saved, skipped, err := downloadFiles(context.Background(), fake, []goslack.File{video}, t.TempDir(), "slk-audio", isAudioFile)
	if err != nil || len(saved) != 0 || len(skipped) != 1 {
		t.Fatalf("isAudioFile should skip video: saved=%d skipped=%d err=%v", len(saved), len(skipped), err)
	}

	saved, skipped, err = downloadFiles(context.Background(), fake, []goslack.File{video}, t.TempDir(), "slk-audio", isTranscribableFile)
	if err != nil || len(saved) != 1 || len(skipped) != 0 {
		t.Fatalf("isTranscribableFile should save video: saved=%d skipped=%d err=%v", len(saved), len(skipped), err)
	}
}

func TestConfinedAudioPath_staysInsideDestDir(t *testing.T) {
	dir := "/tmp/scratch"

	// Normal case: one component under destDir.
	got, err := confinedTempPath(dir, "slk-audio", "F123", "clip.m4a")
	if err != nil || got != filepath.Join(dir, "slk-audio-F123-clip.m4a") {
		t.Fatalf("normal path: got %q err=%v", got, err)
	}

	// Hostile file ID and name: slashes/dots must not survive into a
	// traversal — both are sanitized to a single safe component.
	for _, tc := range []struct{ id, name string }{
		{"../../etc", "passwd"},
		{"F1", "../../../etc/passwd"},
		{"..", ".."},
		{"/abs/id", "/abs/name"},
	} {
		got, err := confinedTempPath(dir, "slk-audio", tc.id, tc.name)
		if err != nil {
			// A refusal is an acceptable outcome; the key invariant is
			// that we never return a path outside dir.
			continue
		}
		if rel, _ := filepath.Rel(dir, got); strings.HasPrefix(rel, "..") {
			t.Fatalf("confinedTempPath(%q,%q) escaped destDir: %q (rel %q)", tc.id, tc.name, got, rel)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"audio clip 2026.m4a": "audio_clip_2026.m4a",
		"../../../etc/passwd": "etc_passwd",
		"голос.m4a":           "m4a",
		"":                    "audio",
		"???":                 "audio",
		"ok-name_1.mp3":       "ok-name_1.mp3",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Fatalf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDownloadAudioFiles_savesAudioSkipsRest(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAudioClient{payload: []byte("RIFFdata")}
	files := []goslack.File{
		audioFile("F1", "clip.m4a", "audio/mp4", "https://example.invalid/f1"),
		audioFile("F2", "pic.png", "image/png", "https://example.invalid/f2"),
	}

	saved, skipped, err := downloadFiles(context.Background(), fake, files, dir, "slk-audio", isAudioFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(saved) != 1 {
		t.Fatalf("expected 1 saved audio, got %d", len(saved))
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "pic.png") {
		t.Fatalf("expected pic.png skipped, got %v", skipped)
	}
	want := filepath.Join(dir, "slk-audio-F1-clip.m4a")
	if saved[0].Path != want {
		t.Fatalf("path = %q, want %q", saved[0].Path, want)
	}
	data, rerr := os.ReadFile(saved[0].Path)
	if rerr != nil || string(data) != "RIFFdata" {
		t.Fatalf("file content mismatch: %q err=%v", data, rerr)
	}
	if saved[0].Size != int64(len("RIFFdata")) {
		t.Fatalf("size = %d, want %d", saved[0].Size, len("RIFFdata"))
	}
}

func TestDownloadAudioFiles_fallsBackToURLPrivate(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAudioClient{payload: []byte("x")}
	f := goslack.File{}
	f.ID = "F3"
	f.Name = "v.mp3"
	f.Mimetype = "audio/mpeg"
	f.URLPrivate = "https://example.invalid/private"

	saved, _, err := downloadFiles(context.Background(), fake, []goslack.File{f}, dir, "slk-audio", isAudioFile)
	if err != nil || len(saved) != 1 {
		t.Fatalf("expected fallback to url_private to save 1 file, got %d err=%v", len(saved), err)
	}
}

func TestDownloadAudioFiles_noURLIsSkipped(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAudioClient{}
	f := goslack.File{}
	f.ID = "F4"
	f.Name = "ghost.m4a"
	f.Mimetype = "audio/mp4"

	saved, skipped, err := downloadFiles(context.Background(), fake, []goslack.File{f}, dir, "slk-audio", isAudioFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(saved) != 0 || len(skipped) != 1 {
		t.Fatalf("no-URL file should be skipped, saved=%d skipped=%d", len(saved), len(skipped))
	}
}

func TestDownloadAudioFiles_htmlBodyIsScopeError(t *testing.T) {
	// Slack answers 200 + its sign-in page when the token lacks
	// files:read — that must surface as a scope error, not be saved
	// as "audio".
	dir := t.TempDir()
	fake := &fakeAudioClient{payload: []byte("<!DOCTYPE html><html lang=\"en-US\">...")}
	files := []goslack.File{audioFile("F6", "clip.m4a", "audio/mp4", "https://example.invalid/f6")}

	_, _, err := downloadFiles(context.Background(), fake, files, dir, "slk-audio", isAudioFile)
	if err == nil || !strings.Contains(err.Error(), "files:read") {
		t.Fatalf("HTML body should produce a files:read scope error, got %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "slk-audio-F6-clip.m4a")); !os.IsNotExist(serr) {
		t.Fatal("HTML payload must not survive on disk")
	}
}

func TestDownloadAudioFiles_downloadErrorCleansUp(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeAudioClient{fail: true}
	files := []goslack.File{audioFile("F5", "clip.m4a", "audio/mp4", "https://example.invalid/f5")}

	_, _, err := downloadFiles(context.Background(), fake, files, dir, "slk-audio", isAudioFile)
	if err == nil {
		t.Fatal("expected download error")
	}
	if _, serr := os.Stat(filepath.Join(dir, "slk-audio-F5-clip.m4a")); !os.IsNotExist(serr) {
		t.Fatal("partial file must be removed on download failure")
	}
}

func TestRunDownloadAudio_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runDownloadAudio(context.Background(), "ghost", "#general", "1.1", "", "")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestRunDownloadAudio_MissingTargetIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runDownloadAudio(context.Background(), "", "", "", "", "")
	if res == nil || !res.IsError {
		t.Fatalf("missing channel+ts+permalink should error, got %+v", res)
	}
}

func TestRunDownloadAudio_BadPermalinkIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runDownloadAudio(context.Background(), "", "", "", "not a permalink", "")
	if res == nil || !res.IsError {
		t.Fatalf("unparseable permalink should error, got %+v", res)
	}
}
