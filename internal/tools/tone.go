package tools

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// analyze_audio_tone measures the PROSODY of a voice message — loudness
// dynamics and (when aubio is present) pitch — to estimate vocal arousal
// (calm/controlled vs agitated/shouting) that a transcript cannot
// capture. It orchestrates ffmpeg (required) and aubiopitch (optional),
// both host-provided, and degrades to a plain download when ffmpeg is
// missing. The metrics chosen survive the auto-normalization phone voice
// notes apply: absolute loudness is meaningless after normalization, but
// the RELATIVE spread (EBU R128 loudness range) and pitch do not lie.

// toneMetrics is the parsed acoustic summary for one clip.
type toneMetrics struct {
	LRA            float64 // EBU R128 loudness range, LU (spread of loudness)
	IntegratedLUFS float64
	CrestLinear    float64 // astats crest factor (peak/RMS, linear)
	RMSLevelDB     float64
	PeakLevelDB    float64
	PitchMeanHz    float64
	PitchHasData   bool
}

// CrestDB expresses the crest factor in dB (20·log10) for intuition —
// higher = punchier peaks over the average, lower = flatter/monotone.
func (m toneMetrics) CrestDB() float64 {
	if m.CrestLinear <= 0 {
		return 0
	}
	return 20 * math.Log10(m.CrestLinear)
}

// lraLabel maps the loudness range to a plain-language arousal band. The
// bands come from observed speech: a controlled delivery clusters low
// (a steady voice barely varies its loudness), an animated or shouting
// one swings wide.
func lraLabel(lra float64) string {
	switch {
	case lra < 4:
		return "очень ровный (монотон/спокойный)"
	case lra < 6:
		return "ровный, контролируемый — без взрывных скачков"
	case lra < 9:
		return "умеренная динамика (оживлён, но не срыв)"
	default:
		return "высокая динамика — возможно возбуждён/крик"
	}
}

// ffmpeg stderr / aubio stdout parsers. ebur128 and astats print their
// figures repeatedly (per-frame) then in a final summary; taking the
// LAST match lands on the summary value.
var (
	reLRA        = regexp.MustCompile(`LRA:\s*(-?\d+(?:\.\d+)?)\s*LU`)
	reIntegrated = regexp.MustCompile(`\bI:\s*(-?\d+(?:\.\d+)?)\s*LUFS`)
	reCrest      = regexp.MustCompile(`Crest factor:\s*(-?\d+(?:\.\d+)?)`)
	reRMSLevel   = regexp.MustCompile(`RMS level dB:\s*(-?\d+(?:\.\d+)?)`)
	rePeakLevel  = regexp.MustCompile(`Peak level dB:\s*(-?\d+(?:\.\d+)?)`)
)

// lastFloat returns the last regex capture parsed as a float — the
// summary value, since ffmpeg emits per-frame figures before it.
func lastFloat(re *regexp.Regexp, s string) (float64, bool) {
	m := re.FindAllStringSubmatch(s, -1)
	if len(m) == 0 {
		return 0, false
	}
	v, err := strconv.ParseFloat(m[len(m)-1][1], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// parseToneStderr pulls the acoustic metrics out of the combined
// astats+ebur128 ffmpeg stderr. Missing fields stay at their zero value;
// the renderer omits anything that didn't parse rather than lying.
func parseToneStderr(stderr string) toneMetrics {
	var m toneMetrics
	if v, ok := lastFloat(reLRA, stderr); ok {
		m.LRA = v
	}
	if v, ok := lastFloat(reIntegrated, stderr); ok {
		m.IntegratedLUFS = v
	}
	if v, ok := lastFloat(reCrest, stderr); ok {
		m.CrestLinear = v
	}
	if v, ok := lastFloat(reRMSLevel, stderr); ok {
		m.RMSLevelDB = v
	}
	if v, ok := lastFloat(rePeakLevel, stderr); ok {
		m.PeakLevelDB = v
	}
	return m
}

// parseAubioPitch averages the non-zero pitch estimates from aubiopitch
// stdout ("<time> <freqHz>" per line). Unvoiced frames read 0 and are
// dropped so silence/consonants don't drag the mean down.
func parseAubioPitch(stdout string) (float64, bool) {
	var sum float64
	var n int
	for _, line := range strings.Split(stdout, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		hz, err := strconv.ParseFloat(f[1], 64)
		if err != nil || hz <= 0 {
			continue
		}
		sum += hz
		n++
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

func (h *Hub) registerToneTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("analyze_audio_tone") {
		return
	}
	s.AddTool(
		mcp.NewTool("analyze_audio_tone",
			mcp.WithDescription("Estimate the VOCAL TONE of a Slack voice message — loudness dynamics (EBU R128 loudness range) and, when aubio is installed, pitch — to gauge whether the speaker is calm/controlled or agitated/shouting, which a transcript can't tell you. Requires ffmpeg; aubio (aubiopitch) adds pitch. Pass a permalink, or channel + timestamp."),
			mcp.WithString("permalink", mcp.Description("Slack message permalink — resolves channel and timestamp in one go")),
			mcp.WithString("channel", mcp.Description("Channel name or ID (optional if permalink is provided)")),
			mcp.WithString("timestamp", mcp.Description("Message ts holding the audio (optional if permalink is provided)")),
			mcp.WithString("workspace", mcp.Description(workspaceArgSingle)),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return h.runAnalyzeTone(ctx,
				req.GetString("workspace", ""),
				req.GetString("channel", ""),
				req.GetString("timestamp", ""),
				req.GetString("permalink", ""),
				""), nil
		},
	)
}

// analyzeTone runs the ffmpeg (+ optional aubio) acoustic pass over one
// audio file. ffmpegBin is required; aubioBin empty disables pitch.
func analyzeTone(ctx context.Context, ffmpegBin, aubioBin, path string) (toneMetrics, error) {
	_, stderr, err := runCommand(ctx, ffmpegBin, "-hide_banner", "-nostats", "-i", path,
		"-af", "astats=metadata=1,ebur128", "-f", "null", "-")
	if err != nil {
		return toneMetrics{}, fmt.Errorf("ffmpeg analysis: %v: %s", err, firstErrLine(stderr))
	}
	m := parseToneStderr(string(stderr))

	if aubioBin != "" {
		wav := path + ".tone.wav"
		if _, se, cerr := runCommand(ctx, ffmpegBin, "-y", "-loglevel", "error", "-i", path, "-ar", "16000", "-ac", "1", wav); cerr == nil {
			if so, _, aerr := runCommand(ctx, aubioBin, "-i", wav); aerr == nil {
				if hz, ok := parseAubioPitch(string(so)); ok {
					m.PitchMeanHz, m.PitchHasData = hz, true
				}
			}
			os.Remove(wav)
		} else {
			_ = se // pitch is best-effort; ffmpeg already succeeded above
		}
	}
	return m, nil
}

// runAnalyzeTone downloads the message's audio and reports the acoustic
// tone. When ffmpeg is missing it degrades to download-only (paths +
// hint), mirroring transcribe_audio. Analysed files are cleaned up.
func (h *Hub) runAnalyzeTone(ctx context.Context, workspace, channel, timestamp, permalink, destDir string) *mcp.CallToolResult {
	saved, skipped, wsName, errRes := h.fetchFiles(ctx, workspace, channel, timestamp, permalink, destDir, "slk-tone", isTranscribableFile)
	if errRes != nil {
		return errRes
	}

	ffmpegBin := h.cfg.FFmpegBin
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	ffPath, err := lookPath(ffmpegBin)
	if err != nil {
		var b strings.Builder
		fmt.Fprintf(&b, "ffmpeg not found (%s) — cannot analyse tone. Install it (brew install ffmpeg), then retry.\n", ffmpegBin)
		fmt.Fprintf(&b, "downloaded %d file(s)%s for manual analysis:\n", len(saved), h.wsLabel(wsName))
		for _, s := range saved {
			fmt.Fprintf(&b, "- %s (%s, %d bytes)\n", s.Path, s.Mimetype, s.Size)
		}
		return mcp.NewToolResultText(b.String())
	}
	aubioPath, _ := lookPath("aubiopitch") // optional; empty disables pitch

	var b strings.Builder
	for i, sf := range saved {
		m, aerr := analyzeTone(ctx, ffPath, aubioPath, sf.Path)
		os.Remove(sf.Path)
		if aerr != nil {
			fmt.Fprintf(&b, "## %s%s — analysis failed: %v\n", fileBase(sf.Path), h.wsLabel(wsName), aerr)
			continue
		}
		if len(saved) > 1 {
			fmt.Fprintf(&b, "## audio %d/%d — %s%s\n", i+1, len(saved), fileBase(sf.Path), h.wsLabel(wsName))
		} else {
			fmt.Fprintf(&b, "tone%s:\n", h.wsLabel(wsName))
		}
		fmt.Fprintf(&b, "- loudness range (LRA): %.1f LU → %s\n", m.LRA, lraLabel(m.LRA))
		fmt.Fprintf(&b, "- crest factor: %.1f dB (peaks over average)\n", m.CrestDB())
		fmt.Fprintf(&b, "- integrated loudness: %.1f LUFS; RMS %.1f dB; peak %.1f dB\n", m.IntegratedLUFS, m.RMSLevelDB, m.PeakLevelDB)
		if m.PitchHasData {
			fmt.Fprintf(&b, "- mean pitch: %.0f Hz\n", m.PitchMeanHz)
		} else {
			b.WriteString("- pitch: unavailable (install aubio — brew install aubio — for f0)\n")
		}
	}
	b.WriteString("\nnote: proxy, not an emotion model. Phone voice notes are auto-normalized, so absolute loudness is unreliable — read LRA (spread) and pitch, not the dB level. High LRA + high/variable pitch = agitated; low LRA + steady pitch = controlled.")
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\nskipped non-audio: %s", strings.Join(skipped, ", "))
	}
	return mcp.NewToolResultText(strings.TrimRight(b.String(), "\n"))
}

// fileBase is filepath.Base kept local to avoid widening this file's
// import surface for one call; temp paths use '/'.
func fileBase(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
