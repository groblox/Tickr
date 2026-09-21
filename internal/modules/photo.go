package modules

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tickr/internal/faces"
	"tickr/internal/imaging"
)

// ── Family photo with a face ─────────────────────────────────────────────────

type facePhotoModule struct{}

func (facePhotoModule) Info() Info {
	return Info{
		ID: "photo_face", Name: "Family photo (face crop)", Category: CatFun, DefaultEnabled: false,
		Description: "Picks a random photo from a library (subfolders included), finds a face, crops loosely around it and prints it with Atkinson dithering. Photos without a face are skipped.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: ""},
			{Key: "folder", Label: "Photo library folder", Type: FieldPath, Default: `G:\personalmedia\iCloudPhotos`, Help: "JPG and PNG are used; videos, HEIC and RAW files are ignored."},
			{Key: "tries", Label: "Photos to try before giving up", Type: FieldNumber, Default: 12, Min: F64(1), Max: F64(60)},
			{Key: "margin", Label: "Crop looseness", Type: FieldNumber, Default: 4.5, Min: F64(1.2), Max: F64(8), Help: "Crop width as a multiple of the face width. 2.5 ≈ head and shoulders, 4.5 ≈ half body with surroundings, 7 ≈ most of the scene."},
			{Key: "minScore", Label: "Detector strictness", Type: FieldNumber, Default: 12, Min: F64(3), Max: F64(60), Help: "Higher = fewer false faces. 8–15 is typical."},
			{Key: "dither", Label: "Dithering", Type: FieldSelect, Options: []string{"atkinson", "floyd", "none"}, Default: "atkinson"},
			{Key: "width", Label: "Width (%)", Type: FieldNumber, Default: 100, Min: F64(30), Max: F64(300), Help: "Above 100% the sides are cropped off."},
			{Key: "caption", Label: "Caption", Type: FieldSelect, Options: []string{"date", "month", "none", "filename"}, Default: "month", Help: "date/month come from the photo's EXIF data when available."},
		},
	}
}

var photoExts = map[string]bool{".jpg": true, ".jpeg": true, ".png": true}

func (facePhotoModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	root := env.resolve(opt.Str("folder", `G:\personalmedia\iCloudPhotos`))
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("photo folder: %w", err)
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	tries := opt.Int("tries", 12)
	margin := 4.5
	if v, ok := opt["margin"]; ok {
		if f, ok := v.(float64); ok && f > 0 {
			margin = f
		}
	}
	minScore := float32(opt.Int("minScore", 12))
	mode := imaging.ParseDither(opt.Str("dither", "atkinson"))
	var lastErr error
	for i := 0; i < tries; i++ {
		path, err := randomPhoto(rng, root, 4)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			env.Log("photo: %s: %v", filepath.Base(path), err)
			continue
		}
		img, err := imaging.Decode(data)
		if err != nil {
			lastErr = err
			continue
		}
		meta := imaging.ReadMeta(data)
		img = imaging.Orient(img, meta.Orientation)
		found, err := faces.Detect(img, minScore)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			env.Log("photo: no face in %s", filepath.Base(path))
			continue
		}
		crop := faces.CropAround(img, found[0], margin)
		w := opt.Int("width", 100)
		src, err := imaging.EncodeForPrint(crop, env.ImageMaxWidth(w), mode)
		if err != nil {
			return nil, err
		}
		caption := ""
		switch opt.Str("caption", "month") {
		case "date":
			if !meta.Taken.IsZero() {
				caption = meta.Taken.Format("January 2, 2006")
			}
		case "month":
			if !meta.Taken.IsZero() {
				caption = meta.Taken.Format("January 2006")
			}
		case "filename":
			caption = filepath.Base(path)
		}
		env.Log("photo: %s (%d face(s), best score %.0f)", path, len(found), found[0].Score)
		b := crop.Bounds()
		paperPx := int(env.Cfg.General.PaperWidthMM / 25.4 * 96)
		est := paperPx*w/100*b.Dy()/max(b.Dx(), 1) + 24
		return imageSection("photo_face", opt.Str("title", ""), src, caption, w, est)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("no usable photo with a face after %d tries (last error: %v)", tries, lastErr)
	}
	return nil, fmt.Errorf("no face found in %d random photos", tries)
}

// randomPhoto performs random walks down subfolders until one lands on a
// photo file. Dead-end folders (no photos, no subfolders) just trigger
// another walk from the root.
func randomPhoto(rng *rand.Rand, root string, depth int) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 25; attempt++ {
		path, err := randomWalk(rng, root, depth)
		if err == nil {
			return path, nil
		}
		lastErr = err
	}
	return "", lastErr
}

func randomWalk(rng *rand.Rand, dir string, depth int) (string, error) {
	for level := 0; level <= depth; level++ {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", dir, err)
		}
		var photos, dirs []string
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() {
				dirs = append(dirs, name)
			} else if photoExts[strings.ToLower(filepath.Ext(name))] {
				photos = append(photos, name)
			}
		}
		// Prefer descending into folders when there are any, so a library
		// organised by year samples evenly across years rather than favouring
		// loose files at the top level.
		if len(dirs) > 0 && (len(photos) == 0 || rng.Intn(len(dirs)+1) != 0) && level < depth {
			dir = filepath.Join(dir, dirs[rng.Intn(len(dirs))])
			continue
		}
		if len(photos) == 0 {
			return "", fmt.Errorf("no photos found under %s", dir)
		}
		return filepath.Join(dir, photos[rng.Intn(len(photos))]), nil
	}
	return "", fmt.Errorf("no photos found (folders nested too deep)")
}

func init() {
	Register(facePhotoModule{})
}
