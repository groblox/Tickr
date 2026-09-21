// Package faces finds faces with pigo, a pure-Go pixel-intensity cascade
// detector, so no OpenCV or CGO is needed.
package faces

import (
	"fmt"
	"image"
	"sort"
	"sync"

	pigo "github.com/esimov/pigo/core"

	"breaklist/assets"
	"breaklist/internal/imaging"
)

// Face is one detection in the coordinates of the image passed to Detect.
type Face struct {
	Rect  image.Rectangle
	Score float32
}

var (
	once       sync.Once
	classifier *pigo.Pigo
	loadErr    error
)

func load() (*pigo.Pigo, error) {
	once.Do(func() {
		classifier, loadErr = pigo.NewPigo().Unpack(assets.FaceCascade())
	})
	return classifier, loadErr
}

// Detect returns faces sorted largest first. Detection runs on a copy scaled
// to at most 900px wide for speed; rectangles are mapped back to img.
func Detect(img image.Image, minScore float32) ([]Face, error) {
	cls, err := load()
	if err != nil {
		return nil, fmt.Errorf("loading face cascade: %w", err)
	}
	const workW = 900
	work := imaging.Resize(img, workW)
	scale := float64(img.Bounds().Dx()) / float64(work.Bounds().Dx())
	gray := imaging.Flatten(work)
	rows, cols := gray.Bounds().Dy(), gray.Bounds().Dx()
	params := pigo.CascadeParams{
		MinSize:     20,
		MaxSize:     rows,
		ShiftFactor: 0.1,
		ScaleFactor: 1.1,
		ImageParams: pigo.ImageParams{Pixels: gray.Pix, Rows: rows, Cols: cols, Dim: gray.Stride},
	}
	dets := cls.ClusterDetections(cls.RunCascade(params, 0.0), 0.2)
	var out []Face
	for _, d := range dets {
		if d.Q < minScore {
			continue
		}
		r := float64(d.Scale) / 2
		rect := image.Rect(
			int((float64(d.Col)-r)*scale), int((float64(d.Row)-r)*scale),
			int((float64(d.Col)+r)*scale), int((float64(d.Row)+r)*scale))
		out = append(out, Face{Rect: rect.Intersect(img.Bounds()), Score: d.Q})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rect.Dx() > out[j].Rect.Dx() })
	return out, nil
}

// CropAround returns a square-ish crop centred on the face, sized margin times
// the face width (2.5 gives head-and-shoulders), clamped to the image.
func CropAround(img image.Image, f Face, margin float64) image.Image {
	cx := (f.Rect.Min.X + f.Rect.Max.X) / 2
	cy := (f.Rect.Min.Y + f.Rect.Max.Y) / 2
	half := int(float64(f.Rect.Dx()) * margin / 2)
	if half < 16 {
		half = 16
	}
	// Portrait-ish box (taller than wide), biased downward so there is room
	// for shoulders and body rather than empty space above the head.
	r := image.Rect(cx-half, cy-int(float64(half)*0.95), cx+half, cy+int(float64(half)*1.35))
	// If the box runs off the image, slide it back inside instead of clipping
	// so the crop keeps its proportions.
	b := img.Bounds()
	if r.Dx() > b.Dx() {
		r.Min.X, r.Max.X = b.Min.X, b.Max.X
	} else if r.Min.X < b.Min.X {
		r = r.Add(image.Pt(b.Min.X-r.Min.X, 0))
	} else if r.Max.X > b.Max.X {
		r = r.Sub(image.Pt(r.Max.X-b.Max.X, 0))
	}
	if r.Dy() > b.Dy() {
		r.Min.Y, r.Max.Y = b.Min.Y, b.Max.Y
	} else if r.Min.Y < b.Min.Y {
		r = r.Add(image.Pt(0, b.Min.Y-r.Min.Y))
	} else if r.Max.Y > b.Max.Y {
		r = r.Sub(image.Pt(0, r.Max.Y-b.Max.Y))
	}
	return imaging.Crop(img, r)
}
