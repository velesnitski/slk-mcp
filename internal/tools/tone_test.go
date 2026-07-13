package tools

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// Real ffmpeg astats+ebur128 stderr shape (trimmed), including the
// per-frame lines that precede the summary — the parser must land on the
// summary (last) value, not a mid-clip frame.
const sampleToneStderr = `
[Parsed_ebur128_1 @ 0x1] t: 1.0  I: -20.0 LUFS  LRA: 2.0 LU
[Parsed_astats_0 @ 0x2] Crest factor: 6.686721
[Parsed_astats_0 @ 0x2] RMS level dB: -16.504265
[Parsed_astats_0 @ 0x2] Peak level dB: 0.000000
[Parsed_ebur128_1 @ 0x1] t: 39.3  I: -16.6 LUFS  LRA: 4.2 LU
    Integrated loudness:
      I:         -16.6 LUFS
    Loudness range:
      LRA:         4.2 LU
`

func TestParseToneStderr(t *testing.T) {
	m := parseToneStderr(sampleToneStderr)
	if m.LRA != 4.2 {
		t.Fatalf("LRA should be the summary value 4.2, got %v", m.LRA)
	}
	if m.IntegratedLUFS != -16.6 {
		t.Fatalf("integrated should be -16.6, got %v", m.IntegratedLUFS)
	}
	if m.CrestLinear < 6.6 || m.CrestLinear > 6.7 {
		t.Fatalf("crest linear ~6.69, got %v", m.CrestLinear)
	}
	if d := m.CrestDB(); math.Abs(d-16.5) > 0.2 {
		t.Fatalf("crest dB ~16.5, got %v", d)
	}
}

func TestParseToneStderr_missingFieldsAreZero(t *testing.T) {
	m := parseToneStderr("nothing useful here")
	if m.LRA != 0 || m.CrestLinear != 0 {
		t.Fatalf("absent fields must stay zero, got %+v", m)
	}
}

func TestLraLabel(t *testing.T) {
	if !strings.Contains(lraLabel(4.2), "ровный") {
		t.Fatalf("4.2 LU should read controlled, got %q", lraLabel(4.2))
	}
	if !strings.Contains(lraLabel(12), "крик") {
		t.Fatalf("12 LU should flag shouting, got %q", lraLabel(12))
	}
	if !strings.Contains(lraLabel(3), "монотон") {
		t.Fatalf("3 LU should read flat, got %q", lraLabel(3))
	}
}

// sine builds `n` samples of a pure tone at freqHz for testing the pitch
// detector against a known ground truth.
func sine(freqHz float64, sampleRate, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = 0.6 * math.Sin(2*math.Pi*freqHz*float64(i)/float64(sampleRate))
	}
	return s
}

func TestYinF0_recoversKnownFrequencies(t *testing.T) {
	// The whole point: YIN must return the true f0 of a synthetic tone.
	for _, f := range []float64{90, 150, 220, 330} {
		frame := sine(f, pitchSampleRate, pitchWindow)
		got, ok := yinF0(frame, pitchSampleRate, pitchFmin, pitchFmax, yinThreshold)
		if !ok {
			t.Fatalf("YIN should find a pitch for %.0f Hz", f)
		}
		if math.Abs(got-f) > 2.0 {
			t.Fatalf("YIN(%.0f Hz) = %.1f Hz, want within 2 Hz", f, got)
		}
	}
}

func TestYinF0_silenceIsUnvoiced(t *testing.T) {
	frame := make([]float64, pitchWindow) // all zeros
	if _, ok := yinF0(frame, pitchSampleRate, pitchFmin, pitchFmax, yinThreshold); ok {
		t.Fatal("silence must be unvoiced")
	}
}

func TestEstimatePitch_sineMeanAndLowVariability(t *testing.T) {
	samples := sine(150, pitchSampleRate, pitchSampleRate) // 1 second
	mean, std, voiced, ok := estimatePitch(samples, pitchSampleRate)
	if !ok {
		t.Fatal("a 1s tone must yield voiced frames")
	}
	if math.Abs(mean-150) > 2 {
		t.Fatalf("mean pitch = %.1f, want ~150", mean)
	}
	if std > 2 {
		t.Fatalf("a steady tone should have near-zero variability, got std %.2f", std)
	}
	if voiced < 0.9 {
		t.Fatalf("a pure tone should be almost fully voiced, got %.2f", voiced)
	}
}

func TestPcmFromInt16LE(t *testing.T) {
	// 0x0000 = 0.0, 0x00FF? build known samples: +16384 (~0.5), -16384 (~-0.5)
	b := []byte{0x00, 0x40, 0x00, 0xC0} // 16384, -16384 (LE)
	s := pcmFromInt16LE(b)
	if len(s) != 2 {
		t.Fatalf("2 samples expected, got %d", len(s))
	}
	if math.Abs(s[0]-0.5) > 0.01 || math.Abs(s[1]+0.5) > 0.01 {
		t.Fatalf("decoded %v, want [~0.5, ~-0.5]", s)
	}
}

// int16LE encodes float samples back to PCM bytes for the analyzeTone stub.
func int16LE(samples []float64) []byte {
	b := make([]byte, 2*len(samples))
	for i, v := range samples {
		x := int16(v * 32767)
		b[2*i] = byte(x)
		b[2*i+1] = byte(x >> 8)
	}
	return b
}

func TestAnalyzeTone_runsLoudnessThenPitch(t *testing.T) {
	pcm := int16LE(sine(150, pitchSampleRate, pitchSampleRate))
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "ebur128") {
			return nil, []byte(sampleToneStderr), nil // loudness pass → stderr
		}
		if strings.Contains(joined, "s16le") {
			return pcm, nil, nil // pitch decode → stdout PCM
		}
		return nil, nil, nil
	})
	m, err := analyzeTone(context.Background(), "/bin/ffmpeg", "/tmp/v.m4a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.LRA != 4.2 {
		t.Fatalf("LRA should parse from ffmpeg stderr, got %v", m.LRA)
	}
	if !m.PitchHasData || math.Abs(m.PitchMeanHz-150) > 3 {
		t.Fatalf("native pitch should recover ~150 Hz, got %+v", m)
	}
}

func TestAnalyzeTone_ffmpegFailure(t *testing.T) {
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("bad input\nmore"), errors.New("exit 1")
	})
	if _, err := analyzeTone(context.Background(), "/bin/ffmpeg", "/tmp/v.m4a"); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("expected ffmpeg error, got %v", err)
	}
}

func TestRunAnalyzeTone_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runAnalyzeTone(context.Background(), "ghost", "#c", "1.1", "", "", "")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}
