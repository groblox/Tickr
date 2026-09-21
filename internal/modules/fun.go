package modules

import (
	"context"
	"fmt"
	"html"
	"strings"

	"breaklist/internal/httpx"
)

var quoteTpl = Tpl("quote", `{{if .Title}}<div class="h">{{.Title}}</div>{{end}}<div class="quote">{{.Text}}</div>{{if .By}}<div class="c s">— {{.By}}</div>{{end}}`)

func quoteSection(id, title, text, by string) (*Section, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return EmptySection(id), nil
	}
	est := LinesPx(text, 32, 14) + 16
	if title != "" {
		est += 20
	}
	if by != "" {
		est += 10
	}
	return Exec(id, quoteTpl, struct{ Title, Text, By string }{title, text, by}, est)
}

// ── JokeAPI ──────────────────────────────────────────────────────────────────

type jokeModule struct{}

func (jokeModule) Info() Info {
	return Info{
		ID: "joke", Name: "Joke of the day", Category: CatFun, DefaultEnabled: true, Source: "jokeapi.dev",
		Description: "A clean joke from JokeAPI (nsfw, racist, sexist and explicit jokes are filtered out).",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText},
			{Key: "categories", Label: "Categories", Type: FieldText, Default: "Miscellaneous,Pun", Help: "Comma separated: Programming, Miscellaneous, Pun, Spooky, Christmas"},
		},
	}
}

func (jokeModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	cats := strings.ReplaceAll(opt.Str("categories", "Miscellaneous,Pun"), " ", "")
	var res struct {
		Error    bool   `json:"error"`
		Type     string `json:"type"`
		Joke     string `json:"joke"`
		Setup    string `json:"setup"`
		Delivery string `json:"delivery"`
	}
	url := fmt.Sprintf("https://v2.jokeapi.dev/joke/%s?blacklistFlags=nsfw,religious,political,racist,sexist,explicit", cats)
	if err := httpx.GetJSON(ctx, url, nil, &res); err != nil {
		return nil, fmt.Errorf("jokeapi: %w", err)
	}
	if res.Error {
		return nil, fmt.Errorf("jokeapi returned an error")
	}
	text := res.Joke
	if res.Type == "twopart" {
		text = res.Setup + "\n\n" + res.Delivery
	}
	return quoteSection("joke", opt.Str("title", ""), text, "")
}

// ── icanhazdadjoke ───────────────────────────────────────────────────────────

type dadJokeModule struct{}

func (dadJokeModule) Info() Info {
	return Info{
		ID: "dadjoke", Name: "Dad joke", Category: CatFun, DefaultEnabled: false, Source: "icanhazdadjoke.com",
		Description: "One groan-worthy dad joke.",
		Fields:      []Field{{Key: "title", Label: "Heading", Type: FieldText, Default: "Dad joke"}},
	}
}

func (dadJokeModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	var res struct {
		Joke string `json:"joke"`
	}
	if err := httpx.GetJSON(ctx, "https://icanhazdadjoke.com/", map[string]string{"Accept": "application/json"}, &res); err != nil {
		return nil, fmt.Errorf("icanhazdadjoke: %w", err)
	}
	return quoteSection("dadjoke", opt.Str("title", "Dad joke"), res.Joke, "")
}

// ── ZenQuotes ────────────────────────────────────────────────────────────────

type quoteModule struct{}

func (quoteModule) Info() Info {
	return Info{
		ID: "quote", Name: "Quote of the day", Category: CatFun, DefaultEnabled: false, Source: "zenquotes.io",
		Description: "An inspirational quote with its author.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: ""},
			{Key: "mode", Label: "Pick", Type: FieldSelect, Options: []string{"today", "random"}, Default: "today"},
		},
	}
}

func (quoteModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	var res []struct {
		Q string `json:"q"`
		A string `json:"a"`
	}
	if err := httpx.GetJSON(ctx, "https://zenquotes.io/api/"+opt.Str("mode", "today"), nil, &res); err != nil {
		return nil, fmt.Errorf("zenquotes: %w", err)
	}
	if len(res) == 0 {
		return EmptySection("quote"), nil
	}
	return quoteSection("quote", opt.Str("title", ""), "“"+res[0].Q+"”", res[0].A)
}

// ── Advice slip ──────────────────────────────────────────────────────────────

type adviceModule struct{}

func (adviceModule) Info() Info {
	return Info{
		ID: "advice", Name: "Random advice", Category: CatFun, DefaultEnabled: false, Source: "adviceslip.com",
		Description: "A one-line piece of advice, wise or silly.",
		Fields:      []Field{{Key: "title", Label: "Heading", Type: FieldText, Default: "Advice"}},
	}
}

func (adviceModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	var res struct {
		Slip struct {
			Advice string `json:"advice"`
		} `json:"slip"`
	}
	if err := httpx.GetJSON(ctx, "https://api.adviceslip.com/advice", nil, &res); err != nil {
		return nil, fmt.Errorf("adviceslip: %w", err)
	}
	return quoteSection("advice", opt.Str("title", "Advice"), res.Slip.Advice, "")
}

// ── Useless / cat facts ──────────────────────────────────────────────────────

type factModule struct{}

func (factModule) Info() Info {
	return Info{
		ID: "fact", Name: "Fun fact", Category: CatFun, DefaultEnabled: false, Source: "uselessfacts.jsph.pl / catfact.ninja",
		Description: "A random fact. Choose general trivia or cat facts.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Did you know?"},
			{Key: "kind", Label: "Kind", Type: FieldSelect, Options: []string{"general", "cats"}, Default: "general"},
		},
	}
}

func (factModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	var text string
	if opt.Str("kind", "general") == "cats" {
		var res struct {
			Fact string `json:"fact"`
		}
		if err := httpx.GetJSON(ctx, "https://catfact.ninja/fact?max_length=200", nil, &res); err != nil {
			return nil, fmt.Errorf("catfact: %w", err)
		}
		text = res.Fact
	} else {
		var res struct {
			Text string `json:"text"`
		}
		if err := httpx.GetJSON(ctx, "https://uselessfacts.jsph.pl/api/v2/facts/random?language=en", nil, &res); err != nil {
			return nil, fmt.Errorf("uselessfacts: %w", err)
		}
		text = res.Text
	}
	return quoteSection("fact", opt.Str("title", "Did you know?"), text, "")
}

// ── Trivia (Open Trivia DB) ──────────────────────────────────────────────────

type triviaModule struct{}

var triviaTpl = Tpl("trivia", `<div class="h">{{.Title}}</div><div class="t">{{.Q}}</div>{{if .Choices}}<div class="s" style="margin-top:3px">{{range .Choices}}&#9633; {{.}}&nbsp; {{end}}</div>{{end}}<div class="s flip">Answer: {{.A}}</div>`)

func (triviaModule) Info() Info {
	return Info{
		ID: "trivia", Name: "Trivia question", Category: CatFun, DefaultEnabled: false, Source: "opentdb.com",
		Description: "A multiple-choice trivia question with the answer printed upside down.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Trivia"},
			{Key: "difficulty", Label: "Difficulty", Type: FieldSelect, Options: []string{"any", "easy", "medium", "hard"}, Default: "any"},
			{Key: "showChoices", Label: "Show choices", Type: FieldBool, Default: true},
		},
	}
}

func (triviaModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	url := "https://opentdb.com/api.php?amount=1&type=multiple"
	if d := opt.Str("difficulty", "any"); d != "any" {
		url += "&difficulty=" + d
	}
	var res struct {
		Results []struct {
			Question  string   `json:"question"`
			Correct   string   `json:"correct_answer"`
			Incorrect []string `json:"incorrect_answers"`
		} `json:"results"`
	}
	if err := httpx.GetJSON(ctx, url, nil, &res); err != nil {
		return nil, fmt.Errorf("opentdb: %w", err)
	}
	if len(res.Results) == 0 {
		return EmptySection("trivia"), nil
	}
	r := res.Results[0]
	q := html.UnescapeString(r.Question)
	a := html.UnescapeString(r.Correct)
	var choices []string
	if opt.Bool("showChoices", true) {
		all := append([]string{r.Correct}, r.Incorrect...)
		// Deterministic shuffle by day so the answer is not always first.
		seed := env.DaySeed()
		for i := len(all) - 1; i > 0; i-- {
			j := (seed*7 + i*13) % (i + 1)
			all[i], all[j] = all[j], all[i]
		}
		for _, c := range all {
			choices = append(choices, html.UnescapeString(c))
		}
	}
	est := 20 + LinesPx(q, 32, 13) + 12
	if len(choices) > 0 {
		est += LinesPx(strings.Join(choices, "   "), 40, 10) + 4
	}
	return Exec("trivia", triviaTpl, struct {
		Title, Q, A string
		Choices     []string
	}{opt.Str("title", "Trivia"), q, a, choices}, est)
}

// ── Word of the day ──────────────────────────────────────────────────────────

type wordModule struct{}

var wordTpl = Tpl("word", `<div class="h">{{.Title}}</div><div class="c"><span class="big">{{.Word}}</span>{{if .Phon}} <span class="s">{{.Phon}}</span>{{end}}</div><div class="t"><i>{{.POS}}</i> {{.Def}}</div>{{if .Example}}<div class="s">“{{.Example}}”</div>{{end}}`)

var wordList = []string{
	"serendipity", "ephemeral", "luminous", "meander", "quixotic", "resilient", "sonder", "petrichor", "halcyon", "mellifluous",
	"ebullient", "gossamer", "ineffable", "labyrinth", "nebulous", "obfuscate", "panacea", "quintessential", "reverie", "sanguine",
	"tenacious", "ubiquitous", "verdant", "wanderlust", "zenith", "aplomb", "bucolic", "candor", "dulcet", "eloquent",
	"fastidious", "garrulous", "hubris", "idyllic", "juxtapose", "kindle", "languid", "magnanimous", "nonchalant", "opulent",
	"pragmatic", "querulous", "ruminate", "surreptitious", "tranquil", "umbrage", "vociferous", "whimsical", "yonder", "zephyr",
	"alacrity", "benevolent", "cacophony", "diligent", "effervescent", "furtive", "gregarious", "harbinger", "incandescent", "jubilant",
	"kaleidoscope", "lackadaisical", "meticulous", "nostalgia", "onerous", "perspicacious", "quandary", "resplendent", "scintillating", "taciturn",
	"unfettered", "vivacious", "wistful", "exuberant", "yearn", "zealous", "ameliorate", "brevity", "copious", "demure",
	"enigma", "flabbergasted", "grandiose", "hapless", "impetuous", "jovial", "keen", "loquacious", "myriad", "nimble",
	"ostentatious", "placid", "quaint", "rambunctious", "serene", "trepidation", "undulate", "voracious", "winsome", "audacious",
}

func (wordModule) Info() Info {
	return Info{
		ID: "word", Name: "Word of the day", Category: CatFun, DefaultEnabled: false, Source: "dictionaryapi.dev",
		Description: "An interesting word with its definition and an example sentence.",
		Fields:      []Field{{Key: "title", Label: "Heading", Type: FieldText, Default: "Word of the day"}},
	}
}

func (wordModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	word := wordList[env.DaySeed()%len(wordList)]
	var res []struct {
		Word     string `json:"word"`
		Phonetic string `json:"phonetic"`
		Meanings []struct {
			PartOfSpeech string `json:"partOfSpeech"`
			Definitions  []struct {
				Definition string `json:"definition"`
				Example    string `json:"example"`
			} `json:"definitions"`
		} `json:"meanings"`
	}
	if err := httpx.GetJSON(ctx, "https://api.dictionaryapi.dev/api/v2/entries/en/"+word, nil, &res); err != nil {
		return nil, fmt.Errorf("dictionaryapi: %w", err)
	}
	if len(res) == 0 || len(res[0].Meanings) == 0 || len(res[0].Meanings[0].Definitions) == 0 {
		return EmptySection("word"), nil
	}
	m := res[0].Meanings[0]
	d := m.Definitions[0]
	data := struct{ Title, Word, Phon, POS, Def, Example string }{
		opt.Str("title", "Word of the day"), word, res[0].Phonetic, m.PartOfSpeech, d.Definition, d.Example,
	}
	return Exec("word", wordTpl, data, 20+18+LinesPx(d.Definition, 32, 13)+LinesPx(d.Example, 36, 10)+6)
}

func init() {
	Register(jokeModule{})
	Register(dadJokeModule{})
	Register(quoteModule{})
	Register(adviceModule{})
	Register(factModule{})
	Register(triviaModule{})
	Register(wordModule{})
}
