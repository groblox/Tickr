// Package imaging prepares pictures for a 1-bit thermal printer.
package imaging

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif" // register decoders
	"image/jpeg"
	"image/png"
)

// Decode parses PNG, JPEG or GIF bytes.
func Decode(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}
	return img, nil
}

// Resize scales an image so its width is at most maxW pixels using box
// sampling (which keeps pixel art crisp enough for print).
func Resize(img image.Image, maxW int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxW || maxW <= 0 {
		return img
	}
	nw := maxW
	nh := int(float64(h) * float64(maxW) / float64(w))
	if nh < 1 {
		nh = 1
	}
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy0 := b.Min.Y + y*h/nh
		sy1 := b.Min.Y + (y+1)*h/nh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < nw; x++ {
			sx0 := b.Min.X + x*w/nw
			sx1 := b.Min.X + (x+1)*w/nw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var r, g, bl, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					pr, pg, pb, pa := img.At(sx, sy).RGBA()
					r += uint64(pr)
					g += uint64(pg)
					bl += uint64(pb)
					a += uint64(pa)
					n++
				}
			}
			out.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return out
}

// Flatten composites transparent pixels onto white.
func Flatten(img image.Image) *image.Gray {
	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, image.White, image.Point{}, draw.Src)
	draw.Draw(rgba, b, img, b.Min, draw.Over)
	gray := image.NewGray(b)
	draw.Draw(gray, b, rgba, b.Min, draw.Src)
	return gray
}

// Dither converts to pure black and white using Floyd–Steinberg error
// diffusion, which looks far better on thermal paper than a plain threshold.
func Dither(img image.Image) *image.Gray {
	gray := Flatten(img)
	b := gray.Bounds()
	w, h := b.Dx(), b.Dy()
	buf := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf[y*w+x] = float64(gray.GrayAt(b.Min.X+x, b.Min.Y+y).Y)
		}
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			old := buf[y*w+x]
			var nv float64
			if old >= 128 {
				nv = 255
			}
			out.SetGray(x, y, color.Gray{Y: uint8(nv)})
			e := old - nv
			if x+1 < w {
				buf[y*w+x+1] += e * 7 / 16
			}
			if y+1 < h {
				if x > 0 {
					buf[(y+1)*w+x-1] += e * 3 / 16
				}
				buf[(y+1)*w+x] += e * 5 / 16
				if x+1 < w {
					buf[(y+1)*w+x+1] += e * 1 / 16
				}
			}
		}
	}
	return out
}

// Atkinson dithers with Bill Atkinson's algorithm (the classic Macintosh
// look): only 6/8 of the error is diffused, which gives brighter, punchier
// output that suits low-resolution thermal printers.
func Atkinson(img image.Image) *image.Gray {
	gray := Flatten(img)
	b := gray.Bounds()
	w, h := b.Dx(), b.Dy()
	buf := make([]float64, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			buf[y*w+x] = float64(gray.GrayAt(b.Min.X+x, b.Min.Y+y).Y)
		}
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	spread := [][2]int{{1, 0}, {2, 0}, {-1, 1}, {0, 1}, {1, 1}, {0, 2}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			old := buf[y*w+x]
			var nv float64
			if old >= 128 {
				nv = 255
			}
			out.SetGray(x, y, color.Gray{Y: uint8(nv)})
			e := (old - nv) / 8
			for _, d := range spread {
				nx, ny := x+d[0], y+d[1]
				if nx >= 0 && nx < w && ny < h {
					buf[ny*w+nx] += e
				}
			}
		}
	}
	return out
}

// DitherMode selects how an image is reduced to black and white.
type DitherMode string

// Dither modes accepted by PrepareForPrint.
const (
	DitherNone     DitherMode = "none"
	DitherFloyd    DitherMode = "floyd"
	DitherAtkinson DitherMode = "atkinson"
)

// ParseDither accepts mode names plus legacy true/false values.
func ParseDither(s string) DitherMode {
	switch s {
	case "floyd", "true", "floyd-steinberg":
		return DitherFloyd
	case "atkinson":
		return DitherAtkinson
	}
	return DitherNone
}

// Threshold converts to black and white with a hard cut-off (good for line art).
func Threshold(img image.Image, cut uint8) *image.Gray {
	gray := Flatten(img)
	b := gray.Bounds()
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			v := gray.GrayAt(b.Min.X+x, b.Min.Y+y).Y
			if v >= cut {
				v = 255
			} else {
				v = 0
			}
			out.SetGray(x, y, color.Gray{Y: v})
		}
	}
	return out
}

// EncodePNG returns PNG bytes.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeJPEG returns JPEG bytes.
func EncodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DataURI wraps bytes as an inline data: URL.
func DataURI(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// PrepareForPrint decodes, uprights, shrinks to maxW and dithers, returning a
// PNG data URI.
func PrepareForPrint(data []byte, maxW int, mode DitherMode) (string, error) {
	img, err := Decode(data)
	if err != nil {
		return "", err
	}
	img = Orient(img, ReadMeta(data).Orientation)
	return EncodeForPrint(img, maxW, mode)
}

// EncodeForPrint shrinks a decoded image to maxW, dithers it and returns a PNG data URI.
func EncodeForPrint(img image.Image, maxW int, mode DitherMode) (string, error) {
	img = Resize(img, maxW)
	var out image.Image
	switch mode {
	case DitherFloyd:
		out = Dither(img)
	case DitherAtkinson:
		out = Atkinson(img)
	default:
		out = Flatten(img)
	}
	b, err := EncodePNG(out)
	if err != nil {
		return "", err
	}
	return DataURI("image/png", b), nil
}
