package printer

import (
	"fmt"
	"image"
	"math"
)

const DefaultImageMaxWidth = 512

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

	p.LineSpacing(24)
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
			gray[y*dstW+x] = 0.299*rf + 0.587*gf + 0.114*bf
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
			if old < 128 {
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
