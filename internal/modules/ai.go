package modules

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"breaklist/internal/connectors"
)

// ── Ask an AI ────────────────────────────────────────────────────────────────

type aiModule struct{}

const defaultAISystem = "You write a short note for a family's morning receipt-printer briefing. " +
	"Plain text only: no markdown, no headings, no emoji. Keep it under 70 words."

const defaultAIPrompt = "Today is {weekday}, {date}. The weather: {weather}. " +
	"Write a warm, slightly witty two-sentence good-morning note for {name}, then one small, concrete suggestion for the day."

func (aiModule) Info() Info {
	return Info{
		ID: "ai", Name: "Ask an AI", Category: CatFun, DefaultEnabled: false, Needs: []string{"ai"},
		Description: "Sends a prompt to Claude or an OpenAI-compatible model and prints the reply. Use it for a daily note, a riddle, a haiku about the weather, a toddler story, anything.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "A note for today"},
			{Key: "provider", Label: "Provider", Type: FieldSelect, Options: []string{"anthropic", "openai"}, Default: "anthropic", Help: "Keys live on the Connectors tab (or ANTHROPIC_API_KEY / OPENAI_API_KEY in the environment)."},
			{Key: "model", Label: "Model", Type: FieldText, Default: "", Help: "Blank = claude-sonnet-5 (Anthropic) or gpt-4o-mini (OpenAI). Other ideas: claude-haiku-4-5-20251001, claude-opus-5, gpt-4o."},
			{Key: "system", Label: "System prompt", Type: FieldTextarea, Default: defaultAISystem},
			{Key: "prompt", Label: "Prompt", Type: FieldTextarea, Default: defaultAIPrompt,
				Help: "Placeholders: {date} {weekday} {name} {location} {weather} {season} {seed}. {weather} is today's high/low and conditions."},
			{Key: "maxTokens", Label: "Max tokens", Type: FieldNumber, Default: 300, Min: F64(20), Max: F64(4000)},
			{Key: "temperature", Label: "Temperature", Type: FieldNumber, Default: 1, Min: F64(0), Max: F64(2), Help: "0 = predictable, 1 = default, higher = wilder (OpenAI allows up to 2)."},
			{Key: "cache", Label: "One reply per day", Type: FieldBool, Default: true, Help: "Reuses today's reply on regenerate/preview so you are not billed for every run."},
			{Key: "align", Label: "Alignment", Type: FieldSelect, Options: []string{"left", "center"}, Default: "left"},
		},
	}
}

var aiCacheMu sync.Mutex

func (aiModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	provider := opt.Str("provider", connectors.ProviderAnthropic)
	prompt := strings.TrimSpace(opt.Str("prompt", defaultAIPrompt))
	if prompt == "" {
		return EmptySection("ai"), nil
	}
	prompt = expandAIPlaceholders(ctx, env, prompt)
	system := expandAIPlaceholders(ctx, env, strings.TrimSpace(opt.Str("system", defaultAISystem)))
	req := connectors.AIRequest{
		Provider: provider, Model: strings.TrimSpace(opt.Str("model", "")), System: system, Prompt: prompt,
		MaxTokens: opt.Int("maxTokens", 300), Temperature: floatOpt(opt, "temperature", 1),
	}
	key := fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("%d|%s|%s|%s|%s|%d|%.2f", env.DaySeed(), provider, req.Model, system, prompt, req.MaxTokens, req.Temperature))))
	cachePath := filepath.Join(env.DataDir, "output", "ai-cache.json")
	var text string
	if opt.Bool("cache", true) {
		if cached, ok := readAICache(cachePath, key); ok {
			text = cached
			env.Log("ai: reused today's reply")
		}
	}
	if text == "" {
		out, err := connectors.Complete(ctx, env.Cfg.Connectors.AI, req)
		if err != nil {
			return nil, err
		}
		text = out
		if opt.Bool("cache", true) {
			writeAICache(cachePath, key, text)
		}
	}
	if text == "" {
		return EmptySection("ai"), nil
	}
	align := ""
	if opt.Str("align", "left") == "center" {
		align = "c"
	}
	return Exec("ai", customTpl, struct{ Title, Text, Align string }{opt.Str("title", "A note for today"), text, align}, 20+LinesPx(text, 32, 14)+8)
}

// expandAIPlaceholders fills {date}-style tokens; {weather} costs a forecast fetch only when used.
func expandAIPlaceholders(ctx context.Context, env *Env, s string) string {
	name := childName(env)
	if name == "" {
		name = "the family"
	}
	r := strings.NewReplacer(
		"{date}", env.Now.Format("January 2, 2006"),
		"{weekday}", env.Now.Weekday().String(),
		"{name}", name,
		"{location}", env.Cfg.General.LocationName,
		"{season}", seasonName(env),
		"{seed}", fmt.Sprint(env.DaySeed()),
	)
	s = r.Replace(s)
	if strings.Contains(s, "{weather}") {
		s = strings.ReplaceAll(s, "{weather}", weatherSummary(ctx, env))
	}
	return s
}

func weatherSummary(ctx context.Context, env *Env) string {
	fc, err := env.Forecast(ctx)
	if err != nil || fc == nil {
		return "unknown"
	}
	for _, d := range fc.Daily {
		if d.Date.YearDay() == env.Now.YearDay() && d.Date.Year() == env.Now.Year() {
			return fmt.Sprintf("high %s, low %s, %s, %d%% chance of rain", env.Temp(d.MaxC), env.Temp(d.MinC), connectors.Describe(d.Code), d.PrecipProb)
		}
	}
	if len(fc.Daily) > 0 {
		d := fc.Daily[0]
		return fmt.Sprintf("high %s, low %s, %s", env.Temp(d.MaxC), env.Temp(d.MinC), connectors.Describe(d.Code))
	}
	return "unknown"
}

func seasonName(env *Env) string {
	lat, _, ok := env.Cfg.LatLng()
	north := !ok || lat >= 0
	m := env.Now.Month()
	var s string
	switch {
	case m >= 3 && m <= 5:
		s = "spring"
	case m >= 6 && m <= 8:
		s = "summer"
	case m >= 9 && m <= 11:
		s = "autumn"
	default:
		s = "winter"
	}
	if !north {
		s = map[string]string{"spring": "autumn", "summer": "winter", "autumn": "spring", "winter": "summer"}[s]
	}
	return s
}

func floatOpt(o Options, key string, def float64) float64 {
	if v, ok := o[key]; ok {
		switch t := v.(type) {
		case float64:
			return t
		case int:
			return float64(t)
		}
	}
	return def
}

func readAICache(path, key string) (string, bool) {
	aiCacheMu.Lock()
	defer aiCacheMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return "", false
	}
	v, ok := m[key]
	return v, ok
}

func writeAICache(path, key, text string) {
	aiCacheMu.Lock()
	defer aiCacheMu.Unlock()
	m := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	// Keep the file small: only today's entries matter, so drop older ones
	// whenever it grows past a handful.
	if len(m) > 24 {
		m = map[string]string{}
	}
	m[key] = text
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, data, 0o600)
	}
}

func init() {
	Register(aiModule{})
}
