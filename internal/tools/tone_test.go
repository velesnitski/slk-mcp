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
	if m.RMSLevelDB != -16.504265 {
		t.Fatalf("RMS -16.50, got %v", m.RMSLevelDB)
	}
	// 6.687 linear ≈ 16.5 dB
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

func TestParseAubioPitch(t *testing.T) {
	// aubiopitch: "<time> <freqHz>"; 0.0 = unvoiced, must be dropped.
	out := "0.00 0.000000\n0.01 118.5\n0.02 122.0\n0.03 0.000000\n0.04 120.0\n"
	hz, ok := parseAubioPitch(out)
	if !ok {
		t.Fatal("expected voiced frames")
	}
	// mean of 118.5, 122.0, 120.0 ≈ 120.17
	if math.Abs(hz-120.17) > 0.1 {
		t.Fatalf("mean pitch ~120.17, got %v", hz)
	}
	if _, ok := parseAubioPitch("0.00 0.0\n0.01 0.0\n"); ok {
		t.Fatal("all-unvoiced should report no data")
	}
}

func TestAnalyzeTone_ffmpegRuns(t *testing.T) {
	var gotArgs []string
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return nil, []byte(sampleToneStderr), nil
	})
	m, err := analyzeTone(context.Background(), "/bin/ffmpeg", "", "/tmp/v.m4a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.LRA != 4.2 {
		t.Fatalf("LRA should parse from ffmpeg stderr, got %v", m.LRA)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "ebur128") || !strings.Contains(joined, "astats") {
		t.Fatalf("ffmpeg must run astats+ebur128, got %v", gotArgs)
	}
}

func TestAnalyzeTone_ffmpegFailure(t *testing.T) {
	withSTTStubs(t, nil, func(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("bad input\nmore"), errors.New("exit 1")
	})
	if _, err := analyzeTone(context.Background(), "/bin/ffmpeg", "", "/tmp/v.m4a"); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("expected ffmpeg error, got %v", err)
	}
}

func TestRunAnalyzeTone_UnknownWorkspaceIsError(t *testing.T) {
	hub := twoWorkspaceHub(t)
	res := hub.runAnalyzeTone(context.Background(), "ghost", "#c", "1.1", "", "")
	if res == nil || !res.IsError {
		t.Fatalf("unknown workspace should error, got %+v", res)
	}
}
