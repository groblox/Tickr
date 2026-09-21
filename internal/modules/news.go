package modules

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"tickr/internal/httpx"

	"github.com/PuerkitoBio/goquery"
)

type headline struct {
	Title string
	Meta  string
}

var listTpl = Tpl("headlines", `<div class="h">{{.Title}}</div>
<ul class="news">{{range .Items}}<li>{{.Title}}{{if .Meta}} <span class="s">{{.Meta}}</span>{{end}}</li>{{end}}</ul>`)

func headlinesSection(id, title string, items []headline) (*Section, error) {
	if len(items) == 0 {
		return EmptySection(id), nil
	}
	est := 24
	for _, it := range items {
		text := it.Title
		if it.Meta != "" {
			text += " " + it.Meta
		}
		est += LinesPx(text, 36, 13) + 5
	}
	return Exec(id, listTpl, struct {
		Title string
		Items []headline
	}{title, items}, est)
}

// ── New York Times (via upstract.com digest) ─────────────────────────────────

type nytModule struct{}

func (nytModule) Info() Info {
	return Info{
		ID: "news_nyt", Name: "NY Times headlines", Category: CatNews, DefaultEnabled: true, Source: "upstract.com",
		Description: "Top New York Times headlines, scraped from the upstract.com digest.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "NY Times"},
			{Key: "count", Label: "Headlines", Type: FieldNumber, Default: 5, Min: F64(1), Max: F64(15)},
			{Key: "showSummary", Label: "Show summary sentence", Type: FieldBool, Default: true, Help: "The 1–2 sentence excerpt upstract.com shows under each headline."},
		},
	}
}

func (nytModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	resp, err := httpx.Do(ctx, http.MethodGet, "https://upstract.com/", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("upstract: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstract: status %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("upstract: %w", err)
	}
	showSummary := opt.Bool("showSummary", true)
	var items []headline
	doc.Find("#s_nyt_main ul li").Each(func(_ int, s *goquery.Selection) {
		a := s.Find("a").First()
		if a.HasClass("lmr") {
			return
		}
		href, _ := a.Attr("href")
		if href == "#" || href == "" {
			return
		}
		if t := strings.TrimSpace(a.Text()); t != "" {
			h := headline{Title: t}
			if showSummary {
				h.Meta = strings.TrimSpace(a.AttrOr("data-p", ""))
			}
			items = append(items, h)
		}
	})
	if n := opt.Int("count", 5); len(items) > n {
		items = items[:n]
	}
	return headlinesSection("news_nyt", opt.Str("title", "NY Times"), items)
}

// ── Hacker News (official Firebase API) ──────────────────────────────────────

type hnModule struct{}

func (hnModule) Info() Info {
	return Info{
		ID: "news_hn", Name: "Hacker News", Category: CatNews, DefaultEnabled: false, Source: "news.ycombinator.com",
		Description: "Top stories from Hacker News with points and comment counts.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "Hacker News"},
			{Key: "count", Label: "Stories", Type: FieldNumber, Default: 5, Min: F64(1), Max: F64(15)},
			{Key: "feed", Label: "Feed", Type: FieldSelect, Options: []string{"top", "best", "new"}, Default: "top"},
			{Key: "showMeta", Label: "Show points and domain", Type: FieldBool, Default: true},
		},
	}
}

func (hnModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	var ids []int
	feed := opt.Str("feed", "top")
	if err := httpx.GetJSON(ctx, fmt.Sprintf("https://hacker-news.firebaseio.com/v0/%sstories.json", feed), nil, &ids); err != nil {
		return nil, fmt.Errorf("hacker news: %w", err)
	}
	n := opt.Int("count", 5)
	if len(ids) > n {
		ids = ids[:n]
	}
	type story struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Score       int    `json:"score"`
		Descendants int    `json:"descendants"`
	}
	stories := make([]story, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i, id int) {
			defer wg.Done()
			_ = httpx.GetJSON(ctx, fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%d.json", id), nil, &stories[i])
		}(i, id)
	}
	wg.Wait()
	var items []headline
	for _, s := range stories {
		if s.Title == "" {
			continue
		}
		h := headline{Title: s.Title}
		if opt.Bool("showMeta", true) {
			h.Meta = fmt.Sprintf("%d pts · %d comments", s.Score, s.Descendants)
			if host := hostOf(s.URL); host != "" {
				h.Meta += " · " + host
			}
		}
		items = append(items, h)
	}
	return headlinesSection("news_hn", opt.Str("title", "Hacker News"), items)
}

func hostOf(raw string) string {
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	raw = strings.TrimPrefix(raw, "www.")
	if i := strings.Index(raw, "/"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// ── Wikipedia: On this day ───────────────────────────────────────────────────

type onThisDayModule struct{}

func (onThisDayModule) Info() Info {
	return Info{
		ID: "onthisday", Name: "On this day", Category: CatNews, DefaultEnabled: false, Source: "Wikipedia",
		Description: "Historical events that happened on today's date, from Wikipedia.",
		Fields: []Field{
			{Key: "title", Label: "Heading", Type: FieldText, Default: "On this day"},
			{Key: "count", Label: "Events", Type: FieldNumber, Default: 3, Min: F64(1), Max: F64(8)},
			{Key: "kind", Label: "Type", Type: FieldSelect, Options: []string{"events", "births", "deaths", "holidays", "selected"}, Default: "selected"},
		},
	}
}

func (onThisDayModule) Render(ctx context.Context, env *Env, opt Options) (*Section, error) {
	kind := opt.Str("kind", "selected")
	url := fmt.Sprintf("https://en.wikipedia.org/api/rest_v1/feed/onthisday/%s/%02d/%02d", kind, int(env.Now.Month()), env.Now.Day())
	var resp map[string][]struct {
		Text string `json:"text"`
		Year int    `json:"year"`
	}
	if err := httpx.GetJSON(ctx, url, nil, &resp); err != nil {
		return nil, fmt.Errorf("wikipedia: %w", err)
	}
	entries := resp[kind]
	n := opt.Int("count", 3)
	// Pick a stable spread across the list so it is not always the same era.
	var items []headline
	if len(entries) > 0 {
		step := len(entries) / n
		if step < 1 {
			step = 1
		}
		offset := env.DaySeed() % step
		for i := offset; i < len(entries) && len(items) < n; i += step {
			e := entries[i]
			h := headline{Title: e.Text}
			if e.Year != 0 {
				h.Title = fmt.Sprintf("%d — %s", e.Year, e.Text)
			}
			items = append(items, h)
		}
	}
	return headlinesSection("onthisday", opt.Str("title", "On this day"), items)
}

func init() {
	Register(nytModule{})
	Register(hnModule{})
	Register(onThisDayModule{})
}
