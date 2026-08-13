package media

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"
)

// blockingSTTer blocks inside RunSTT until release is closed and signals
// entry by closing entered.
type blockingSTTer struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSTTer) RunSTT(_ []byte, _ string) (string, error) {
	close(b.entered)
	<-b.release
	return "transcribed", nil
}

// TestRunSTTForFileSerializesPerFile verifies that two concurrent
// RunSTTForFile calls for the same file do not run the STT model in parallel:
// the second call blocks until the first completes.
func TestRunSTTForFileSerializesPerFile(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	// Insert a files row directly (pattern of TestSaveAndGetOCRResult).
	key, err := GenerateFileKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	nonce, err := GenerateBaseNonce()
	if err != nil {
		t.Fatalf("generate nonce: %v", err)
	}

	cipherFile, err := os.CreateTemp(t.TempDir(), "clip-*.m4a")
	if err != nil {
		t.Fatalf("create cipher file: %v", err)
	}
	cipherSHA, _, cipherSize, err := EncryptToFile(bytes.NewReader([]byte("audio")), cipherFile, key, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cipherData, err := os.ReadFile(cipherFile.Name())
	if err != nil {
		t.Fatalf("read cipher file: %v", err)
	}
	cipherFile.Close()

	res, err := svc.DB.Exec(
		`INSERT INTO files (storage_key, filename, mime_type, size_bytes, ciphertext_sha256, aes_key, aes_nonce)
		 VALUES (?, 'clip.m4a', 'audio/mp4', ?, ?, ?, ?)`,
		"stt-test-key", cipherSize, cipherSHA, key, nonce,
	)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	// Put ciphertext in cache (same call pattern as the replica path in stt.go).
	if err := svc.Cache.Put(fileID, cipherSHA, bytes.NewReader(cipherData)); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	release := make(chan struct{})
	stt1 := &blockingSTTer{entered: make(chan struct{}), release: release}
	stt2 := &blockingSTTer{entered: make(chan struct{}), release: release}

	type result struct {
		res *STTResult
		err error
	}
	results := make(chan result, 2)

	go func() {
		r, err := svc.RunSTTForFile(ctx, fileID, stt1)
		results <- result{r, err}
	}()
	select {
	case <-stt1.entered: // first call is inside the STT model, holding the lock
	case <-time.After(2 * time.Second):
		t.Fatal("first RunSTTForFile never entered the STT model")
	}

	go func() {
		r, err := svc.RunSTTForFile(ctx, fileID, stt2)
		results <- result{r, err}
	}()

	// The second call must NOT enter the STT model while the first is in flight.
	select {
	case <-stt2.entered:
		t.Fatal("second RunSTTForFile entered the STT model while the first was in flight — per-file serialization missing")
	case <-time.After(300 * time.Millisecond):
	}

	close(release) // first call finishes; second proceeds afterwards

	for i := range 2 {
		r := <-results
		if r.err != nil {
			t.Fatalf("RunSTTForFile %d: %v", i, r.err)
		}
		if r.res == nil || r.res.STTText != "transcribed" {
			t.Fatalf("RunSTTForFile %d: unexpected result %+v", i, r.res)
		}
	}

	// Both calls completed, so the second must have entered the model after the
	// first released (close happens-before the result send we already received).
	select {
	case <-stt2.entered:
	default:
		t.Fatal("second RunSTTForFile never entered the STT model after the lock was released")
	}
}

// recordingSTTer captures every (audioData, filename) it receives and returns
// a per-call part label.
type recordingSTTer struct {
	mu    sync.Mutex
	calls []sttCall
}

type sttCall struct {
	data     []byte
	filename string
}

func (r *recordingSTTer) RunSTT(audioData []byte, filename string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.calls)
	r.calls = append(r.calls, sttCall{data: append([]byte(nil), audioData...), filename: filename})
	return fmt.Sprintf("part%02d ", n), nil
}

func TestSttChunkWindows(t *testing.T) {
	tests := []struct {
		name     string
		duration float64
		want     []sttChunkWindow
	}{
		{"zero duration", 0, []sttChunkWindow{{0, 0}}},
		{"negative duration", -5, []sttChunkWindow{{0, 0}}},
		{"under one chunk", 10, []sttChunkWindow{{0, 10}}},
		{"exactly one chunk", 30, []sttChunkWindow{{0, 30}}},
		{"just over one chunk", 31, []sttChunkWindow{{0, 30}, {27, 4}}},
		{"two chunks plus tail", 60, []sttChunkWindow{{0, 30}, {27, 30}, {54, 6}}},
		{"three chunks plus tail", 90, []sttChunkWindow{{0, 30}, {27, 30}, {54, 30}, {81, 9}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sttChunkWindows(tt.duration)
			if len(got) != len(tt.want) {
				t.Fatalf("sttChunkWindows(%v) = %v, want %v", tt.duration, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("sttChunkWindows(%v)[%d] = %+v, want %+v", tt.duration, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMergeTranscriptions(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{
			name:  "overlap dedupe",
			parts: []string{"one two three four five six seven eight nine", "four five six seven eight nine ten"},
			want:  "one two three four five six seven eight nine ten",
		},
		{
			name:  "case and punctuation insensitive",
			parts: []string{"Hello, world. Test A B C D E", "test a b c d e End."},
			want:  "Hello, world. Test A B C D E End.",
		},
		{
			name:  "no overlap",
			parts: []string{"alpha beta", "gamma delta"},
			want:  "alpha beta gamma delta",
		},
		{
			name:  "overlap below threshold",
			parts: []string{"the the the the the one", "the two"},
			want:  "the the the the the one the two",
		},
		{
			name:  "single part unchanged",
			parts: []string{"only one part"},
			want:  "only one part",
		},
		{
			name:  "empty parts",
			parts: []string{},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeTranscriptions(tt.parts); got != tt.want {
				t.Errorf("mergeTranscriptions(%q) = %q, want %q", tt.parts, got, tt.want)
			}
		})
	}
}

// writeLE16/writeLE32 write little-endian values into a RIFF header buffer.
func writeLE16(w *bytes.Buffer, v uint16) {
	w.WriteByte(byte(v))
	w.WriteByte(byte(v >> 8))
}

func writeLE32(w *bytes.Buffer, v uint32) {
	w.WriteByte(byte(v))
	w.WriteByte(byte(v >> 8))
	w.WriteByte(byte(v >> 16))
	w.WriteByte(byte(v >> 24))
}

// writeRIFFHeader writes a standard 44-byte PCM WAV header for dataSize bytes
// of mono 16-bit audio at sampleRate Hz.
func writeRIFFHeader(w *bytes.Buffer, sampleRate, dataSize int) {
	w.WriteString("RIFF")
	writeLE32(w, uint32(36+dataSize))
	w.WriteString("WAVE")
	w.WriteString("fmt ")
	writeLE32(w, 16)
	writeLE16(w, 1) // PCM
	writeLE16(w, 1) // mono
	writeLE32(w, uint32(sampleRate))
	writeLE32(w, uint32(sampleRate*2)) // byte rate
	writeLE16(w, 2)                    // block align
	writeLE16(w, 16)                   // bits per sample
	w.WriteString("data")
	writeLE32(w, uint32(dataSize))
}

// wavData walks the RIFF chunk list and returns the payload of the data chunk.
// The data chunk is NOT at a fixed offset: ffmpeg inserts a LIST/INFO chunk
// before it (e.g. "ISFT Lavf60..."), so the header length varies by version.
func wavData(t *testing.T, raw []byte) []byte {
	t.Helper()
	off := 12 // skip "RIFF" + size + "WAVE"
	for off+8 <= len(raw) {
		id := string(raw[off : off+4])
		size := int(raw[off+4]) | int(raw[off+5])<<8 | int(raw[off+6])<<16 | int(raw[off+7])<<24
		if id == "data" {
			return raw[off+8 : off+8+size]
		}
		off += 8 + size + (size & 1)
	}
	t.Fatalf("wavData: no data chunk in %d bytes", len(raw))
	return nil
}

// TestRunSTTForFileChunksAudio drives the full chunked path: a 57s synthetic
// WAV must produce exactly 3 chunk STT calls (30s + 30s + 3s) with the
// expected chunk filenames, per-window PCM lengths, a byte-identical 3s
// overlap between chunk 1's tail and chunk 2's head, and a merged result.
func TestRunSTTForFileChunksAudio(t *testing.T) {
	if !sttChunkAvailable() {
		t.Skip("ffmpeg not available")
	}

	svc, _ := newTestService(t)
	ctx := context.Background()

	// Synthetic 57s mono 16kHz 16-bit WAV: 44-byte RIFF header + 57*16000
	// samples of a 440 Hz sine.
	const sampleRate = 16000
	const durationSec = 57
	samples := make([]int16, sampleRate*durationSec)
	for i := range samples {
		samples[i] = int16(30000 * math.Sin(2*math.Pi*440*float64(i)/sampleRate))
	}
	var wav bytes.Buffer
	writeRIFFHeader(&wav, sampleRate, len(samples)*2)
	for _, s := range samples {
		wav.WriteByte(byte(s))
		wav.WriteByte(byte(s >> 8))
	}

	key, err := GenerateFileKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	nonce, err := GenerateBaseNonce()
	if err != nil {
		t.Fatalf("generate nonce: %v", err)
	}

	cipherFile, err := os.CreateTemp(t.TempDir(), "chunk-test-*.wav")
	if err != nil {
		t.Fatalf("create cipher file: %v", err)
	}
	cipherSHA, _, cipherSize, err := EncryptToFile(bytes.NewReader(wav.Bytes()), cipherFile, key, nonce)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cipherData, err := os.ReadFile(cipherFile.Name())
	if err != nil {
		t.Fatalf("read cipher file: %v", err)
	}
	cipherFile.Close()

	res, err := svc.DB.Exec(
		`INSERT INTO files (storage_key, filename, mime_type, size_bytes, ciphertext_sha256, aes_key, aes_nonce)
		 VALUES (?, 'chunk-test.wav', 'audio/wav', ?, ?, ?, ?)`,
		"stt-chunk-test-key", cipherSize, cipherSHA, key, nonce,
	)
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	if err := svc.Cache.Put(fileID, cipherSHA, bytes.NewReader(cipherData)); err != nil {
		t.Fatalf("cache put: %v", err)
	}

	rec := &recordingSTTer{}
	sttRes, err := svc.RunSTTForFile(ctx, fileID, rec)
	if err != nil {
		t.Fatalf("RunSTTForFile: %v", err)
	}
	if sttRes == nil || sttRes.STTText != "part00 part01 part02" {
		t.Fatalf("unexpected merged result: %+v", sttRes)
	}

	rec.mu.Lock()
	calls := append([]sttCall(nil), rec.calls...)
	rec.mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("expected 3 STT calls, got %d", len(calls))
	}

	wantNames := []string{"chunk_000.wav", "chunk_001.wav", "chunk_002.wav"}
	windows := sttChunkWindows(durationSec)
	var pcm [][]byte
	for i, call := range calls {
		if call.filename != wantNames[i] {
			t.Errorf("call %d: filename = %q, want %q", i, call.filename, wantNames[i])
		}
		if len(call.data) < 44 {
			t.Errorf("call %d: chunk shorter than a WAV header (%d bytes)", i, len(call.data))
			continue
		}
		d := wavData(t, call.data)
		pcm = append(pcm, d)
		wantLen := int(windows[i].Length * 32000) // seconds * 16000 Hz * 2 bytes
		if diff := len(d) - wantLen; diff > 2 || diff < -2 {
			t.Errorf("call %d: pcm length = %d, want %d (±2)", i, len(d), wantLen)
		}
	}

	// The 3s overlap: chunk 2's first 96000 PCM bytes (3s * 16000 * 2) must
	// equal chunk 1's last 96000 bytes.
	if len(pcm) != 3 || len(pcm[1]) < 96000 || len(pcm[0]) < 96000 {
		t.Fatal("chunks missing or too short for overlap comparison")
	}
	if !bytes.Equal(pcm[0][len(pcm[0])-96000:], pcm[1][:96000]) {
		t.Error("chunk 1 tail and chunk 2 head (3s overlap) differ")
	}
}
