package main

import (
	"fmt"
	"strings"

	"github.com/gocolly/colly"
)

// nytArticle represents a scraped NYT article.
type nytArticle struct {
	Title string
	Link  string
}

// getNYTArticles scrapes the NY Times main section from upstract.com.
func getNYTArticles() ([]nytArticle, error) {
	var articles []nytArticle
	var scrapeErr error

	c := colly.NewCollector()

	c.OnHTML("#s_nyt_main ul li", func(e *colly.HTMLElement) {
		a := e.DOM.Find("a").First()
		if a.HasClass("lmr") {
			return
		}
		href, _ := a.Attr("href")
		if href == "#" || href == "" {
			return
		}
		if strings.HasPrefix(href, "/") {
			href = "https://upstract.com" + href
		}
		title := strings.TrimSpace(a.Text())

		if len(title) > 0 {
			articles = append(articles, nytArticle{
				Title: title,
				Link:  href,
			})
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		scrapeErr = fmt.Errorf("scraping NYT from upstract: %w", err)
	})

	if err := c.Visit("https://upstract.com/"); err != nil {
		return nil, fmt.Errorf("visiting upstract: %w", err)
	}

	if scrapeErr != nil {
		return nil, scrapeErr
	}

	return articles, nil
}
