package printer

import (
	"fmt"
	"image"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	DefaultImageMaxWidth        = 512
	defaultImageLineSpacingDots = 16
	// Slightly darker defaults now that image writes are throttled more gently.
	defaultImageThreshold     = 120.0
	defaultImageDarknessScale = 0.96
)

func configuredImageThreshold() float64 {
	raw := strings.TrimSpace(os.Getenv("THERMAL_PRINTER_IMAGE_THRESHOLD"))
	if raw == "" {
		return defaultImageThreshold
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 255 {
		log.Printf("printer: invalid THERMAL_PRINTER_IMAGE_THRESHOLD=%q, using default %.2f", raw, defaultImageThreshold)
		return defaultImageThreshold
	}
	return v
}

func configuredImageDarknessScale() float64 {
	raw := strings.TrimSpace(os.Getenv("THERMAL_PRINTER_IMAGE_DARKNESS_SCALE"))
	if raw == "" {
		return defaultImageDarknessScale
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		log.Printf("printer: invalid THERMAL_PRINTER_IMAGE_DARKNESS_SCALE=%q, using default %.2f", raw, defaultImageDarknessScale)
		return defaultImageDarknessScale
	}
	return v
}

type monoRaster struct {
	width  int
	height int
	black  []bool
}

// ImageBitColumn renders img using the legacy ESC/POS 24-dot column mode.
// This matches the Python escpos "bitImageColumn" mode that is known to
// work reliably with TM-T88III-compatible printers.
func (p *Buf) ImageBitColumn(img image.Image) error {
	return p.ImageBitColumnWidth(img, DefaultImageMaxWidth)
}

// ImageBitColumnWidth renders img using the legacy ESC/POS 24-dot column
// mode after scaling it down to maxWidth pixels if necessary.
func (p *Buf) ImageBitColumnWidth(img image.Image, maxWidth int) error {
	raster, err := rasterizeMono(img, maxWidth)
	if err != nil {
		return err
	}
	if raster.width == 0 || raster.height == 0 {
		return nil
	}

	// Match the legacy python-escpos bitImageColumn path more closely.
	// A slightly tighter line spacing is known to work better on TM-T88III-class
	// printers, and we keep image width at 512 while reducing thermal load via
	// rasterization rather than shrinking the image.
	p.LineSpacing(defaultImageLineSpacingDots)
	defer p.ResetLineSpacing()

	for y0 := 0; y0 < raster.height; y0 += 24 {
		p.b.Write([]byte{ESC, '*', 33, byte(raster.width & 0xff), byte((raster.width >> 8) & 0xff)})
		for x := 0; x < raster.width; x++ {
			var col [3]byte
			for bit := 0; bit < 24; bit++ {
				y := y0 + bit
				if y >= raster.height {
					continue
				}
				if raster.black[y*raster.width+x] {
					col[bit/8] |= 1 << uint(7-(bit%8))
				}
			}
			p.b.Write(col[:])
		}
		p.Ln()
	}

	return nil
}

func rasterizeMono(img image.Image, maxWidth int) (monoRaster, error) {
	if img == nil {
		return monoRaster{}, fmt.Errorf("printer: image is nil")
	}

	bounds := img.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return monoRaster{}, fmt.Errorf("printer: invalid image size %dx%d", srcW, srcH)
	}

	if maxWidth <= 0 {
		maxWidth = DefaultImageMaxWidth
	}

	dstW := srcW
	if dstW > maxWidth {
		dstW = maxWidth
	}
	dstH := int(math.Round(float64(dstW) * float64(srcH) / float64(srcW)))
	if dstH < 1 {
		dstH = 1
	}

	threshold := configuredImageThreshold()
	darknessScale := configuredImageDarknessScale()

	gray := make([]float64, dstW*dstH)
	for y := 0; y < dstH; y++ {
		srcY := bounds.Min.Y + (y * srcH / dstH)
		for x := 0; x < dstW; x++ {
			srcX := bounds.Min.X + (x * srcW / dstW)
			r, g, b, a := img.At(srcX, srcY).RGBA()
			alpha := float64(a) / 65535.0
			rf := (1-alpha)*255.0 + alpha*float64(r)/257.0
			gf := (1-alpha)*255.0 + alpha*float64(g)/257.0
			bf := (1-alpha)*255.0 + alpha*float64(b)/257.0
			grayVal := 0.299*rf + 0.587*gf + 0.114*bf
			// Slightly lift dark tones before dithering so large photos do not
			// drive the thermal head as hard. Keep width unchanged. This is
			// configurable so you can trade darker blacks against thermal load.
			gray[y*dstW+x] = 255.0 - (255.0-grayVal)*darknessScale
		}
	}

	black := make([]bool, len(gray))
	work := append([]float64(nil), gray...)
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			idx := y*dstW + x
			old := work[idx]
			newVal := 255.0
			isBlack := false
			if old < threshold {
				newVal = 0
				isBlack = true
			}
			black[idx] = isBlack
			err := old - newVal
			diffuseError(work, dstW, dstH, x+1, y, err*7.0/16.0)
			diffuseError(work, dstW, dstH, x-1, y+1, err*3.0/16.0)
			diffuseError(work, dstW, dstH, x, y+1, err*5.0/16.0)
			diffuseError(work, dstW, dstH, x+1, y+1, err*1.0/16.0)
		}
	}

	return monoRaster{width: dstW, height: dstH, black: black}, nil
}

func diffuseError(buf []float64, width, height, x, y int, delta float64) {
	if x < 0 || y < 0 || x >= width || y >= height {
		return
	}
	buf[y*width+x] += delta
}
