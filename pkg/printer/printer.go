// Package printer provides ESC/POS command helpers and USB printer
// communication for thermal receipt printers (e.g., Epson TM-T88 series).
//
// Note types can use these helpers to format and print their data via the
// ActionHandler interface.
package printer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

// ---------------------------------------------------------------------------
// ESC/POS command constants
// ---------------------------------------------------------------------------

const (
	ESC = 0x1B
	GS  = 0x1D
)

// ---------------------------------------------------------------------------
// Command builder
// ---------------------------------------------------------------------------

// Buf is a byte buffer that accumulates ESC/POS commands and text.
// Zero value is ready to use.
type Buf struct {
	b bytes.Buffer
}

// Init resets the printer to its default state.
func (p *Buf) Init() *Buf {
	p.b.Write([]byte{ESC, '@'})
	return p
}

// Align sets text justification. 0 = left, 1 = center, 2 = right.
func (p *Buf) Align(n byte) *Buf {
	p.b.Write([]byte{ESC, 'a', n})
	return p
}

// AlignLeft is shorthand for Align(0).
func (p *Buf) AlignLeft() *Buf { return p.Align(0) }

// AlignCenter is shorthand for Align(1).
func (p *Buf) AlignCenter() *Buf { return p.Align(1) }

// AlignRight is shorthand for Align(2).
func (p *Buf) AlignRight() *Buf { return p.Align(2) }

// Style sets character formatting via the ESC ! command.
// Bit flags: 0x08 = bold, 0x10 = double height, 0x20 = double width.
// Provide 0 to reset all styles.
func (p *Buf) Style(n byte) *Buf {
	p.b.Write([]byte{ESC, '!', n})
	return p
}

// Bold sets or clears bold text.
func (p *Buf) Bold(on bool) *Buf {
	if on {
		p.b.Write([]byte{ESC, 'E', 1})
	} else {
		p.b.Write([]byte{ESC, 'E', 0})
	}
	return p
}

// Underline sets or clears underline. n: 0=off, 1=1-dot, 2=2-dot.
func (p *Buf) Underline(n byte) *Buf {
	p.b.Write([]byte{ESC, '-', n})
	return p
}

// BigSize enables bold + double height (no double width).
// Characters are taller but not wider — roughly 1.5× the normal
// size while keeping the full ~42 chars on an 80 mm receipt.
func (p *Buf) BigSize() *Buf {
	return p.Style(0x08 | 0x10)
}

// DoubleSize enables bold + double height + double width.
func (p *Buf) DoubleSize() *Buf {
	return p.Style(0x08 | 0x10 | 0x20)
}

// NormalSize resets all character styles.
func (p *Buf) NormalSize() *Buf {
	return p.Style(0x00)
}

// HLine draws a horizontal line of dashes across the receipt.
// Receipts are typically 32 or 42 chars wide at normal size.
func (p *Buf) HLine(width int) *Buf {
	line := make([]byte, width)
	for i := range line {
		line[i] = '-'
	}
	p.b.Write(line)
	p.Ln()
	return p
}

// DoubleHLine draws a horizontal double-width line (roughly half width).
func (p *Buf) DoubleHLine(width int) *Buf {
	line := make([]byte, width)
	for i := range line {
		line[i] = '='
	}
	p.b.Write(line)
	p.Ln()
	return p
}

// Text writes plain text.
func (p *Buf) Text(s string) *Buf {
	p.b.WriteString(s)
	return p
}

// Textf writes formatted text (like fmt.Sprintf).
func (p *Buf) Textf(format string, args ...any) *Buf {
	p.b.WriteString(fmt.Sprintf(format, args...))
	return p
}

// Ln writes a line feed.
func (p *Buf) Ln() *Buf {
	p.b.WriteByte('\n')
	return p
}

// Feed feeds n blank lines.
func (p *Buf) Feed(n byte) *Buf {
	p.b.Write([]byte{ESC, 'd', n})
	return p
}

// Cut triggers the auto-cutter. mode 0 = full cut, 1 = partial cut.
func (p *Buf) Cut(mode byte) *Buf {
	p.b.Write([]byte{GS, 'V', mode})
	return p
}

// PartialCut triggers a partial cut (leaves a small tab).
func (p *Buf) PartialCut() *Buf { return p.Cut(1) }

// FullCut triggers a full cut.
func (p *Buf) FullCut() *Buf { return p.Cut(0) }

// Bytes returns the accumulated bytes.
func (p *Buf) Bytes() []byte {
	return p.b.Bytes()
}

// Reset clears the buffer.
func (p *Buf) Reset() {
	p.b.Reset()
}

// PadRight returns s padded with spaces to the given total width.
func PadRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	b := make([]byte, width)
	copy(b, s)
	for i := len(s); i < width; i++ {
		b[i] = ' '
	}
	return string(b)
}

// PadCenter pads s with spaces on both sides to center it within width.
func PadCenter(s string, width int) string {
	if len(s) >= width {
		return s
	}
	totalPad := width - len(s)
	left := totalPad / 2
	b := make([]byte, width)
	for i := 0; i < left; i++ {
		b[i] = ' '
	}
	copy(b[left:], s)
	for i := left + len(s); i < width; i++ {
		b[i] = ' '
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Printer interface
// ---------------------------------------------------------------------------

// Printer sends bytes to a physical printer.
type Printer interface {
	Write(data []byte) (int, error)
	Close() error
}

// FilePrinter wraps an *os.File as a Printer (e.g., /dev/usb/lp0).
type FilePrinter struct{ f *os.File }

// NewFilePrinter opens a printer device file.
func NewFilePrinter(devicePath string) (*FilePrinter, error) {
	f, err := os.OpenFile(devicePath, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("printer: open %s: %w", devicePath, err)
	}
	return &FilePrinter{f: f}, nil
}

func (p *FilePrinter) Write(data []byte) (int, error) { return p.f.Write(data) }
func (p *FilePrinter) Close() error                   { return p.f.Close() }

// USBPrinter writes to a USB device using raw USB bulk transfers.
// This uses the Linux usbfs /dev/bus/usb/... directly via a helper binary,
// or you can use /dev/usb/lp* on systems where the usblp kernel module
// is loaded.  For direct USB control (vendor/product matching), prefer
// the FilePrinter with the appropriate device node.
//
// vendorID and productID are the USB vendor/product IDs (hex, e.g. 0x08A6, 0x003D).
// On Linux, if the usblp driver is loaded, the printer appears as /dev/usb/lp0.
// This constructor opens /dev/usb/lp0 by default; provide the path via NewFilePrinter
// if your device node differs.

// FindUSBLP attempts to locate a USB printer device and return a Printer.
// It tries common device paths in order.
func FindUSBLP() (Printer, error) {
	candidates := []string{
		"/dev/usb/lp0",
		"/dev/usb/lp1",
		"/dev/usb/lp2",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return NewFilePrinter(path)
		}
	}
	return nil, fmt.Errorf("printer: no USB printer found at %v", candidates)
}

// ---------------------------------------------------------------------------
// Helper: write buf to printer
// ---------------------------------------------------------------------------

// Send writes the contents of buf to the given printer.
func Send(pr Printer, buf *Buf) error {
	_, err := pr.Write(buf.Bytes())
	return err
}

// SendAndCut writes buf to the printer, appends a partial cut + line feeds,
// and closes the printer.
func SendAndCut(pr Printer, buf *Buf) error {
	buf.Feed(4)
	buf.PartialCut()
	_, err := pr.Write(buf.Bytes())
	if closeErr := pr.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

// PrintU16LE appends a 16-bit little-endian integer (used by some ESC/POS commands).
func PrintU16LE(n uint16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, n)
	return b
}
