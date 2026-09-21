package main

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/gocolly/colly"
)

// hnArticle represents a Hacker News article with its metadata.
type hnArticle struct {
	Title   string
	Summary string
	Link    string
	Rank    int
	Votes   int
	Time    string
}

func formatDuration(duration time.Duration) string {
	if duration < 0 {
		duration = -duration
	}

	switch {
	case duration >= time.Hour:
		hours := duration / time.Hour
		return fmt.Sprintf("%d hours ago", hours)
	case duration >= time.Minute:
		minutes := duration / time.Minute
		return fmt.Sprintf("%d minutes ago", minutes)
	default:
		return "just now"
	}
}

// getHNArticles scrapes the top Hacker News articles from the hacker-news-digest
// site and returns them sorted by rank.
func getHNArticles() ([]hnArticle, error) {
	var articles []hnArticle
	var scrapeErr error

	c := colly.NewCollector()

	c.OnHTML("article", func(e *colly.HTMLElement) {
		articleTime, _ := time.Parse("2006-01-02 15:04:05 MST", e.ChildText("span .last-updated"))
		articleTimeFormatted := formatDuration(time.Since(articleTime))
		score, _ := strconv.Atoi(e.ChildText(".score"))
		rank, _ := strconv.Atoi(e.ChildText(".data-rank"))
		title := e.ChildText(".post-title")
		summary := e.ChildText(".post-summary")

		if len(summary) > 0 && len(title) > 0 {
			articles = append(articles, hnArticle{
				Title:   title,
				Summary: summary,
				Votes:   score,
				Link:    e.ChildText(".host"),
				Rank:    rank,
				Time:    articleTimeFormatted,
			})
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		scrapeErr = fmt.Errorf("scraping HN digest: %w", err)
	})

	if err := c.Visit("https://hackernews.betacat.io/"); err != nil {
		return nil, fmt.Errorf("visiting HN digest: %w", err)
	}

	if scrapeErr != nil {
		return nil, scrapeErr
	}

	sort.Slice(articles, func(i, j int) bool {
		return articles[i].Rank < articles[j].Rank
	})

	return articles, nil
}
