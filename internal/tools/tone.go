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
// dynamics and pitch — to estimate vocal arousal (calm/controlled vs
// agitated/shouting) that a transcript cannot capture. It orchestrates
// ffmpeg (required, already host-provided for transcribe_audio) and
// nothing else: pitch (f0) is computed natively in Go via YIN over the
// PCM ffmpeg decodes, so there is ZERO extra system dependency (no
// aubio → no gcc/openblas/numpy toolchain). The metrics chosen survive
// the auto-normalization phone voice notes apply: absolute loudness is
// meaningless after normalization, but the RELATIVE loudness spread
// (EBU R128 loudness range) and pitch do not lie.

// Pitch estimation config. Human speech f0 sits ~70–400 Hz; framing at
// 1024/512 (window/hop) resolves that range at 16 kHz while keeping the
// per-frame YIN cost small.
const (
	pitchSampleRate = 16000
	pitchFmin       = 70.0
	pitchFmax       = 400.0
	pitchWindow     = 1024
	pitchHop        = 512
	yinThreshold    = 0.15
	pitchRMSGate    = 0.01 // skip near-silent frames
)

// toneMetrics is the parsed acoustic summary for one clip.
type toneMetrics struct {
	LRA            float64 // EBU R128 loudness range, LU (spread of loudness)
	IntegratedLUFS float64
	CrestLinear    float64 // astats crest factor (peak/RMS, linear)
	RMSLevelDB     float64
	PeakLevelDB    float64
	PitchMeanHz    float64
	PitchStdHz     float64 // f0 variability — a second arousal signal
	PitchVoiced    float64 // fraction of frames with a confident pitch
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
// bands come from observed speech: a controlled delivery clusters low (a
// steady voice barely varies its loudness), an animated or shouting one
// swings wide.
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

// ffmpeg stderr parsers. ebur128 and astats print their figures
// repeatedly (per-frame) then in a final summary; taking the LAST match
// lands on the summary value.
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

// parseToneStderr pulls the loudness metrics out of the combined
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

// pcmFromInt16LE turns raw signed-16-bit little-endian mono PCM bytes
// into normalized [-1,1) float samples.
func pcmFromInt16LE(b []byte) []float64 {
	n := len(b) / 2
	s := make([]float64, n)
	for i := 0; i < n; i++ {
		v := int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
		s[i] = float64(v) / 32768.0
	}
	return s
}

// frameRMS is the root-mean-square amplitude of a frame — the voicing/
// energy gate that keeps silence and breaths out of the pitch average.
func frameRMS(x []float64) float64 {
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

// parabolicMin refines the integer minimum at tau to sub-sample accuracy
// by fitting a parabola through its neighbours — YIN's standard step 5.
func parabolicMin(c []float64, tau int) float64 {
	if tau <= 0 || tau >= len(c)-1 {
		return float64(tau)
	}
	s0, s1, s2 := c[tau-1], c[tau], c[tau+1]
	denom := s0 - 2*s1 + s2
	if denom == 0 {
		return float64(tau)
	}
	return float64(tau) + 0.5*(s0-s2)/denom
}

// yinF0 estimates the fundamental frequency of one frame with the YIN
// cumulative-mean-normalized difference (de Cheveigné & Kawahara 2002).
// Returns (f0Hz, true) on a confident pitch inside [fmin,fmax], else
// (0,false) — the voicing decision is the threshold: no dip below it
// means no periodicity, i.e. unvoiced.
func yinF0(x []float64, sampleRate int, fmin, fmax, threshold float64) (float64, bool) {
	tauMin := int(float64(sampleRate) / fmax)
	tauMax := int(float64(sampleRate) / fmin)
	if tauMax >= len(x) {
		tauMax = len(x) - 1
	}
	if tauMin < 1 {
		tauMin = 1
	}
	if tauMax <= tauMin {
		return 0, false
	}

	// Difference function d(tau).
	d := make([]float64, tauMax+1)
	for tau := 1; tau <= tauMax; tau++ {
		var sum float64
		for j := 0; j+tau < len(x); j++ {
			diff := x[j] - x[j+tau]
			sum += diff * diff
		}
		d[tau] = sum
	}

	// Cumulative mean normalized difference d'(tau).
	cmnd := make([]float64, tauMax+1)
	cmnd[0] = 1
	var run float64
	for tau := 1; tau <= tauMax; tau++ {
		run += d[tau]
		if run == 0 {
			cmnd[tau] = 1
		} else {
			cmnd[tau] = d[tau] * float64(tau) / run
		}
	}

	// Absolute threshold: first tau below it (in range), stepped to the
	// local minimum, then parabolically refined.
	for tau := tauMin; tau <= tauMax; tau++ {
		if cmnd[tau] < threshold {
			for tau+1 <= tauMax && cmnd[tau+1] < cmnd[tau] {
				tau++
			}
			f0 := float64(sampleRate) / parabolicMin(cmnd, tau)
			if f0 >= fmin && f0 <= fmax {
				return f0, true
			}
			return 0, false
		}
	}
	return 0, false
}

// estimatePitch runs YIN over overlapping frames and aggregates the
// voiced ones into mean f0, its standard deviation (variability — an
// arousal signal: agitated speech moves its pitch more), and the voiced
// fraction. ok is false when nothing voiced was found.
func estimatePitch(samples []float64, sampleRate int) (mean, std, voicedFrac float64, ok bool) {
	if len(samples) < pitchWindow {
		return 0, 0, 0, false
	}
	var f0s []float64
	frames := 0
	for start := 0; start+pitchWindow <= len(samples); start += pitchHop {
		frame := samples[start : start+pitchWindow]
		frames++
		if frameRMS(frame) < pitchRMSGate {
			continue
		}
		if f0, voiced := yinF0(frame, sampleRate, pitchFmin, pitchFmax, yinThreshold); voiced {
			f0s = append(f0s, f0)
		}
	}
	if len(f0s) == 0 || frames == 0 {
		return 0, 0, 0, false
	}
	for _, v := range f0s {
		mean += v
	}
	mean /= float64(len(f0s))
	for _, v := range f0s {
		std += (v - mean) * (v - mean)
	}
	std = math.Sqrt(std / float64(len(f0s)))
	return mean, std, float64(len(f0s)) / float64(frames), true
}

func (h *Hub) registerToneTools(s *server.MCPServer) {
	if h.cfg.IsDisabled("analyze_audio_tone") {
		return
	}
	s.AddTool(
		mcp.NewTool("analyze_audio_tone",
			mcp.WithDescription("Estimate the VOCAL TONE of a Slack voice message — loudness dynamics (EBU R128 loudness range) and pitch (native f0, mean + variability) — to gauge whether the speaker is calm/controlled or agitated/shouting, which a transcript can't tell you. Needs only ffmpeg (pitch is computed in-process, no extra install). Pass a permalink, or channel + timestamp."),
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

// analyzeTone runs the ffmpeg loudness pass and the native pitch pass
// over one audio file. ffmpegBin is required.
func analyzeTone(ctx context.Context, ffmpegBin, path string) (toneMetrics, error) {
	_, stderr, err := runCommand(ctx, ffmpegBin, "-hide_banner", "-nostats", "-i", path,
		"-af", "astats=metadata=1,ebur128", "-f", "null", "-")
	if err != nil {
		return toneMetrics{}, fmt.Errorf("ffmpeg analysis: %v: %s", err, firstErrLine(stderr))
	}
	m := parseToneStderr(string(stderr))

	// Native pitch: decode to raw mono PCM, then YIN in-process — no
	// external pitch binary. A decode failure only drops pitch; the
	// loudness read above already succeeded.
	if pcm, _, perr := runCommand(ctx, ffmpegBin, "-loglevel", "error", "-i", path,
		"-ac", "1", "-ar", strconv.Itoa(pitchSampleRate), "-f", "s16le", "-"); perr == nil {
		if mean, std, voiced, ok := estimatePitch(pcmFromInt16LE(pcm), pitchSampleRate); ok {
			m.PitchMeanHz, m.PitchStdHz, m.PitchVoiced, m.PitchHasData = mean, std, voiced, true
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

	var b strings.Builder
	for i, sf := range saved {
		m, aerr := analyzeTone(ctx, ffPath, sf.Path)
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
			fmt.Fprintf(&b, "- pitch (f0): mean %.0f Hz, variability ±%.0f Hz (voiced %.0f%%)\n",
				m.PitchMeanHz, m.PitchStdHz, m.PitchVoiced*100)
		} else {
			b.WriteString("- pitch (f0): no voiced frames detected\n")
		}
	}
	b.WriteString("\nnote: proxy, not an emotion model. Phone voice notes are auto-normalized, so absolute loudness is unreliable — read LRA (loudness spread) and pitch, not the dB level. Low LRA + steady pitch = controlled; high LRA + high pitch variability = agitated/shouting.")
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
