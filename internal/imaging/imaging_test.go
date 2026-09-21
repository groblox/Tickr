package imaging

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// buildJPEGWithExif wraps a tiny JPEG with an APP1 EXIF segment carrying the
// given orientation and DateTimeOriginal.
func buildJPEGWithExif(t *testing.T, orientation uint16, taken string) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 4, 2))
	var jp bytes.Buffer
	if err := jpeg.Encode(&jp, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := jp.Bytes()
	// TIFF (big endian): header + IFD0 (2 entries) + Exif IFD (1 entry) + date string.
	var tiff bytes.Buffer
	be := binary.BigEndian
	tiff.WriteString("MM")
	binary.Write(&tiff, be, uint16(0x2A))
	binary.Write(&tiff, be, uint32(8))
	// IFD0 at offset 8: count, 2 entries, next=0 => 2+24+4 = 30 bytes, ends at 38
	binary.Write(&tiff, be, uint16(2))
	binary.Write(&tiff, be, uint16(0x0112)) // orientation
	binary.Write(&tiff, be, uint16(3))
	binary.Write(&tiff, be, uint32(1))
	binary.Write(&tiff, be, orientation)
	binary.Write(&tiff, be, uint16(0))
	binary.Write(&tiff, be, uint16(0x8769)) // exif pointer
	binary.Write(&tiff, be, uint16(4))
	binary.Write(&tiff, be, uint32(1))
	binary.Write(&tiff, be, uint32(38))
	binary.Write(&tiff, be, uint32(0))
	// Exif IFD at 38: 1 entry, next=0 => 2+12+4 = 18 bytes, ends at 56
	binary.Write(&tiff, be, uint16(1))
	binary.Write(&tiff, be, uint16(0x9003))
	binary.Write(&tiff, be, uint16(2))
	binary.Write(&tiff, be, uint32(20))
	binary.Write(&tiff, be, uint32(56))
	binary.Write(&tiff, be, uint32(0))
	tiff.WriteString(taken + "\x00")

	var out bytes.Buffer
	out.Write(raw[:2]) // SOI
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	out.Write([]byte{0xFF, 0xE1})
	binary.Write(&out, be, uint16(len(seg)+2))
	out.Write(seg)
	out.Write(raw[2:])
	return out.Bytes()
}

func TestReadMetaAndOrient(t *testing.T) {
	data := buildJPEGWithExif(t, 6, "2019:06:14 09:30:00")
	m := ReadMeta(data)
	if m.Orientation != 6 {
		t.Fatalf("orientation: got %d want 6", m.Orientation)
	}
	if m.Taken.Year() != 2019 || m.Taken.Month() != 6 || m.Taken.Day() != 14 {
		t.Fatalf("taken wrong: %v", m.Taken)
	}
	img, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	up := Orient(img, 6)
	if up.Bounds().Dx() != 2 || up.Bounds().Dy() != 4 {
		t.Fatalf("orientation 6 should rotate 4x2 to 2x4, got %v", up.Bounds())
	}
	if ReadMeta([]byte("not a jpeg")).Orientation != 0 {
		t.Fatal("garbage should yield zero meta")
	}
}

func TestDithersAreBinary(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.SetGray(x, y, color.Gray{Y: uint8(x * 16)})
		}
	}
	for name, fn := range map[string]func(image.Image) *image.Gray{"floyd": Dither, "atkinson": Atkinson} {
		out := fn(img)
		black := 0
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				v := out.GrayAt(x, y).Y
				if v != 0 && v != 255 {
					t.Fatalf("%s: pixel not binary: %d", name, v)
				}
				if v == 0 {
					black++
				}
			}
		}
		if black < 60 || black > 200 {
			t.Fatalf("%s: implausible black count %d for a gradient", name, black)
		}
	}
	c := Crop(img, image.Rect(4, 4, 8, 12))
	if c.Bounds().Dx() != 4 || c.Bounds().Dy() != 8 {
		t.Fatalf("crop size wrong: %v", c.Bounds())
	}
	if ParseDither("true") != DitherFloyd || ParseDither("atkinson") != DitherAtkinson || ParseDither("") != DitherNone {
		t.Fatal("ParseDither mapping wrong")
	}
}
