package modules

import (
	"context"
	"fmt"
	"html/template"
	"strings"

	"tickr/internal/kids"
)

func childName(env *Env) string {
	return strings.TrimSpace(env.Cfg.General.ChildName)
}

// ── Letter & number of the day ───────────────────────────────────────────────

type letterModule struct{}

var letterWords = map[string][]string{
	"A": {"apple", "ant", "airplane"}, "B": {"ball", "bear", "banana"}, "C": {"cat", "car", "cookie"}, "D": {"dog", "duck", "drum"},
	"E": {"egg", "elephant", "ear"}, "F": {"fish", "frog", "flower"}, "G": {"goat", "grapes", "guitar"}, "H": {"hat", "horse", "house"},
	"I": {"igloo", "ice cream", "iguana"}, "J": {"jam", "jellyfish", "jump"}, "K": {"kite", "kangaroo", "key"}, "L": {"lion", "leaf", "lemon"},
	"M": {"moon", "monkey", "milk"}, "N": {"nest", "nose", "nut"}, "O": {"owl", "orange", "octopus"}, "P": {"pig", "pizza", "penguin"},
	"Q": {"queen", "quilt", "quack"}, "R": {"rabbit", "rainbow", "robot"}, "S": {"sun", "star", "snake"}, "T": {"tree", "turtle", "train"},
	"U": {"umbrella", "unicorn", "up"}, "V": {"van", "violin", "volcano"}, "W": {"whale", "water", "worm"}, "X": {"x-ray", "xylophone", "fox"},
	"Y": {"yak", "yo-yo", "yellow"}, "Z": {"zebra", "zoo", "zipper"},
}

var letterTpl = Tpl("letter", `<div class="h">{{.Title}}</div>
{{if eq .Mode "both"}}<table class="kid2"><tr>
<td><div class="kid-lbl">Letter</div>{{.LetterSVG}}<div class="kid-word">{{.Letter}} is for {{.Word}}</div></td>
<td><div class="kid-lbl">Number</div>{{.NumberSVG}}<div class="kid-word">{{.NumberWord}}</div></td>
</tr></table>
{{else if eq .Mode "letter"}}<div class="c">{{.LetterSVG}}</div><div class="c kid-big">{{.Letter}} is for {{.Word}}</div>{{if .MoreWords}}<div class="c s">also: {{.MoreWords}}</div>{{end}}
<div class="c s" style="margin-top:4px">Trace me!</div><div class="c">{{.TraceRow}}</div>
{{else}}<div class="c">{{.NumberSVG}}</div><div class="c kid-big">{{.NumberWord}}</div>
<div class="c s" style="margin-top:4px">Trace me!</div><div class="c">{{.TraceRow}}</div>{{end}}
{{if .Count}}<div class="c s" style="margin-top:4px">Count the stars!</div><div class="c">{{.Count}}</div>{{end}}`)

func (letterModule) Info() Info {
	return Info{
		ID: "kids_letter", Name: "Letter / number of the day", Category: CatKids, DefaultEnabled: false,
		Description: "A big dashed letter or number to trace (or both side by side), a word for the letter, a tracing row and stars to count.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "", Help: "Blank picks a heading to match the mode."},
			{Key: "mode", Label: "Show", Type: FieldSelect, Options: []string{"both", "letter", "number"}, Default: "both"},
			{Key: "maxNumber", Label: "Highest number", Type: FieldNumber, Default: 10, Min: F64(3), Max: F64(20)},
			{Key: "stars", Label: "Show stars to count", Type: FieldBool, Default: true},
			{Key: "lowercase", Label: "Also show lowercase letter", Type: FieldBool, Default: true},
			{Key: "pick", Label: "Pick by", Type: FieldSelect, Options: []string{"day", "sequence"}, Default: "day", Help: "day = the same letter/number all day; sequence = A, B, C… one per print."},
		},
	}
}

var numberWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
	"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen", "twenty"}

func (letterModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	mode := opt.Str("mode", "both")
	seed := env.DaySeed()
	if opt.Str("pick", "day") == "sequence" {
		seed = sequentialIndex(env.DataDir, "kids_letter", 26*20)
	}
	letter := string(rune('A' + seed%26))
	words := letterWords[letter]
	word := words[(seed/26)%len(words)]
	var more []string
	for _, w := range words {
		if w != word {
			more = append(more, w)
		}
	}
	maxN := opt.Int("maxNumber", 10)
	n := 1 + (seed*7)%maxN
	glyph := letter
	if opt.Bool("lowercase", true) {
		glyph = letter + strings.ToLower(letter)
	}
	title := opt.Str("title", "")
	if title == "" {
		title = map[string]string{"both": "Today's letter & number", "letter": "Letter of the day", "number": "Number of the day"}[mode]
	}
	data := struct {
		Title, Mode, Letter, Word, MoreWords, NumberWord string
		LetterSVG, NumberSVG, TraceRow, Count            template.HTML
	}{Title: title, Mode: mode, Letter: letter, Word: word, MoreWords: strings.Join(more, ", "), NumberWord: numberWords[n]}
	est := 20
	switch mode {
	case "letter":
		data.LetterSVG = template.HTML(kids.TraceLetterSVG(glyph, 102))
		data.TraceRow = template.HTML(traceRow(letter, 4, 34))
		est += 102 + 18 + 10 + 12 + 34
	case "number":
		data.NumberSVG = template.HTML(kids.TraceLetterSVG(fmt.Sprint(n), 102))
		data.TraceRow = template.HTML(traceRow(fmt.Sprint(n), 4, 34))
		est += 102 + 18 + 12 + 34
	default:
		data.LetterSVG = template.HTML(kids.TraceLetterSVG(glyph, 70))
		data.NumberSVG = template.HTML(kids.TraceLetterSVG(fmt.Sprint(n), 70))
		est += 100
	}
	if opt.Bool("stars", true) && mode != "letter" {
		data.Count = template.HTML(kids.CountingRowSVG(n, 5, 16))
		est += 12 + ((n+4)/5)*16
	}
	return Exec("kids_letter", letterTpl, data, est)
}

// traceRow draws n small dashed copies of a glyph side by side.
func traceRow(glyph string, n, size int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(kids.TraceLetterSVG(glyph, size))
	}
	return b.String()
}

// ── Shape of the day ─────────────────────────────────────────────────────────

type shapeModule struct{}

var shapeTpl = Tpl("shape", `<div class="h">{{.Title}}</div><div class="c">{{.SVG}}</div><div class="c kid-word">{{.Name}}</div>{{if .Hint}}<div class="c s">{{.Hint}}</div>{{end}}`)

var colorNames = []string{"red", "blue", "yellow", "green", "orange", "purple", "pink", "brown", "black", "white"}

func (shapeModule) Info() Info {
	return Info{
		ID: "kids_shape", Name: "Shape & colour of the day", Category: CatKids, DefaultEnabled: false,
		Description: "A big shape outline to trace or colour in, plus a colour scavenger hint.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Shape of the day"},
			{Key: "size", Label: "Size (px)", Type: FieldNumber, Default: 90, Min: F64(50), Max: F64(160)},
			{Key: "dashed", Label: "Dashed outline for tracing", Type: FieldBool, Default: true},
			{Key: "colour", Label: "Add colour hunt hint", Type: FieldBool, Default: true},
		},
	}
}

func (shapeModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	seed := env.DaySeed()
	s := kids.Shapes[seed%len(kids.Shapes)]
	size := opt.Int("size", 90)
	hint := ""
	if opt.Bool("colour", true) {
		hint = fmt.Sprintf("Colour it %s, then find 3 %s things!", colorNames[seed%len(colorNames)], colorNames[seed%len(colorNames)])
	}
	data := struct {
		Title, Name, Hint string
		SVG               template.HTML
	}{opt.Str("title", "Shape of the day"), s.Name, hint, template.HTML(kids.ShapeSVG(s, size, opt.Bool("dashed", true)))}
	return Exec("kids_shape", shapeTpl, data, 20+size+30)
}

// ── Maze ─────────────────────────────────────────────────────────────────────

type mazeModule struct{}

var mazeTpl = Tpl("maze", `<div class="h">{{.Title}}</div><div class="c">{{.SVG}}</div><div class="c s">Start at the dot, find the star!</div>`)

func (mazeModule) Info() Info {
	return Info{
		ID: "kids_maze", Name: "Maze", Category: CatKids, DefaultEnabled: false,
		Description: "A fresh maze every day. Keep it 4×4 or 5×5 for toddlers; older kids can handle 10×12.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Maze"},
			{Key: "cols", Label: "Columns", Type: FieldNumber, Default: 5, Min: F64(2), Max: F64(20)},
			{Key: "rows", Label: "Rows", Type: FieldNumber, Default: 5, Min: F64(2), Max: F64(30)},
			{Key: "width", Label: "Width (px)", Type: FieldNumber, Default: 150, Min: F64(80), Max: F64(180)},
		},
	}
}

func (mazeModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	cols, rows, width := opt.Int("cols", 5), opt.Int("rows", 5), opt.Int("width", 150)
	m := kids.NewMaze(cols, rows, int64(env.DaySeed()))
	height := width * rows / cols
	return Exec("kids_maze", mazeTpl, struct {
		Title string
		SVG   template.HTML
	}{opt.Str("title", "Maze"), template.HTML(m.SVG(width))}, 20+height+16)
}

// ── Toddler jokes (built in, always age-appropriate) ─────────────────────────

type kidJokeModule struct{}

var kidJokes = []string{
	"Knock knock!\nWho's there?\nBoo.\nBoo who?\nDon't cry, it's just a joke!",
	"What do you call a sleeping dinosaur?\nA dino-SNORE!",
	"Why did the banana go to the doctor?\nIt wasn't peeling well!",
	"What does a cow say when it is happy?\nMoo-velous!",
	"Knock knock!\nWho's there?\nLettuce.\nLettuce who?\nLettuce in, it's cold out here!",
	"What do you call a bear with no teeth?\nA gummy bear!",
	"Why did the cookie go to the doctor?\nIt felt crummy!",
	"What is a cat's favourite colour?\nPurr-ple!",
	"Knock knock!\nWho's there?\nCow says.\nCow says who?\nNo silly, a cow says MOO!",
	"What do you call a pig that does karate?\nA pork chop!",
	"Why do ducks have feathers?\nTo cover their butt quacks!",
	"How do you make a tissue dance?\nPut a little boogie in it!",
	"What do you call a fish with no eyes?\nA fsh!",
	"Knock knock!\nWho's there?\nInterrupting cow.\nInterrupting c—\nMOO!",
	"Why was six afraid of seven?\nBecause seven eight nine!",
	"What did the ocean say to the beach?\nNothing, it just waved!",
	"What is a frog's favourite drink?\nCroak-a-cola!",
	"Why did the teddy bear say no to dessert?\nBecause it was stuffed!",
	"What do you call a dinosaur that crashes its car?\nTyrannosaurus WRECKS!",
	"Knock knock!\nWho's there?\nBanana.\nBanana who?\nKnock knock!\nWho's there?\nOrange.\nOrange who?\nOrange you glad I didn't say banana?",
	"What has ears but cannot hear?\nA cornfield!",
	"Why did the chicken cross the playground?\nTo get to the other slide!",
	"What do you call a snowman in summer?\nA puddle!",
	"How does a train eat?\nChew chew!",
	"What is a bunny's favourite music?\nHip hop!",
	"What do you call a dog magician?\nA labracadabrador!",
	"Why did the crayon cry?\nIt was feeling blue!",
	"What kind of key opens a banana?\nA mon-key!",
	"What do you give a sick lemon?\nLemon-aid!",
	"Why can't Elsa have a balloon?\nBecause she will let it go!",
}

func (kidJokeModule) Info() Info {
	return Info{
		ID: "kids_joke", Name: "Silly joke for little ones", Category: CatKids, DefaultEnabled: false,
		Description: "A knock-knock or animal joke from a built-in list, so it is always toddler-safe.",
		Fields:      []Field{{Key: "title", Label: "Heading", Type: FieldText, Default: "Silly joke"}},
	}
}

func (kidJokeModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	return quoteSection("kids_joke", opt.Str("title", "Silly joke"), kidJokes[env.DaySeed()%len(kidJokes)], "")
}

// ── Kid-friendly weather sentence ────────────────────────────────────────────

type kidWeatherModule struct{}

var kidWeatherTpl = Tpl("kidweather", `<div class="h">{{.Title}}</div><table class="poke"><tr><td>{{.Icon}}</td><td class="t">{{.Text}}</td></tr></table>`)

func (kidWeatherModule) Info() Info {
	return Info{
		ID: "kids_weather", Name: "Weather for little ones", Category: CatKids, DefaultEnabled: false, Needs: []string{"weather"},
		Description: "One simple sentence about today's weather and what to wear, written for a three-year-old.",
		Fields:      []Field{{Key: "title", Label: "Heading", Type: FieldText, Default: "Outside today"}},
	}
}

func (kidWeatherModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	fc, err := env.Forecast(ctx)
	if err != nil {
		return nil, err
	}
	day := todayDaily(fc, env.Now)
	if day == nil {
		return EmptySection("kids_weather"), nil
	}
	name := childName(env)
	hi := day.MaxC
	var feel, wear string
	switch {
	case hi >= 30:
		feel, wear = "It is very hot today", "Wear shorts and a sun hat, and drink lots of water"
	case hi >= 22:
		feel, wear = "It is warm and nice today", "Shorts and a t-shirt are perfect"
	case hi >= 14:
		feel, wear = "It is a little cool today", "Wear long sleeves"
	case hi >= 5:
		feel, wear = "It is chilly today", "Wear a jacket"
	default:
		feel, wear = "Brrr, it is very cold today", "Wear a warm coat, hat and mittens"
	}
	sky := ""
	switch day.Code {
	case 1000, 1100:
		sky = "The sun is shining!"
	case 1101, 1102, 1001:
		sky = "There are clouds in the sky."
	case 2000, 2100:
		sky = "It is foggy, like walking in a cloud!"
	case 4000, 4200, 4001, 4201:
		sky = "It is going to rain. Bring your boots and umbrella!"
	case 5000, 5001, 5100, 5101:
		sky = "It might SNOW today!"
	case 8000:
		sky = "There may be thunder. Boom boom!"
	default:
		if day.PrecipProb >= 50 {
			sky = "It might rain. Bring your boots!"
		}
	}
	greeting := "Hi there!"
	if name != "" {
		greeting = "Hi " + name + "!"
	}
	text := fmt.Sprintf("%s %s. %s %s.", greeting, feel, sky, wear)
	icon := weatherIcon(env, day.Code, true, 40)
	return Exec("kids_weather", kidWeatherTpl, struct {
		Title, Text string
		Icon        template.HTML
	}{opt.Str("title", "Outside today"), text, icon}, 20+max(44, LinesPx(text, 26, 13)))
}

// ── Daily routine chart ──────────────────────────────────────────────────────

type routineModule struct{}

var routineTpl = Tpl("routine", `<div class="h">{{.Title}}</div><table class="routine">{{range .Items}}<tr><td class="box">&#9744;</td><td>{{.}}</td></tr>{{end}}</table>`)

func (routineModule) Info() Info {
	return Info{
		ID: "kids_routine", Name: "Routine chart", Category: CatKids, DefaultEnabled: false,
		Description: "Big checkboxes for the morning routine: brush teeth, get dressed, feed the cat…",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "{name}'s jobs", Help: "{name} inserts the child's name"},
			{Key: "items", Label: "Items", Type: FieldList, Default: "Brush teeth\nGet dressed\nEat breakfast\nPut on shoes\nTidy toys"},
		},
	}
}

func (routineModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	items := opt.List("items")
	if len(items) == 0 {
		items = []string{"Brush teeth", "Get dressed", "Eat breakfast", "Put on shoes", "Tidy toys"}
	}
	title := opt.Str("title", "{name}'s jobs")
	if name := childName(env); name != "" {
		title = strings.ReplaceAll(title, "{name}", strings.ToUpper(name[:1])+name[1:])
	} else {
		title = strings.ReplaceAll(strings.ReplaceAll(title, "{name}'s", "My"), "{name}", "My")
	}
	return Exec("kids_routine", routineTpl, struct {
		Title string
		Items []string
	}{title, items}, 20+len(items)*18)
}

// ── Doodle box ───────────────────────────────────────────────────────────────

type doodleModule struct{}

var doodleTpl = Tpl("doodle", `<div class="h">{{.Title}}</div><div class="c">{{.SVG}}</div>{{if .Prompt}}<div class="c s">{{.Prompt}}</div>{{end}}`)

var doodlePrompts = []string{"Draw your family", "Draw a happy sun", "Draw a big truck", "Draw a cat", "Draw what you ate for breakfast",
	"Draw a rainbow", "Draw a monster with 3 eyes", "Draw a house", "Draw a fish", "Draw a flower", "Draw a dinosaur", "Draw yourself"}

func (doodleModule) Info() Info {
	return Info{
		ID: "kids_doodle", Name: "Doodle box", Category: CatKids, DefaultEnabled: false,
		Description: "An empty dotted box with a drawing prompt.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Doodle time"},
			{Key: "height", Label: "Height (px)", Type: FieldNumber, Default: 110, Min: F64(40), Max: F64(300)},
			{Key: "prompt", Label: "Show drawing prompt", Type: FieldBool, Default: true},
		},
	}
}

func (doodleModule) Render(_ context.Context, env *Env, opt Options) (*Section, error) {
	h := opt.Int("height", 110)
	cell := 12
	prompt := ""
	if opt.Bool("prompt", true) {
		prompt = doodlePrompts[env.DaySeed()%len(doodlePrompts)]
	}
	return Exec("kids_doodle", doodleTpl, struct {
		Title, Prompt string
		SVG           template.HTML
	}{opt.Str("title", "Doodle time"), prompt, template.HTML(kids.DotGridSVG(14, h/cell, cell))}, 20+h+12)
}

func init() {
	Register(letterModule{})
	Register(shapeModule{})
	Register(mazeModule{})
	Register(kidJokeModule{})
	Register(kidWeatherModule{})
	Register(routineModule{})
	Register(doodleModule{})
}
