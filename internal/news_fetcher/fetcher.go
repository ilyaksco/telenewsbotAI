package news_fetcher

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-shiori/go-readability"
	"github.com/mmcdole/gofeed"
)

type Article struct {
	Title           string
	Link            string
	Description     string
	TextContent     string
	ImageURL        string
	PublicationTime *time.Time
}

type DiscoveredArticle struct {
	Link    string
	Source  Source
	PubDate *time.Time
}

type Source struct {
	ID           int64  `json:"id"`
	Type         string `json:"type"`
	URL          string `json:"url"`
	LinkSelector string `json:"link_selector,omitempty"`
	TopicID      int64  `json:"topic_id,omitempty"`
	TopicName    string `json:"topic_name,omitempty"`
}

type AnalyzedLink struct {
	Href        string
	Text        string
	Class       string
	ParentClass string
}

type Fetcher struct {
	parser *gofeed.Parser
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		parser: gofeed.NewParser(),
	}
}

func (f *Fetcher) DiscoverArticles(sources []Source, maxAgeHours int) ([]DiscoveredArticle, error) {
	var discoveredArticles []DiscoveredArticle
	for _, source := range sources {
		var articlesFromSource []DiscoveredArticle
		var err error

		switch source.Type {
		case "rss":
			articlesFromSource, err = f.fetchFromRSS(source, maxAgeHours)
		case "scrape":
			articlesFromSource, err = f.fetchFromHomepage(source)
		default:
			fmt.Printf("Warning: Unknown source type '%s' for URL %s\n", source.Type, source.URL)
			continue
		}

		if err != nil {
			fmt.Printf("Warning: Failed to fetch from source %s: %v\n", source.URL, err)
			continue
		}

		discoveredArticles = append(discoveredArticles, articlesFromSource...)
	}

	return discoveredArticles, nil
}

func (f *Fetcher) fetchFromRSS(source Source, maxAgeHours int) ([]DiscoveredArticle, error) {
	feed, err := f.parser.ParseURL(source.URL)
	if err != nil {
		return nil, err
	}
	var discoveredArticles []DiscoveredArticle
	now := time.Now()
	maxAge := time.Duration(maxAgeHours) * time.Hour

	for _, item := range feed.Items {
		if item.PublishedParsed == nil {
			continue
		}

		if now.Sub(*item.PublishedParsed) > maxAge {
			continue
		}

		discoveredArticles = append(discoveredArticles, DiscoveredArticle{
			Link:    item.Link,
			Source:  source,
			PubDate: item.PublishedParsed,
		})
	}
	return discoveredArticles, nil
}

func (f *Fetcher) fetchFromHomepage(source Source) ([]DiscoveredArticle, error) {
	res, err := http.Get(source.URL)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(source.URL)
	if err != nil {
		return nil, err
	}

	var discoveredArticles []DiscoveredArticle
	doc.Find(source.LinkSelector).Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if exists {
			u, err := url.Parse(href)
			if err == nil {
				discoveredArticles = append(discoveredArticles, DiscoveredArticle{
					Link:    base.ResolveReference(u).String(),
					Source:  source,
					PubDate: nil,
				})
			}
		}
	})
	return discoveredArticles, nil
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

func (f *Fetcher) AnalyzePageLinks(pageURL string) ([]AnalyzedLink, error) {
	res, err := http.Get(pageURL)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch page: status code %d", res.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		return nil, err
	}

	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}

	var links []AnalyzedLink
	doc.Find("a").Each(func(i int, s *goquery.Selection) {
		href, exists := s.Attr("href")
		if !exists {
			return
		}

		u, err := url.Parse(href)
		if err != nil {
			return
		}
		fullURL := base.ResolveReference(u).String()

		class, _ := s.Attr("class")
		parentClass, _ := s.Parent().Attr("class")

		links = append(links, AnalyzedLink{
			Href:        fullURL,
			Text:        strings.TrimSpace(s.Text()),
			Class:       class,
			ParentClass: parentClass,
		})
	})
	return links, nil
}