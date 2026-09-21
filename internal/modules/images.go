package modules

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tickr/internal/httpx"
	"tickr/internal/imaging"
)

var imageTpl = Tpl("image", `{{if .Title}}<div class="h">{{.Title}}</div>{{end}}<div class="imgwrap"><img src="{{url .Src}}" style="width:{{.Width}}%{{if .Offset}};position:relative;left:-{{.Offset}}%{{end}}"></div>{{if .Caption}}<div class="c s">{{.Caption}}</div>{{end}}`)

// imageSection lays out a picture. Widths above 100% are centred and the
// overflow on both sides is clipped by the wrapper.
func imageSection(id, title, src, caption string, widthPct, estPx int) (*Section, error) {
	if widthPct < 10 {
		widthPct = 100
	}
	offset := 0
	if widthPct > 100 {
		offset = (widthPct - 100) / 2
	}
	return Exec(id, imageTpl, struct {
		Title, Src, Caption string
		Width, Offset       int
	}{title, src, caption, widthPct, offset}, estPx)
}

// ── Local image folder (Far Side library) ────────────────────────────────────

type folderImageModule struct{}

func (folderImageModule) Info() Info {
	return Info{
		ID: "farside", Name: "Picture from a folder", Category: CatFun, DefaultEnabled: true,
		Description: "A random image from a local folder (for example a scraped Far Side library). JPG, PNG and GIF are accepted.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText},
			{Key: "folder", Label: "Folder", Type: FieldPath, Default: "jpgs/farside", Help: "Absolute path, or relative to the data folder."},
			{Key: "mode", Label: "Pick", Type: FieldSelect, Options: []string{"random", "daily", "sequential"}, Default: "random", Help: "daily = same picture all day; sequential = walk the folder in name order"},
			{Key: "dither", Label: "Dithering", Type: FieldSelect, Options: []string{"none", "atkinson", "floyd"}, Default: "none", Help: "Use atkinson or floyd for photos; none for images that are already black and white."},
			{Key: "width", Label: "Width (%)", Type: FieldNumber, Default: 100, Min: F64(30), Max: F64(300), Help: "Above 100% the picture is enlarged and the sides are cropped off."},
		},
	}
}

func (folderImageModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	dir := env.resolve(opt.Str("folder", "jpgs/farside"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading image folder: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".jpg", ".jpeg", ".png", ".gif":
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no images found in %s", dir)
	}
	sort.Strings(files)
	var idx int
	switch opt.Str("mode", "random") {
	case "daily":
		idx = env.DaySeed() % len(files)
	case "sequential":
		idx = sequentialIndex(env.DataDir, "farside", len(files))
	default:
		idx = rand.New(rand.NewSource(time.Now().UnixNano())).Intn(len(files))
	}
	data, err := os.ReadFile(filepath.Join(dir, files[idx]))
	if err != nil {
		return nil, err
	}
	w := opt.Int("width", 100)
	maxW := 400
	if w > 100 {
		maxW = 400 * w / 100
	}
	src, err := imaging.PrepareForPrint(data, maxW, imaging.ParseDither(opt.Str("dither", "none")))
	if err != nil {
		return nil, err
	}
	env.Log("picture: %s", files[idx])
	return imageSection("farside", opt.Str("title", ""), src, "", w, 180*w/100+10)
}

// sequentialIndex keeps a tiny counter file so each run advances one image.
func sequentialIndex(dataDir, key string, n int) int {
	path := filepath.Join(dataDir, ".tickr-"+key+".idx")
	var cur int
	if b, err := os.ReadFile(path); err == nil {
		fmt.Sscanf(string(b), "%d", &cur)
	}
	idx := cur % n
	_ = os.WriteFile(path, []byte(fmt.Sprint(idx+1)), 0o600)
	return idx
}

// ── xkcd ─────────────────────────────────────────────────────────────────────

type xkcdModule struct{}

func (xkcdModule) Info() Info {
	return Info{
		ID: "xkcd", Name: "xkcd comic", Category: CatFun, DefaultEnabled: false, Source: "xkcd.com",
		Description: "A random or the latest xkcd strip. Wide strips are shrunk to fit; tall ones print best.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "xkcd"},
			{Key: "mode", Label: "Pick", Type: FieldSelect, Options: []string{"random", "latest"}, Default: "random"},
			{Key: "maxAspect", Label: "Skip comics wider than (width ÷ height)", Type: FieldNumber, Default: 2, Min: F64(1), Max: F64(10), Help: "Very wide strips become unreadable on narrow paper."},
		},
	}
}

func (xkcdModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	type comic struct {
		Num   int    `json:"num"`
		Title string `json:"title"`
		Img   string `json:"img"`
		Alt   string `json:"alt"`
	}
	var latest comic
	if err := httpx.GetJSON(ctx, "https://xkcd.com/info.0.json", nil, &latest); err != nil {
		return nil, fmt.Errorf("xkcd: %w", err)
	}
	chosen := latest
	if opt.Str("mode", "random") == "random" {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		maxAspect := float64(opt.Int("maxAspect", 2))
		for attempt := 0; attempt < 6; attempt++ {
			num := 1 + rng.Intn(latest.Num)
			if num == 404 { // famously does not exist
				continue
			}
			var c comic
			if err := httpx.GetJSON(ctx, fmt.Sprintf("https://xkcd.com/%d/info.0.json", num), nil, &c); err != nil {
				continue
			}
			data, _, err := httpx.GetBytes(ctx, c.Img, nil)
			if err != nil {
				continue
			}
			img, err := imaging.Decode(data)
			if err != nil {
				continue
			}
			b := img.Bounds()
			if float64(b.Dx())/float64(b.Dy()) > maxAspect {
				continue
			}
			chosen = c
			break
		}
	}
	data, _, err := httpx.GetBytes(ctx, chosen.Img, nil)
	if err != nil {
		return nil, fmt.Errorf("xkcd image: %w", err)
	}
	img, err := imaging.Decode(data)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	src, err := imaging.PrepareForPrint(data, 500, imaging.DitherNone)
	if err != nil {
		return nil, err
	}
	// Height on paper ≈ paper width * (h/w); express in px at 96dpi.
	paperPx := int(env.Cfg.General.PaperWidthMM / 25.4 * 96)
	est := paperPx*b.Dy()/max(b.Dx(), 1) + 40
	caption := fmt.Sprintf("#%d · %s", chosen.Num, chosen.Title)
	return imageSection("xkcd", opt.Str("title", "xkcd"), src, caption, 100, est)
}

// ── Pokémon of the day ───────────────────────────────────────────────────────

type pokemonModule struct{}

var pokemonTpl = Tpl("pokemon", `<div class="h">{{.Title}}</div><table class="poke"><tr><td><img src="{{url .Src}}" width="72" height="72"></td><td><div class="big">{{.Name}}</div><div class="s">#{{.Num}} · {{.Types}}</div><div class="s">{{.Height}} tall · {{.Weight}}</div>{{if .Flavor}}<div class="s" style="margin-top:2px">{{.Flavor}}</div>{{end}}</td></tr></table>`)

func (pokemonModule) Info() Info {
	return Info{
		ID: "pokemon", Name: "Pokémon of the day", Category: CatKids, DefaultEnabled: false, Source: "pokeapi.co",
		Description: "A pixel-art Pokémon sprite with its name, type and Pokédex blurb. Pixel art prints beautifully on thermal paper.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Pokémon of the day"},
			{Key: "generation", Label: "Only the original 151", Type: FieldBool, Default: true},
			{Key: "flavor", Label: "Show Pokédex entry", Type: FieldBool, Default: true},
		},
	}
}

func (pokemonModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	limit := 1025
	if opt.Bool("generation", true) {
		limit = 151
	}
	num := 1 + (env.DaySeed()*37)%limit
	var p struct {
		Name   string `json:"name"`
		Height int    `json:"height"`
		Weight int    `json:"weight"`
		Types  []struct {
			Type struct {
				Name string `json:"name"`
			} `json:"type"`
		} `json:"types"`
		Sprites struct {
			FrontDefault string `json:"front_default"`
		} `json:"sprites"`
	}
	if err := httpx.GetJSON(ctx, fmt.Sprintf("https://pokeapi.co/api/v2/pokemon/%d", num), nil, &p); err != nil {
		return nil, fmt.Errorf("pokeapi: %w", err)
	}
	var types []string
	for _, t := range p.Types {
		types = append(types, t.Type.Name)
	}
	flavor := ""
	if opt.Bool("flavor", true) {
		var sp struct {
			Entries []struct {
				Text string `json:"flavor_text"`
				Lang struct {
					Name string `json:"name"`
				} `json:"language"`
			} `json:"flavor_text_entries"`
		}
		if err := httpx.GetJSON(ctx, fmt.Sprintf("https://pokeapi.co/api/v2/pokemon-species/%d", num), nil, &sp); err == nil {
			for _, e := range sp.Entries {
				if e.Lang.Name == "en" {
					flavor = strings.Join(strings.Fields(e.Text), " ")
					break
				}
			}
		}
	}
	src := ""
	if p.Sprites.FrontDefault != "" {
		if data, _, err := httpx.GetBytes(ctx, p.Sprites.FrontDefault, nil); err == nil {
			src, _ = imaging.PrepareForPrint(data, 96, imaging.DitherFloyd)
		}
	}
	height := fmt.Sprintf("%.1f m", float64(p.Height)/10)
	weight := fmt.Sprintf("%.1f kg", float64(p.Weight)/10)
	if env.Cfg.Imperial() {
		height = fmt.Sprintf("%.1f ft", float64(p.Height)/10*3.281)
		weight = fmt.Sprintf("%.0f lb", float64(p.Weight)/10*2.205)
	}
	data := struct {
		Title, Src, Name, Types, Height, Weight, Flavor string
		Num                                             int
	}{
		opt.Str("title", "Pokémon of the day"), src, prettyName(p.Name), strings.Join(types, " / "), height, weight, flavor, num,
	}
	return Exec("pokemon", pokemonTpl, data, 20+max(76, 40+LinesPx(flavor, 22, 9)))
}

// prettyName turns an API slug like "mr-mime" into "Mr Mime".
func prettyName(slug string) string {
	words := strings.Split(strings.ReplaceAll(slug, "_", "-"), "-")
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func init() {
	Register(folderImageModule{})
	Register(xkcdModule{})
	Register(pokemonModule{})
}
