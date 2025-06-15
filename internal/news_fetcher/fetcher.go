package news_fetcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time" 

	"github.com/PuerkitoBio/goquery"
	"github.com/go-shiori/go-readability"
	"github.com/mmcdole/gofeed"
)


type Article struct {
	Title       string
	Link        string
	Description string 
	TextContent string 
	ImageURL    string
}


type Source struct {
	Type         string `json:"type"`
	URL          string `json:"url"`
	LinkSelector string `json:"link_selector,omitempty"`
}

type Fetcher struct {
	parser *gofeed.Parser
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		parser: gofeed.NewParser(),
	}
}

func (f *Fetcher) DiscoverArticles(sourcesJSON string) ([]*Article, error) {
	var sources []Source
	if err := json.Unmarshal([]byte(sourcesJSON), &sources); err != nil {
		return nil, fmt.Errorf("could not parse NEWS_SOURCES JSON: %w", err)
	}

	var discoveredLinks []string
	for _, source := range sources {
		var links []string
		var err error

		switch source.Type {
		case "rss":
			links, err = f.fetchFromRSS(source.URL)
		case "scrape":
			links, err = f.fetchFromHomepage(source.URL, source.LinkSelector)
		default:
			fmt.Printf("Warning: Unknown source type '%s' for URL %s\n", source.Type, source.URL)
			continue
		}

		if err != nil {
			fmt.Printf("Warning: Failed to fetch from source %s: %v\n", source.URL, err)
			continue
		}
		discoveredLinks = append(discoveredLinks, links...)
	}

	var articles []*Article
	for _, link := range discoveredLinks {
		articles = append(articles, &Article{Link: link})
	}

	return articles, nil
}

func (f *Fetcher) fetchFromRSS(url string) ([]string, error) {
	feed, err := f.parser.ParseURL(url)
	if err != nil {
		return nil, err
	}
	var links []string
	for _, item := range feed.Items {
		links = append(links, item.Link)
	}
	return links, nil
}

func (f *Fetcher) fetchFromHomepage(pageURL, linkSelector string) ([]string, error) {
	res, err := http.Get(pageURL)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}

	var links []string
	doc.Find(linkSelector).Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if exists {
			// Mengubah link relatif menjadi absolut
			u, err := url.Parse(href)
			if err == nil {
				links = append(links, base.ResolveReference(u).String())
			}
		}
	})
	return links, nil
}


func (f *Fetcher) ScrapeArticleDetails(link string) (*Article, error) {
	parsedURL, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("failed to parse link: %w", err)
	}

	article, err := readability.FromURL(parsedURL.String(), 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to process with readability: %w", err)
	}

	return &Article{
		Link:        link,
		Title:       article.Title,
		Description: article.Excerpt,
		TextContent: article.TextContent,
		ImageURL:    article.Image,
	}, nil
}