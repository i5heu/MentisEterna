package media

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"unicode"
)

const (
	sttChunkSeconds        = 30.0
	sttChunkOverlapSeconds = 3.0
	// Minimum overlapping words (after normalization) required before the
	// merger dedupes; prevents collapsing on short common phrases. A 3-second
	// overlap carries roughly 5-9 words at common speech rates, so a match of
	// at least 4 normalized words is a strong signal the model transcribed the
	// same audio in both chunks.
	sttChunkMinOverlapWords = 4
)

type sttChunkWindow struct {
	Start  float64 // seconds from file start
	Length float64 // seconds, clamped to remaining file duration
}

// sttChunkWindows splits a duration into 30-second windows overlapping by
// 3 seconds: chunk i covers [i*27, i*27+30) clamped to duration. Durations at
// or below one chunk yield a single window; non-positive durations yield a
// zero-length window.
func sttChunkWindows(duration float64) []sttChunkWindow {
	if duration <= sttChunkSeconds {
		return []sttChunkWindow{{0, math.Max(0, duration)}}
	}
	step := sttChunkSeconds - sttChunkOverlapSeconds
	var wins []sttChunkWindow
	for start := 0.0; start < duration; start += step {
		wins = append(wins, sttChunkWindow{start, math.Min(sttChunkSeconds, duration-start)})
	}
	return wins
}

// sttChunkAvailable reports whether both ffmpeg and ffprobe are on PATH, the
// prerequisites for chunked transcription. Without them transcription falls
// back to whole-file requests.
func sttChunkAvailable() bool {
	_, ffmpegErr := exec.LookPath("ffmpeg")
	_, ffprobeErr := exec.LookPath("ffprobe")
	return ffmpegErr == nil && ffprobeErr == nil
}

// probeAudioDuration reads the format duration of srcPath via ffprobe.
func probeAudioDuration(ctx context.Context, srcPath string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		srcPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("stt: probe duration: %w\n%s", err, string(out))
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("stt: probe duration: %w", err)
	}
	return dur, nil
}

// cutAudioChunk extracts [start, start+length) from srcPath as mono 16 kHz
// 16-bit PCM WAV at outPath (the whisper-standard input format; the .wav
// extension drives the STT multipart filename).
func cutAudioChunk(ctx context.Context, srcPath, outPath string, start, length float64) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", length),
		"-i", srcPath,
		"-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le",
		outPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("stt: cut chunk at %.3fs: %w\n%s", start, err, string(out))
	}
	return nil
}

// normalizeWord lowercases w and keeps only letter/digit runes, so "Hello,"
// and "hello" compare equal.
func normalizeWord(w string) string {
	var b strings.Builder
	b.Grow(len(w))
	for _, r := range strings.ToLower(w) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sttMergeOverlap returns the largest k such that the last k words of prev
// (normalized) equal the first k words of next (normalized). k must be at
// least sttChunkMinOverlapWords; 0 is returned when no overlap meets the
// threshold.
func sttMergeOverlap(prev, next []string) int {
	maxK := len(prev)
	if len(next) < maxK {
		maxK = len(next)
	}
	for k := maxK; k >= sttChunkMinOverlapWords; k-- {
		match := true
		for j := range k {
			if normalizeWord(prev[len(prev)-k+j]) != normalizeWord(next[j]) {
				match = false
				break
			}
		}
		if match {
			return k
		}
	}
	return 0
}

// mergeTranscriptions concatenates per-chunk transcripts, dropping the
// overlapping tail of each chunk when the next chunk repeats it (deterministic
// normalized word match only; differing overlaps are kept, joined by spaces).
func mergeTranscriptions(parts []string) string {
	var words []string
	for i, part := range parts {
		nextWords := strings.Fields(part)
		if i > 0 {
			nextWords = nextWords[sttMergeOverlap(words, nextWords):]
		}
		words = append(words, nextWords...)
	}
	return strings.Join(words, " ")
}
