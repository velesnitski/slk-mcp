package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSTTStubs swaps the exec seams for a test and restores them after.
func withSTTStubs(t *testing.T,
	look func(string) (string, error),
	run func(context.Context, string, ...string) ([]byte, []byte, error),
) {
	t.Helper()
	origLook, origRun := lookPath, runCommand
	if look != nil {
		lookPath = look
	}
	if run != nil {
		runCommand = run
	}
	t.Cleanup(func() { lookPath, runCommand = origLook, origRun })
}

func TestDetectSTT_MissingFFmpeg(t *testing.T) {
	withSTTStubs(t, func(name string) (string, error) {
		return "", errors.New("not found")
	}, nil)
	p, reason := detectSTT("", "", "")
	if p != nil || !strings.Contains(reason, "ffmpeg") {
		t.Fatalf("expected ffmpeg-missing reason, got p=%v reason=%q", p, reason)
	}
}

func TestDetectSTT_MissingWhisper(t *testing.T) {
	withSTTStubs(t, func(name string) (string, error) {
		if name == "ffmpeg" {
			return "/usr/bin/ffmpeg", nil
		}
		return "", errors.New("not found")
	}, nil)
	p, reason := detectSTT("", "", "")
	if p != nil || !strings.Contains(reason, "whisper") {
		t.Fatalf("expected whisper-missing reason, got p=%v reason=%q", p, reason)
	}
}

func TestDetectSTT_MissingModel(t *testing.T) {
	withSTTStubs(t, func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}, nil)
	p, reason := detectSTT("", "", filepath.Join(t.TempDir(), "nope.bin"))
	if p != nil || !strings.Contains(reason, "model not found") {
		t.Fatalf("expected model-missing reason, got p=%v reason=%q", p, reason)
	}
}

func TestDetectSTT_ResolvesPipeline(t *testing.T) {
	withSTTStubs(t, func(name string) (string, error) {
		return "/opt/bin/" + name, nil
	}, nil)
	model := filepath.Join(t.TempDir(), "model.bin")
	if err := os.WriteFile(model, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, reason := detectSTT("my-ffmpeg", "my-whisper", model)
	if p == nil {
		t.Fatalf("expected pipeline, got reason=%q", reason)
	}
	if p.ffmpeg != "/opt/bin/my-ffmpeg" || p.whisper != "/opt/bin/my-whisper" || p.model != model {
		t.Fatalf("pipeline misresolved: %+v", p)
	}
}

func TestTranscribeFile_RunsFFmpegThenWhisper(t *testing.T) {
	var calls [][]string
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if strings.Contains(name, "whisper") {
			return []byte("  привет команда  \n"), []byte("metal noise"), nil
		}
		return nil, nil, nil
	})
	p := &sttPipeline{ffmpeg: "/bin/ffmpeg", whisper: "/bin/whisper-cli", model: "/m/model.bin"}

	text, err := p.transcribeFile(context.Background(), "/tmp/a.m4a", "ru")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "привет команда" {
		t.Fatalf("transcript should be stdout trimmed, got %q", text)
	}
	// ffmpeg convert -> ffmpeg volumedetect (silence guard) -> whisper.
	if len(calls) != 3 || calls[0][0] != "/bin/ffmpeg" || calls[1][0] != "/bin/ffmpeg" || calls[2][0] != "/bin/whisper-cli" {
		t.Fatalf("expected ffmpeg, ffmpeg(volumedetect), whisper, got %v", calls)
	}
	if !strings.Contains(strings.Join(calls[1], " "), "volumedetect") {
		t.Fatalf("second call should measure volume, got %v", calls[1])
	}
	joined := strings.Join(calls[2], " ")
	if !strings.Contains(joined, "-l ru") || !strings.Contains(joined, "-m /m/model.bin") {
		t.Fatalf("whisper args missing language/model: %v", calls[1])
	}
}

func TestTranscribeFile_FFmpegFailure(t *testing.T) {
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("bad input\nmore noise"), errors.New("exit 1")
	})
	p := &sttPipeline{ffmpeg: "/bin/ffmpeg", whisper: "/bin/w", model: "/m"}
	_, err := p.transcribeFile(context.Background(), "/tmp/a.m4a", "auto")
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") || !strings.Contains(err.Error(), "bad input") {
		t.Fatalf("expected ffmpeg error with first stderr line, got %v", err)
	}
}

func TestTranscribeFile_EmptyTranscriptIsError(t *testing.T) {
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return []byte("   "), nil, nil
	})
	p := &sttPipeline{ffmpeg: "/bin/ffmpeg", whisper: "/bin/w", model: "/m"}
	_, err := p.transcribeFile(context.Background(), "/tmp/a.m4a", "auto")
	if err == nil || !strings.Contains(err.Error(), "no transcript") {
		t.Fatalf("expected empty-transcript error, got %v", err)
	}
}

func TestTranscribeFile_ExtractsAudioTrackFromVideo(t *testing.T) {
	// -vn must be present so video inputs (recorded huddles/clips)
	// contribute only their audio stream.
	var ffmpegArgs []string
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		if strings.Contains(name, "ffmpeg") {
			// Only the first ffmpeg call is the conversion; the second is
			// the volumedetect probe and carries different args.
			if ffmpegArgs == nil {
				ffmpegArgs = args
			}
			return nil, nil, nil
		}
		return []byte("text"), nil, nil
	})
	p := &sttPipeline{ffmpeg: "/bin/ffmpeg", whisper: "/bin/w", model: "/m"}
	if _, err := p.transcribeFile(context.Background(), "/tmp/huddle.mp4", "auto"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.Join(ffmpegArgs, " "), "-vn") {
		t.Fatalf("ffmpeg args must include -vn, got %v", ffmpegArgs)
	}
}

func TestProbeDuration(t *testing.T) {
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return []byte("3723.45\n"), nil, nil
	})
	p := &sttPipeline{ffprobe: "/bin/ffprobe"}
	if got := p.probeDuration(context.Background(), "/tmp/a.mp4"); got != "1:02:03" {
		t.Fatalf("probeDuration = %q, want 1:02:03", got)
	}
}

func TestProbeDuration_ShortForm(t *testing.T) {
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return []byte("133.2"), nil, nil
	})
	p := &sttPipeline{ffprobe: "/bin/ffprobe"}
	if got := p.probeDuration(context.Background(), "/tmp/a.m4a"); got != "2:13" {
		t.Fatalf("probeDuration = %q, want 2:13", got)
	}
}

func TestProbeDuration_MissingFFprobeIsSilent(t *testing.T) {
	p := &sttPipeline{ffprobe: ""}
	if got := p.probeDuration(context.Background(), "/tmp/a.m4a"); got != "" {
		t.Fatalf("missing ffprobe should yield empty duration, got %q", got)
	}
}

func TestProbeDuration_GarbageIsSilent(t *testing.T) {
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return []byte("N/A"), nil, nil
	})
	p := &sttPipeline{ffprobe: "/bin/ffprobe"}
	if got := p.probeDuration(context.Background(), "/tmp/a.m4a"); got != "" {
		t.Fatalf("unparseable ffprobe output should yield empty duration, got %q", got)
	}
}

func TestFirstErrLine(t *testing.T) {
	if got := firstErrLine([]byte("  one\ntwo\n")); got != "one" {
		t.Fatalf("firstErrLine = %q", got)
	}
	if got := firstErrLine(nil); got != "" {
		t.Fatalf("firstErrLine(nil) = %q", got)
	}
}

func TestSTTSetupHint_IsSelfContained(t *testing.T) {
	// The degraded response must carry everything an agent needs to
	// install the toolchain and retry — guard the hint against
	// accidental truncation.
	for _, want := range []string{
		"brew install ffmpeg whisper-cpp",
		"ggml-small.bin",
		"SLACK_WHISPER_MODEL",
		"transcribe_audio",
	} {
		if !strings.Contains(sttSetupHint, want) {
			t.Fatalf("sttSetupHint missing %q", want)
		}
	}
}

func TestRunTranscribeAudio_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runTranscribeAudio(context.Background(), "ghost", "#general", "1.1", "", "", "auto", "")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}

func TestRunTranscribeAudio_MissingTargetIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runTranscribeAudio(context.Background(), "", "", "", "", "", "auto", "")
	if res == nil || !res.IsError {
		t.Fatalf("missing target should error, got %+v", res)
	}
}

func TestParseMeanVolumeDB(t *testing.T) {
	// Real ffmpeg volumedetect output shape.
	out := `[Parsed_volumedetect_0 @ 0x14a704080] n_samples: 1392000
[Parsed_volumedetect_0 @ 0x14a704080] mean_volume: -91.0 dB
[Parsed_volumedetect_0 @ 0x14a704080] max_volume: -90.3 dB`
	v, ok := parseMeanVolumeDB(out)
	if !ok || v != -91.0 {
		t.Fatalf("digital silence: got (%v, %v), want (-91, true)", v, ok)
	}
	if v >= silentTrackDB {
		t.Errorf("-91 dB must count as silent (floor %v)", silentTrackDB)
	}

	// Ordinary speech sits well above the floor.
	v, ok = parseMeanVolumeDB("[Parsed_volumedetect_0 @ 0x1] mean_volume: -23.7 dB")
	if !ok || v != -23.7 {
		t.Fatalf("speech level: got (%v, %v)", v, ok)
	}
	if v < silentTrackDB {
		t.Errorf("-23.7 dB must NOT count as silent")
	}

	// Unparseable output must fail open (ok=false), never block a transcript.
	if _, ok := parseMeanVolumeDB("ffmpeg version 7.1\nno stats here"); ok {
		t.Error("missing mean_volume must return ok=false")
	}
}

func TestTranscribeFile_SilentTrackIsRefusedNotHallucinated(t *testing.T) {
	// A screen recording made without the mic: whisper would emit a few
	// repeated tokens that read like a real transcript. The guard must
	// fire first and return nothing.
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		if strings.Contains(strings.Join(args, " "), "volumedetect") {
			return nil, []byte("[Parsed_volumedetect_0 @ 0x1] mean_volume: -91.0 dB"), nil
		}
		if strings.Contains(name, "whisper") {
			t.Fatal("whisper must not run on a silent track")
		}
		return nil, nil, nil
	})
	p := &sttPipeline{ffmpeg: "/bin/ffmpeg", whisper: "/bin/whisper-cli", model: "/m"}

	_, err := p.transcribeFile(context.Background(), "/tmp/clip.mp4", "ru")
	if err == nil {
		t.Fatal("silent track must be an error, not a transcript")
	}
	if !strings.Contains(err.Error(), "silent") || !strings.Contains(err.Error(), "-91.0") {
		t.Fatalf("error should name the silence and the level, got: %v", err)
	}
}

func TestTranscribeFile_UnreadableVolumeFailsOpen(t *testing.T) {
	// If the probe itself gives nothing parseable, transcription must
	// still proceed — the guard only fires on a positive silence reading.
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		if strings.Contains(name, "whisper") {
			return []byte("real speech"), nil, nil
		}
		return nil, []byte("ffmpeg version 7.1"), nil
	})
	p := &sttPipeline{ffmpeg: "/bin/ffmpeg", whisper: "/bin/whisper-cli", model: "/m"}

	text, err := p.transcribeFile(context.Background(), "/tmp/a.m4a", "auto")
	if err != nil || text != "real speech" {
		t.Fatalf("unparseable volume must not block: got (%q, %v)", text, err)
	}
}
