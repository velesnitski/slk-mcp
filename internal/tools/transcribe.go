package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

	// Screen recordings and huddles routinely carry MORE THAN ONE audio
	// stream (system audio + microphone). ffmpeg's default pick is a
	// single "best" stream, so a recording whose mic sits on stream 1
	// while stream 0 is silent transcribes as silence. Mix every audio
	// stream instead; with a single stream the command is the plain
	// conversion it always was.
	args := []string{"-y", "-loglevel", "error", "-i", audioPath}
	if n := p.audioStreamCount(ctx, audioPath); n > 1 {
		args = append(args, "-filter_complex", fmt.Sprintf("amix=inputs=%d:duration=longest:normalize=0", n))
	} else {
		args = append(args, "-vn")
	}
	args = append(args, "-ar", "16000", "-ac", "1", wav)
	if _, se, err := runCommand(ctx, p.ffmpeg, args...); err != nil {
		return "", fmt.Errorf("ffmpeg: %v: %s", err, firstErrLine(se))
	}

	// Whisper HALLUCINATES on silence — a screen recording made without
	// the mic yields a confident-looking transcript of a few repeated
	// tokens, which a caller then relays as if it were speech. Measure
	// the track first and fail loudly instead.
	if mean, ok := meanVolumeDB(ctx, p.ffmpeg, wav); ok && mean < silentTrackDB {
		return "", fmt.Errorf("audio track is silent (mean volume %.1f dB): the recording captured no usable sound, most often a screen recording made without the microphone. Whisper output on a silent track is hallucinated, not speech, so nothing is returned", mean)
	}

	// NEVER pass -nt/--no-timestamps. It looks like the obvious way to ask
	// for clean text, but in whisper.cpp 1.9.x it derails the decoder: the
	// run collapses into a couple of repeated tokens and stops after the
	// first segment, so a 90-second clip returns three garbage words in a
	// second and reads like a plausible transcript. Ask for timestamps and
	// strip them here instead.
	wArgs := []string{"-m", p.model, "-np"}
	if language != "" {
		wArgs = append(wArgs, "-l", language)
	}
	wArgs = append(wArgs, wav)
	so, se, err := runCommand(ctx, p.whisper, wArgs...)
	if err != nil {
		return "", fmt.Errorf("whisper: %v: %s", err, firstErrLine(se))
	}
	text := stripTimestamps(string(so))
	if text == "" {
		return "", fmt.Errorf("whisper produced no transcript (stderr: %s)", firstErrLine(se))
	}
	return text, nil
}

// timestampPrefix matches the "[00:00:00.000 --> 00:00:07.000]" lead-in
// whisper prints ahead of every segment.
var timestampPrefix = regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\.\d{3} --> \d{2}:\d{2}:\d{2}\.\d{3}\]\s*`)

// stripTimestamps turns whisper's segment listing back into flowing text,
// dropping the per-segment timestamp prefixes and blank lines. Lines that
// carry no prefix pass through untouched. Pure.
func stripTimestamps(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(timestampPrefix.ReplaceAllString(strings.TrimSpace(line), ""))
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, " ")
}

// audioStreamCount reports how many audio streams a file carries.
// Returns 0 when ffprobe is missing or the probe fails, which the
// caller reads as "not more than one" and falls back to the plain
// single-stream conversion.
func (p *sttPipeline) audioStreamCount(ctx context.Context, path string) int {
	if p.ffprobe == "" {
		return 0
	}
	so, _, err := runCommand(ctx, p.ffprobe, "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=index", "-of", "csv=p=0", path)
	if err != nil {
		return 0
	}
	return countNonEmptyLines(string(so))
}

// countNonEmptyLines counts populated lines in ffprobe's CSV output —
// one per selected stream. Pure.
func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// silentTrackDB is the mean-volume floor below which a track carries no
// speech worth transcribing. Real speech, even quiet or far-mic, sits
// well above -50 dBFS mean; a track with no recorded input measures
// -70 dB or lower (digital silence reports -91 dB).
const silentTrackDB = -50.0

// meanVolumeDB measures a WAV's mean volume via ffmpeg's volumedetect
// filter. Returns ok=false when the measurement itself fails, so an
// unreadable level never blocks a transcript (fail-open: the silence
// guard only fires on a POSITIVE silence reading).
func meanVolumeDB(ctx context.Context, ffmpeg, wav string) (float64, bool) {
	// volumedetect reports on stderr at default log level; -f null
	// discards the decoded output.
	_, se, err := runCommand(ctx, ffmpeg, "-hide_banner", "-i", wav, "-af", "volumedetect", "-f", "null", "-")
	if err != nil {
		return 0, false
	}
	return parseMeanVolumeDB(string(se))
}

var meanVolumeRe = regexp.MustCompile(`mean_volume:\s*(-?\d+(?:\.\d+)?)\s*dB`)

// parseMeanVolumeDB extracts the mean volume from ffmpeg's volumedetect
// stderr block. Pure.
func parseMeanVolumeDB(stderr string) (float64, bool) {
	m := meanVolumeRe.FindStringSubmatch(stderr)
	if m == nil {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
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
			mcp.WithDescription("Transcribe a Slack voice message, audio/video clip, or recorded huddle to text using a local whisper.cpp install (ffmpeg + whisper-cli + model; video files have their audio track extracted). When the toolchain is missing, returns the downloaded file path plus exact install commands — clients with shell access can run them (with user consent) and retry. Pass a permalink, or channel + timestamp, or just a channel/DM to grab its newest voice note."),
			mcp.WithString("permalink", mcp.Description("Slack message permalink (…/archives/…/p…) OR a Slack file URL (…/files/…/F…/name) — either resolves the attachment on its own")),
			mcp.WithString("channel", mcp.Description("Channel name or ID, or a DM as @handle (optional if permalink is provided). With no timestamp, the newest matching attachment in this conversation is used.")),
			mcp.WithString("timestamp", mcp.Description("Message ts holding the audio (optional if permalink is provided)")),
			mcp.WithString("from", mcp.Description("Restrict latest-mode to one author: a @handle, or \"me\" for your own last voice note. Ignored when a permalink/timestamp is given.")),
			mcp.WithString("language", mcp.Description("Speech language as an ISO code (e.g. ru, en) or auto (default: auto)")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runTranscribeAudio(ctx,
				req.GetString("workspace", ""),
				req.GetString("channel", ""),
				req.GetString("timestamp", ""),
				req.GetString("permalink", ""),
				req.GetString("from", ""),
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
func (h *Hub) runTranscribeAudio(ctx context.Context, workspace, channel, timestamp, permalink, from, language, destDir string) *mcp.CallToolResult {
	saved, skipped, wsName, errRes := h.fetchFiles(ctx, workspace, channel, timestamp, permalink, from, destDir, "slk-audio", isTranscribableFile)
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
