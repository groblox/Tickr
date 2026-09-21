package imaging

import (
	"encoding/binary"
	"image"
	"time"
)

// Meta is the little EXIF data Tickr cares about.
type Meta struct {
	Orientation int       // 1..8 per the EXIF spec, 0 if unknown
	Taken       time.Time // DateTimeOriginal, zero if unknown
}

// ReadMeta extracts orientation and capture time from a JPEG's EXIF block.
// It never fails: unknown or non-JPEG data yields a zero Meta.
func ReadMeta(data []byte) Meta {
	var m Meta
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return m
	}
	pos := 2
	for pos+4 <= len(data) {
		if data[pos] != 0xFF {
			return m
		}
		marker := data[pos+1]
		if marker == 0xDA || marker == 0xD9 { // start of scan / end
			return m
		}
		segLen := int(binary.BigEndian.Uint16(data[pos+2:]))
		if segLen < 2 || pos+2+segLen > len(data) {
			return m
		}
		if marker == 0xE1 && segLen > 8 && string(data[pos+4:pos+10]) == "Exif\x00\x00" {
			parseTIFF(data[pos+10:pos+2+segLen], &m)
			return m
		}
		pos += 2 + segLen
	}
	return m
}

func parseTIFF(t []byte, m *Meta) {
	if len(t) < 8 {
		return
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return
	}
	ifd0 := int(bo.Uint32(t[4:]))
	exifOff := 0
	walk := func(off int, visit func(tag, typ uint16, count uint32, val []byte)) {
		if off <= 0 || off+2 > len(t) {
			return
		}
		n := int(bo.Uint16(t[off:]))
		for i := 0; i < n; i++ {
			e := off + 2 + i*12
			if e+12 > len(t) {
				return
			}
			tag := bo.Uint16(t[e:])
			typ := bo.Uint16(t[e+2:])
			count := bo.Uint32(t[e+4:])
			visit(tag, typ, count, t[e+8:e+12])
		}
	}
	walk(ifd0, func(tag, typ uint16, count uint32, val []byte) {
		switch tag {
		case 0x0112:
			if typ == 3 {
				m.Orientation = int(bo.Uint16(val))
			}
		case 0x8769:
			exifOff = int(bo.Uint32(val))
		}
	})
	walk(exifOff, func(tag, typ uint16, count uint32, val []byte) {
		if tag == 0x9003 && typ == 2 && count >= 19 {
			off := int(bo.Uint32(val))
			if off+19 <= len(t) {
				if ts, err := time.Parse("2006:01:02 15:04:05", string(t[off:off+19])); err == nil {
					m.Taken = ts
				}
			}
		}
	})
}

// Orient applies an EXIF orientation so the image is upright.
func Orient(img image.Image, orientation int) image.Image {
	switch orientation {
	case 2:
		return flipH(img)
	case 3:
		return rotate180(img)
	case 4:
		return flipV(img)
	case 5:
		return rotate90(flipH(img))
	case 6:
		return rotate90(img)
	case 7:
		return rotate90(flipV(img))
	case 8:
		return rotate270(img)
	}
	return img
}

func rotate90(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(b.Max.Y-1-y, x-b.Min.X, img.At(x, y))
		}
	}
	return out
}

func rotate270(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(y-b.Min.Y, b.Max.X-1-x, img.At(x, y))
		}
	}
	return out
}

func rotate180(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(b.Max.X-1-x, b.Max.Y-1-y, img.At(x, y))
		}
	}
	return out
}

func flipH(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(b.Max.X-1-x, y-b.Min.Y, img.At(x, y))
		}
	}
	return out
}

func flipV(img image.Image) image.Image {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.Set(x-b.Min.X, b.Max.Y-1-y, img.At(x, y))
		}
	}
	return out
}

// Crop returns the part of img inside r (clamped to the image bounds).
func Crop(img image.Image, r image.Rectangle) image.Image {
	r = r.Intersect(img.Bounds())
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			out.Set(x-r.Min.X, y-r.Min.Y, img.At(x, y))
		}
	}
	return out
}
