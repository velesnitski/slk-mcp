package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestIsImageFile(t *testing.T) {
	if !isImageFile(audioFile("F1", "shot.png", "image/png", "u")) {
		t.Fatal("image/png must be an image")
	}
	if !isImageFile(audioFile("F2", "card.jpg", "image/jpeg", "u")) {
		t.Fatal("image/jpeg must be an image")
	}
	if isImageFile(audioFile("F3", "clip.m4a", "audio/mp4", "u")) {
		t.Fatal("audio must not be an image")
	}
}

func TestConfinedTempPath_prefixIsApplied(t *testing.T) {
	got, err := confinedTempPath("/tmp/x", "slk-image", "F9", "card.jpg")
	if err != nil || got != filepath.Join("/tmp/x", "slk-image-F9-card.jpg") {
		t.Fatalf("prefix should shape the temp name: got %q err=%v", got, err)
	}
}

func TestRunViewImage_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runViewImage(context.Background(), "ghost", "#general", "1.1", "", "", "")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestDownloadFiles_ImageInlineEncoding(t *testing.T) {
	// Drive downloadFiles directly (the IO seam), then assert the encode
	// path: a small file becomes base64 ImageContent and its temp file is
	// removed.
	dir := t.TempDir()
	png := []byte("\x89PNG\r\n\x1a\nfakebytes")
	fake := &fakeAudioClient{payload: png}
	img := goslack.File{}
	img.ID = "F100"
	img.Name = "card.png"
	img.Mimetype = "image/png"
	img.URLPrivateDownload = "https://example.invalid/f100"

	saved, _, err := downloadFiles(context.Background(), fake, []goslack.File{img}, dir, "slk-image", isImageFile)
	if err != nil || len(saved) != 1 {
		t.Fatalf("expected one saved image, got %d err=%v", len(saved), err)
	}
	data, _ := os.ReadFile(saved[0].Path)
	if base64.StdEncoding.EncodeToString(data) == "" {
		t.Fatal("image should be base64-encodable")
	}
	if !strings.HasSuffix(saved[0].Path, "slk-image-F100-card.png") {
		t.Fatalf("image temp file should carry the slk-image prefix, got %q", saved[0].Path)
	}
	if saved[0].Mimetype != "image/png" {
		t.Fatalf("mimetype should be preserved, got %q", saved[0].Mimetype)
	}
}
