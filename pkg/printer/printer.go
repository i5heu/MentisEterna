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
	"log"
	"os"
	"strings"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// ESC/POS command constants
// ---------------------------------------------------------------------------

const (
	ESC = 0x1B
	GS  = 0x1D

	escposCodeTablePC437   = 0
	escposCodeTableWPC1252 = 16
)

type escposCodePage struct {
	name   string
	table  byte
	encode func(string) []byte
}

var cp437Specials = map[rune]byte{
	'Ç': 0x80,
	'ü': 0x81,
	'é': 0x82,
	'â': 0x83,
	'ä': 0x84,
	'à': 0x85,
	'å': 0x86,
	'ç': 0x87,
	'ê': 0x88,
	'ë': 0x89,
	'è': 0x8A,
	'ï': 0x8B,
	'î': 0x8C,
	'ì': 0x8D,
	'Ä': 0x8E,
	'Å': 0x8F,
	'É': 0x90,
	'æ': 0x91,
	'Æ': 0x92,
	'ô': 0x93,
	'ö': 0x94,
	'ò': 0x95,
	'û': 0x96,
	'ù': 0x97,
	'ÿ': 0x98,
	'Ö': 0x99,
	'Ü': 0x9A,
	'¢': 0x9B,
	'£': 0x9C,
	'¥': 0x9D,
	'₧': 0x9E,
	'ƒ': 0x9F,
	'á': 0xA0,
	'í': 0xA1,
	'ó': 0xA2,
	'ú': 0xA3,
	'ñ': 0xA4,
	'Ñ': 0xA5,
	'ª': 0xA6,
	'º': 0xA7,
	'¿': 0xA8,
	'⌐': 0xA9,
	'¬': 0xAA,
	'½': 0xAB,
	'¼': 0xAC,
	'¡': 0xAD,
	'«': 0xAE,
	'»': 0xAF,
	'░': 0xB0,
	'▒': 0xB1,
	'▓': 0xB2,
	'│': 0xB3,
	'┤': 0xB4,
	'╡': 0xB5,
	'╢': 0xB6,
	'╖': 0xB7,
	'╕': 0xB8,
	'╣': 0xB9,
	'║': 0xBA,
	'╗': 0xBB,
	'╝': 0xBC,
	'╜': 0xBD,
	'╛': 0xBE,
	'┐': 0xBF,
	'└': 0xC0,
	'┴': 0xC1,
	'┬': 0xC2,
	'├': 0xC3,
	'─': 0xC4,
	'┼': 0xC5,
	'╞': 0xC6,
	'╟': 0xC7,
	'╚': 0xC8,
	'╔': 0xC9,
	'╩': 0xCA,
	'╦': 0xCB,
	'╠': 0xCC,
	'═': 0xCD,
	'╬': 0xCE,
	'╧': 0xCF,
	'╨': 0xD0,
	'╤': 0xD1,
	'╥': 0xD2,
	'╙': 0xD3,
	'╘': 0xD4,
	'╒': 0xD5,
	'╓': 0xD6,
	'╫': 0xD7,
	'╪': 0xD8,
	'┘': 0xD9,
	'┌': 0xDA,
	'█': 0xDB,
	'▄': 0xDC,
	'▌': 0xDD,
	'▐': 0xDE,
	'▀': 0xDF,
	'α': 0xE0,
	'ß': 0xE1,
	'Γ': 0xE2,
	'π': 0xE3,
	'Σ': 0xE4,
	'σ': 0xE5,
	'µ': 0xE6,
	'τ': 0xE7,
	'Φ': 0xE8,
	'Θ': 0xE9,
	'Ω': 0xEA,
	'δ': 0xEB,
	'∞': 0xEC,
	'φ': 0xED,
	'ε': 0xEE,
	'∩': 0xEF,
	'≡': 0xF0,
	'±': 0xF1,
	'≥': 0xF2,
	'≤': 0xF3,
	'⌠': 0xF4,
	'⌡': 0xF5,
	'÷': 0xF6,
	'≈': 0xF7,
	'°': 0xF8,
	'∙': 0xF9,
	'·': 0xFA,
	'√': 0xFB,
	'ⁿ': 0xFC,
	'²': 0xFD,
	'■': 0xFE,
}

var windows1252Specials = map[rune]byte{
	'€': 0x80,
	'‚': 0x82,
	'ƒ': 0x83,
	'„': 0x84,
	'…': 0x85,
	'†': 0x86,
	'‡': 0x87,
	'ˆ': 0x88,
	'‰': 0x89,
	'Š': 0x8A,
	'‹': 0x8B,
	'Œ': 0x8C,
	'Ž': 0x8E,
	'‘': 0x91,
	'’': 0x92,
	'“': 0x93,
	'”': 0x94,
	'•': 0x95,
	'–': 0x96,
	'—': 0x97,
	'˜': 0x98,
	'™': 0x99,
	'š': 0x9A,
	'›': 0x9B,
	'œ': 0x9C,
	'ž': 0x9E,
	'Ÿ': 0x9F,
}

func configuredCodePage() escposCodePage {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("THERMAL_PRINTER_CODEPAGE"))) {
	case "", "auto":
		usbID := strings.ToLower(strings.TrimSpace(os.Getenv("THERMAL_PRINTER_USB_ID")))
		if usbID == "08a6:003d" {
			return escposCodePage{name: "pc437", table: escposCodeTablePC437, encode: encodePC437}
		}
		return escposCodePage{name: "pc437", table: escposCodeTablePC437, encode: encodePC437}
	case "0", "pc437", "cp437", "tm-t88iii":
		return escposCodePage{name: "pc437", table: escposCodeTablePC437, encode: encodePC437}
	case "16", "wpc1252", "cp1252", "windows1252", "windows-1252":
		return escposCodePage{name: "wpc1252", table: escposCodeTableWPC1252, encode: encodeWindows1252}
	default:
		log.Printf("printer: unknown THERMAL_PRINTER_CODEPAGE=%q, falling back to pc437", os.Getenv("THERMAL_PRINTER_CODEPAGE"))
		return escposCodePage{name: "pc437", table: escposCodeTablePC437, encode: encodePC437}
	}
}

func ConfiguredCodePageName() string {
	return configuredCodePage().name
}

func normalizeCommonRune(r rune) ([]byte, bool) {
	switch r {
	case 0:
		return nil, true
	case '\t':
		return []byte{' ', ' ', ' ', ' '}, true
	case '\n', '\r':
		return []byte{byte(r)}, true
	case '\u00A0':
		return []byte{' '}, true
	case '\u2212':
		return []byte{'-'}, true
	default:
		return nil, false
	}
}

func encodePC437(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if normalized, ok := normalizeCommonRune(r); ok {
			out = append(out, normalized...)
			continue
		}
		switch {
		case r == '\u2010', r == '\u2011', r == '\u2012', r == '\u2013', r == '\u2014', r == '\u2015':
			out = append(out, '-')
		case r == '\u2018', r == '\u2019', r == '\u2032':
			out = append(out, '\'')
		case r == '\u201C', r == '\u201D', r == '\u2033':
			out = append(out, '"')
		case r == '\u2022':
			out = append(out, '*')
		case r == '\u2026':
			out = append(out, '.')
		case r >= 0x20 && r <= 0x7E:
			out = append(out, byte(r))
		default:
			if b, ok := cp437Specials[r]; ok {
				out = append(out, b)
			} else {
				out = append(out, '?')
			}
		}
	}
	return out
}

func encodeWindows1252(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if normalized, ok := normalizeCommonRune(r); ok {
			out = append(out, normalized...)
			continue
		}
		switch {
		case r >= 0x20 && r <= 0x7E:
			out = append(out, byte(r))
		case r >= 0xA0 && r <= 0xFF:
			out = append(out, byte(r))
		default:
			if b, ok := windows1252Specials[r]; ok {
				out = append(out, b)
			} else {
				out = append(out, '?')
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Command builder
// ---------------------------------------------------------------------------

// Buf is a byte buffer that accumulates ESC/POS commands and text.
// Zero value is ready to use.
type Buf struct {
	b            bytes.Buffer
	codeTableSet bool
	codePage     escposCodePage
}

// Init resets the printer to its default state.
func (p *Buf) Init() *Buf {
	p.codePage = configuredCodePage()
	p.b.Write([]byte{ESC, '@'})
	p.b.Write([]byte{ESC, 't', p.codePage.table})
	p.codeTableSet = true
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

// LineSpacing sets the line spacing in vertical dots (1 dot ≈ 0.125 mm).
// Default is ~30 dots.  Smaller values tighten line spacing; larger values loosen it.
// Use SpacerLine with LineSpacing to create a thin, tight guide line.
func (p *Buf) LineSpacing(dots byte) *Buf {
	p.b.Write([]byte{ESC, '3', dots})
	return p
}

// ResetLineSpacing resets line spacing to the printer's default.
func (p *Buf) ResetLineSpacing() *Buf {
	p.b.Write([]byte{ESC, '2'})
	return p
}

// SpacerLine draws a compact horizontal divider line — reduces spacing
// before and after so the line hugs the surrounding rows.  The line itself
// is thin dashes (shorter and tighter).
// Call this instead of HLine when you want a tight guide line.
func (p *Buf) SpacerLine(width int) *Buf {
	p.LineSpacing(10) // tighter spacing
	line := make([]byte, width)
	for i := range line {
		line[i] = '-'
	}
	p.b.Write(line)
	p.Ln()
	p.ResetLineSpacing()
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
	if p.codePage.encode == nil {
		p.codePage = configuredCodePage()
	}
	if !p.codeTableSet {
		// When callers skip Init(), still encode text consistently.
		p.codeTableSet = true
	}
	p.b.Write(p.codePage.encode(s))
	return p
}

// Textf writes formatted text (like fmt.Sprintf).
func (p *Buf) Textf(format string, args ...any) *Buf {
	return p.Text(fmt.Sprintf(format, args...))
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
	p.Ln()
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
	p.codeTableSet = false
	p.codePage = escposCodePage{}
}

// TextWidth returns the printable width of s in runes.
func TextWidth(s string) int {
	return utf8.RuneCountInString(s)
}

// TruncateWidth truncates s to at most width runes.
func TruncateWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if TextWidth(s) <= width {
		return s
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width])
}

// TruncateWithEllipsis truncates s and appends an ellipsis when needed.
func TruncateWithEllipsis(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if TextWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	return TruncateWidth(s, width-1) + "…"
}

// PadRight returns s padded with spaces to the given total width.
func PadRight(s string, width int) string {
	sw := TextWidth(s)
	if sw >= width {
		return s
	}
	return s + strings.Repeat(" ", width-sw)
}

// PadCenter pads s with spaces on both sides to center it within width.
func PadCenter(s string, width int) string {
	sw := TextWidth(s)
	if sw >= width {
		return s
	}
	totalPad := width - sw
	left := totalPad / 2
	right := totalPad - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
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
		log.Printf("printer: open device %s failed: %v", devicePath, err)
		return nil, fmt.Errorf("printer: open %s: %w", devicePath, err)
	}
	log.Printf("printer: opened device %s", devicePath)
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
			log.Printf("printer: found device node %s", path)
			return NewFilePrinter(path)
		}
	}
	log.Printf("printer: no usblp device nodes found at %v", candidates)
	return nil, fmt.Errorf("printer: no USB printer found at %v", candidates)
}

// ---------------------------------------------------------------------------
// Helper: write buf to printer
// ---------------------------------------------------------------------------

// Send writes the contents of buf to the given printer.
func Send(pr Printer, buf *Buf) error {
	data := buf.Bytes()
	log.Printf("printer: sending %d bytes to printer", len(data))
	n, err := pr.Write(data)
	if err != nil {
		log.Printf("printer: write failed after %d bytes: %v", n, err)
		return err
	}
	log.Printf("printer: write succeeded (%d bytes)", n)
	return nil
}

// SendAndCut writes buf to the printer, appends a partial cut + line feeds,
// and closes the printer.
func SendAndCut(pr Printer, buf *Buf) error {
	buf.Feed(4)
	buf.PartialCut()
	data := buf.Bytes()
	log.Printf("printer: sending %d bytes to printer (with feed + cut)", len(data))
	n, err := pr.Write(data)
	if err != nil {
		log.Printf("printer: write failed after %d bytes: %v", n, err)
	}
	if closeErr := pr.Close(); closeErr != nil {
		log.Printf("printer: close failed: %v", closeErr)
		if err == nil {
			err = closeErr
		}
	}
	if err == nil {
		log.Printf("printer: send+cut succeeded (%d bytes)", n)
	}
	return err
}

// PrintU16LE appends a 16-bit little-endian integer (used by some ESC/POS commands).
func PrintU16LE(n uint16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, n)
	return b
}
