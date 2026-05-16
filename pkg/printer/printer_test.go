package printer

import (
	"os"
	"testing"
)

func TestBufInit(t *testing.T) {
	b := new(Buf)
	b.Init()
	// ESC @ = initialize
	if len(b.Bytes()) != 2 || b.Bytes()[0] != ESC || b.Bytes()[1] != '@' {
		t.Errorf("Init did not produce ESC @, got %v", b.Bytes())
	}
}

func TestBufAlign(t *testing.T) {
	b := new(Buf)
	b.AlignLeft()
	if len(b.Bytes()) != 3 || b.Bytes()[0] != ESC || b.Bytes()[1] != 'a' || b.Bytes()[2] != 0 {
		t.Errorf("AlignLeft failed, got %v", b.Bytes())
	}
	b.Reset()
	b.AlignCenter()
	b.Reset()
	b.AlignRight()
}

func TestBufBold(t *testing.T) {
	b := new(Buf)
	b.Bold(true)
	if len(b.Bytes()) != 3 || b.Bytes()[2] != 1 {
		t.Errorf("Bold(true) failed")
	}
	b.Reset()
	b.Bold(false)
}

func TestBufDoubleSize(t *testing.T) {
	b := new(Buf)
	b.DoubleSize()
	// ESC ! 0x38 (0x08 | 0x10 | 0x20)
	if len(b.Bytes()) != 3 || b.Bytes()[2] != 0x38 {
		t.Errorf("DoubleSize failed, got %v", b.Bytes())
	}
}

func TestBufText(t *testing.T) {
	b := new(Buf)
	b.Text("hello")
	s := string(b.Bytes())
	if s != "hello" {
		t.Errorf("Text: expected 'hello', got %q", s)
	}
}

func TestBufTextf(t *testing.T) {
	b := new(Buf)
	b.Textf("%s %d", "item", 3)
	s := string(b.Bytes())
	if s != "item 3" {
		t.Errorf("Textf: expected 'item 3', got %q", s)
	}
}

func TestBufLn(t *testing.T) {
	b := new(Buf)
	b.Text("a").Ln().Text("b")
	s := string(b.Bytes())
	if s != "a\nb" {
		t.Errorf("Ln: expected 'a\\nb', got %q", s)
	}
}

func TestBufHLine(t *testing.T) {
	b := new(Buf)
	b.HLine(5)
	out := string(b.Bytes())
	if len(out) != 6 { // 5 dashes + newline
		t.Errorf("HLine(5): expected 6 bytes, got %d: %q", len(out), out)
	}
}

func TestBufCut(t *testing.T) {
	b := new(Buf)
	b.FullCut()
	if len(b.Bytes()) != 3 || b.Bytes()[0] != GS || b.Bytes()[1] != 'V' || b.Bytes()[2] != 0 {
		t.Errorf("FullCut failed, got %v", b.Bytes())
	}
	b.Reset()
	b.PartialCut()
	if b.Bytes()[2] != 1 {
		t.Errorf("PartialCut failed")
	}
}

func TestBufFeed(t *testing.T) {
	b := new(Buf)
	b.Feed(5)
	if len(b.Bytes()) != 3 || b.Bytes()[0] != ESC || b.Bytes()[1] != 'd' || b.Bytes()[2] != 5 {
		t.Errorf("Feed(5) failed, got %v", b.Bytes())
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		s      string
		w      int
		expect string
	}{
		{"hello", 10, "hello     "},
		{"hello", 5, "hello"},
		{"hello", 3, "hello"},
	}
	for _, tt := range tests {
		got := PadRight(tt.s, tt.w)
		if got != tt.expect {
			t.Errorf("PadRight(%q, %d) = %q, want %q", tt.s, tt.w, got, tt.expect)
		}
	}
}

func TestPadCenter(t *testing.T) {
	tests := []struct {
		s      string
		w      int
		expect string
	}{
		{"A", 5, "  A  "},
		{"AB", 5, " AB  "},
		{"ABC", 5, " ABC "},
		{"ABCDE", 5, "ABCDE"},
		{"ABCDEF", 5, "ABCDEF"},
	}
	for _, tt := range tests {
		got := PadCenter(tt.s, tt.w)
		if got != tt.expect {
			t.Errorf("PadCenter(%q, %d) = %q, want %q", tt.s, tt.w, got, tt.expect)
		}
	}
}

func TestBufReset(t *testing.T) {
	b := new(Buf)
	b.Text("hello")
	if len(b.Bytes()) == 0 {
		t.Error("buffer should not be empty after Text")
	}
	b.Reset()
	if len(b.Bytes()) != 0 {
		t.Error("buffer should be empty after Reset")
	}
}

func TestFindUSBLP_NoDevice(t *testing.T) {
	// This should fail gracefully on a system without a USB printer.
	_, err := FindUSBLP()
	if err == nil {
		t.Log("USB printer found (unexpected in CI, but OK)")
	} else {
		t.Logf("FindUSBLP returned expected error: %v", err)
	}
}

func TestFindUSBByID(t *testing.T) {
	if os.Getenv("PRINT_TEST") != "1" {
		t.Skip("skipping USB discovery test (set PRINT_TEST=1 to run)")
	}
	// Try the Epson TM-T88III (from the Python demo).
	// If the device is plugged in, this should find it.
	// If not, it should return a clean error (not panic).
	pr, err := FindUSBByID(0x08A6, 0x003D)
	if err != nil {
		t.Logf("FindUSBByID(08a6:003d) returned error (printer not connected?): %v", err)
		return
	}
	t.Logf("Found printer via raw USB: %T", pr)
	// Don't actually write — just close it to release the interface.
	if err := pr.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestFindPrinter(t *testing.T) {
	if os.Getenv("PRINT_TEST") != "1" {
		t.Skip("skipping printer discovery test (set PRINT_TEST=1 to run)")
	}
	pr, err := FindPrinter()
	if err != nil {
		t.Logf("FindPrinter returned error: %v", err)
		return
	}
	t.Logf("FindPrinter found: %T", pr)
	if err := pr.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

// TestSmokePrint replicates the Python print.py demo using our Go printer
// package.  This test is SKIPPED by default to avoid printing on every
// test run.  To enable it, set the env var PRINT_TEST=1:
//
//	PRINT_TEST=1 go test ./pkg/printer/ -run TestSmokePrint -v
func TestSmokePrint(t *testing.T) {
	if os.Getenv("PRINT_TEST") != "1" {
		t.Skip("skipping real printer test (set PRINT_TEST=1 to run)")
	}

	pr, err := FindPrinter()
	if err != nil {
		t.Fatalf("no printer found: %v", err)
	}

	b := new(Buf)
	b.Init()
	b.AlignLeft()
	b.DoubleSize() // bold + double height + double width
	b.Text("MentisEterna — Print Test")
	b.Ln()
	b.Text("Umlauts: \xc3\x84/\xc3\xa4 \xc3\x96/\xc3\xb6 \xc3\x9c/\xc3\xbc")
	b.Ln()
	b.NormalSize()

	// Feed 6 lines to clear cutting blade (matching Python demo).
	b.Feed(6)

	// Trigger partial cut.
	b.PartialCut()

	// Send.
	n, err := pr.Write(b.Bytes())
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("Wrote %d bytes successfully", n)

	if err := pr.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}
