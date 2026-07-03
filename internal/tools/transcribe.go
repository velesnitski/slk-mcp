package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// transcribe_audio runs a local speech-to-text pipeline over a voice
// message: download (same path as download_audio) → ffmpeg to 16 kHz
// mono WAV → whisper.cpp. Both binaries and the model live on the HOST,
// not in this repo — the server only orchestrates them, and degrades to
// download_audio behaviour (paths + a setup hint) when the toolchain is
// missing. That keeps the server dependency-free while making the
// common case one tool call.

// sttSetupHint is the complete one-time toolchain install, emitted
// whenever transcribe_audio degrades to download-only. It is written
// FOR THE CALLING AGENT: an MCP client with shell access (e.g. Claude
// Code) can offer to run these commands and retry, which makes the
// missing-toolchain case self-healing for new users. The server itself
// deliberately never installs anything — executing package managers
// from a Slack-facing process is not a power this tool should have.
const sttSetupHint = `
to enable transcription, install the local toolchain (one-time, macOS/Homebrew;
other platforms: see the whisper.cpp README):
  brew install ffmpeg whisper-cpp
  mkdir -p ~/.cache/whisper
  curl -L -o ~/.cache/whisper/ggml-small.bin \
    "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin"
AI agents with shell access: offer to run these commands for the user (confirm
first — ~470 MB model download), then call transcribe_audio again.
Overrides: SLACK_FFMPEG_BIN, SLACK_WHISPER_BIN, SLACK_WHISPER_MODEL.`

// sttPipeline is the resolved external toolchain. ffprobe is optional
// (empty when absent) — it only enriches output with durations and its
// absence must never block transcription.
type sttPipeline struct {
	ffmpeg  string
	ffprobe string
	whisper string
	model   string
}

// lookPath / runCommand are seams for tests: lookPath resolves binaries
// on PATH, runCommand executes one external process and returns stdout
// and stderr separately (whisper prints its transcript to stdout but
// spills model-loading noise to stderr — mixing them would corrupt the
// transcript).
var (
	lookPath = exec.LookPath

	runCommand = func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
		cmd := exec.CommandContext(ctx, name, args...)
		var so, se bytes.Buffer
		cmd.Stdout, cmd.Stderr = &so, &se
		err = cmd.Run()
		return so.Bytes(), se.Bytes(), err
	}
)

// defaultWhisperModel is where the README how-to installs the model.
func defaultWhisperModel() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "whisper", "ggml-small.bin")
}

// detectSTT resolves the pipeline from config/env (SLACK_FFMPEG_BIN,
// SLACK_WHISPER_BIN, SLACK_WHISPER_MODEL) with PATH + well-known-path
// defaults. A non-empty reason means the pipeline is unavailable and
// says which piece is missing and how to install it.
func detectSTT(ffmpegBin, whisperBin, model string) (*sttPipeline, string) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	if whisperBin == "" {
		whisperBin = "whisper-cli"
	}
	ffPath, err := lookPath(ffmpegBin)
	if err != nil {
		return nil, ffmpegBin + " not found in PATH (brew install ffmpeg)"
	}
	wPath, err := lookPath(whisperBin)
	if err != nil {
		return nil, whisperBin + " not found in PATH (brew install whisper-cpp)"
	}
	if model == "" {
		model = defaultWhisperModel()
	}
	if _, err := os.Stat(model); err != nil {
		return nil, "whisper model not found at " + model + " — download it (see README \"How-to: transcribe voice messages\") or point SLACK_WHISPER_MODEL at a ggml model file"
	}
	// ffprobe ships alongside ffmpeg in every common install; treat it
	// as a nice-to-have (durations in output), never a requirement.
	fpPath, err := lookPath("ffprobe")
	if err != nil {
		fpPath = ""
	}
	return &sttPipeline{ffmpeg: ffPath, ffprobe: fpPath, whisper: wPath, model: model}, ""
}

// probeDuration returns a human-readable media duration ("2:13",
// "1:02:03") or "" when ffprobe is missing or fails — output
// enrichment only, never an error path.
func (p *sttPipeline) probeDuration(ctx context.Context, path string) string {
	if p.ffprobe == "" {
		return ""
	}
	so, _, err := runCommand(ctx, p.ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	if err != nil {
		return ""
	}
	var secs float64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(so)), "%f", &secs); err != nil || secs <= 0 {
		return ""
	}
	total := int(secs + 0.5)
	h, m, s := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// firstErrLine trims command stderr down to its most informative line
// for error messages. (Distinct from channels.go firstLine, which
// renders "(none)" placeholders for topic display.)
func firstErrLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	return s
}

// transcribeFile converts one audio file to 16 kHz mono WAV and runs
// whisper over it. language may be "auto" (whisper detects) or an ISO
// code like "ru". The intermediate WAV is always cleaned up.
func (p *sttPipeline) transcribeFile(ctx context.Context, audioPath, language string) (string, error) {
	wav := audioPath + ".wav"
	defer os.Remove(wav)

	// -vn drops any video stream: recorded huddles and clips arrive as
	// video/mp4, and whisper only wants the audio track.
	if _, se, err := runCommand(ctx, p.ffmpeg, "-y", "-loglevel", "error", "-i", audioPath, "-vn", "-ar", "16000", "-ac", "1", wav); err != nil {
		return "", fmt.Errorf("ffmpeg: %v: %s", err, firstErrLine(se))
	}

	args := []string{"-m", p.model, "-np", "-nt"}
	if language != "" {
		args = append(args, "-l", language)
	}
	args = append(args, wav)
	so, se, err := runCommand(ctx, p.whisper, args...)
	if err != nil {
		return "", fmt.Errorf("whisper: %v: %s", err, firstErrLine(se))
	}
	text := strings.TrimSpace(string(so))
	if text == "" {
		return "", fmt.Errorf("whisper produced no transcript (stderr: %s)", firstErrLine(se))
	}
	return text, nil
}

// registerTranscribeTools wires transcribe_audio. Registered alongside
// download_audio (audio.go); split into its own file because this is
// the only tool that shells out to host binaries.
func (h *Hub) registerTranscribeTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("transcribe_audio") {
		return
	}
	s.AddTool(
		mcp.NewTool("transcribe_audio",
			mcp.WithDescription("Transcribe a Slack voice message, audio/video clip, or recorded huddle to text using a local whisper.cpp install (ffmpeg + whisper-cli + model; video files have their audio track extracted). When the toolchain is missing, returns the downloaded file path plus exact install commands — clients with shell access can run them (with user consent) and retry. Pass a permalink, or channel + timestamp."),
			mcp.WithString("permalink", mcp.Description("Slack message permalink — resolves channel and timestamp in one go")),
			mcp.WithString("channel", mcp.Description("Channel name or ID (optional if permalink is provided)")),
			mcp.WithString("timestamp", mcp.Description("Message ts holding the audio (optional if permalink is provided)")),
			mcp.WithString("language", mcp.Description("Speech language as an ISO code (e.g. ru, en) or auto (default: auto)")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runTranscribeAudio(ctx,
				req.GetString("workspace", ""),
				req.GetString("channel", ""),
				req.GetString("timestamp", ""),
				req.GetString("permalink", ""),
				req.GetString("language", "auto"),
				""), nil
		},
	)
}

// runTranscribeAudio downloads the message's audio and, when the local
// STT toolchain is present, returns transcripts; otherwise it returns
// the downloaded paths plus the missing-toolchain reason so the caller
// can still proceed by hand. Successfully transcribed audio files are
// removed — the transcript is the artifact that matters.
func (h *Hub) runTranscribeAudio(ctx context.Context, workspace, channel, timestamp, permalink, language, destDir string) *mcp.CallToolResult {
	saved, skipped, wsName, errRes := h.fetchAudioFiles(ctx, workspace, channel, timestamp, permalink, destDir, isTranscribableFile)
	if errRes != nil {
		return errRes
	}

	pipeline, reason := detectSTT(h.cfg.FFmpegBin, h.cfg.WhisperBin, h.cfg.WhisperModel)
	if pipeline == nil {
		var b strings.Builder
		fmt.Fprintf(&b, "local speech-to-text unavailable: %s\n", reason)
		fmt.Fprintf(&b, "downloaded %d audio file(s)%s for manual transcription:\n", len(saved), h.wsLabel(wsName))
		for _, s := range saved {
			fmt.Fprintf(&b, "- %s (%s, %d bytes)\n", s.Path, s.Mimetype, s.Size)
		}
		b.WriteString(sttSetupHint)
		return mcp.NewToolResultText(b.String())
	}

	var b strings.Builder
	for i, s := range saved {
		duration := pipeline.probeDuration(ctx, s.Path)
		text, err := pipeline.transcribeFile(ctx, s.Path, language)
		if err != nil {
			fmt.Fprintf(&b, "## %s%s — transcription failed: %v (file kept at %s)\n", filepath.Base(s.Path), h.wsLabel(wsName), err, s.Path)
			continue
		}
		os.Remove(s.Path)
		meta := "language: " + language
		if duration != "" {
			meta += ", duration: " + duration
		}
		if len(saved) > 1 {
			fmt.Fprintf(&b, "## audio %d/%d — %s%s (%s)\n", i+1, len(saved), filepath.Base(s.Path), h.wsLabel(wsName), meta)
		} else {
			fmt.Fprintf(&b, "transcript%s (%s):\n", h.wsLabel(wsName), meta)
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "skipped non-audio: %s\n", strings.Join(skipped, ", "))
	}
	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n"))
}
