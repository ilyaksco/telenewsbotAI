package bot

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"news-bot/internal/news_fetcher"
	"news-bot/internal/storage"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *TelegramBot) newsFetchingJob() {
	log.Println("Scheduler fired: Fetching news...")
	b.fetchAndPostNews(context.Background())
}

func (b *TelegramBot) scheduleNewsFetching() {
	b.configMutex.RLock()
	interval := b.cfg.ScheduleIntervalMinutes
	limit := b.cfg.PostLimitPerRun
	b.configMutex.RUnlock()
	log.Printf("Scheduling news fetching job. Interval: %d minutes, Post Limit: %d", interval, limit)
	jobInterval := time.Duration(interval) * time.Minute
	b.scheduler.AddJob(newsFetchingJobTag, jobInterval, b.newsFetchingJob)
}

func (b *TelegramBot) fetchAndPostNews(ctx context.Context) {
	sources, err := b.storage.GetNewsSources()
	if err != nil {
		log.Printf("Error getting sources from DB: %v", err)
		return
	}
	log.Println("Discovering article links from configured sources...")
	discoveredArticles, err := b.fetcher.DiscoverArticles(sources)
	if err != nil {
		log.Printf("Error discovering articles: %v", err)
		return
	}
	log.Printf("Found %d total article links. Checking against %d known posts...", len(discoveredArticles), len(b.postedArticles))
	b.configMutex.RLock()
	postLimit := b.cfg.PostLimitPerRun
	approvalEnabled := b.cfg.EnableApprovalSystem
	b.configMutex.RUnlock()
	postsCount := 0
	for _, articleStub := range discoveredArticles {
		if postsCount >= postLimit {
			log.Printf("Post limit of %d reached for this run. Stopping.", postLimit)
			break
		}
		if articleStub.Link == "" {
			continue
		}
		if b.postedArticles[articleStub.Link] {
			continue
		}
		isPending, err := b.storage.IsArticlePending(articleStub.Link)
		if err != nil {
			log.Printf("Error checking if article is pending: %v", err)
			continue
		}
		if isPending {
			continue
		}

		log.Printf("Found new article link: %s. Scraping full content...", articleStub.Link)
		fullArticle, err := b.fetcher.ScrapeArticleDetails(articleStub.Link)
		if err != nil {
			log.Printf("Could not scrape article '%s': %v", articleStub.Link, err)
			b.postedArticles[articleStub.Link] = true
			continue
		}
		summary, err := b.summarizer.Summarize(ctx, fullArticle.TextContent)
		if err != nil {
			log.Printf("Could not summarize article '%s': %v", fullArticle.Title, err)
			continue
		}

		if approvalEnabled {
			err = b.sendArticleToModeration(fullArticle, summary, articleStub.Source)
			if err != nil {
				log.Printf("Failed to send article to moderation '%s': %v", fullArticle.Title, err)
				continue
			}
		} else {
			err = b.sendArticleToChannel(fullArticle, summary, articleStub.Source)
			if err != nil {
				log.Printf("Failed to send article '%s', it will be retried next cycle: %v", fullArticle.Title, err)
				continue
			}
			err = b.storage.MarkAsPosted(fullArticle.Link)
			if err != nil {
				log.Printf("CRITICAL: Failed to mark article as posted in DB: %v", err)
			}
		}

		b.postedArticles[fullArticle.Link] = true
		postsCount++
		time.Sleep(5 * time.Second)
	}
}

func (b *TelegramBot) sendArticleToChannel(article *news_fetcher.Article, summary string, source news_fetcher.Source) error {
	caption := b.formatCaption(article, summary, source)
	b.configMutex.RLock()
	chatIDStr := b.cfg.TelegramChatID
	b.configMutex.RUnlock()
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		log.Printf("Invalid TelegramChatID. It must be a number. Value: %s", chatIDStr)
		return err
	}
	if article.ImageURL == "" {
		msg := tgbotapi.NewMessage(chatID, caption)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = false
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send text message: %v", err)
			return err
		}
	} else {
		photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(article.ImageURL))
		photoMsg.Caption = caption
		photoMsg.ParseMode = tgbotapi.ModeHTML
		if _, err := b.api.Send(photoMsg); err != nil {
			log.Printf("Failed to send photo message: %v. Trying to send as text.", err)
			msg := tgbotapi.NewMessage(chatID, caption)
			msg.ParseMode = tgbotapi.ModeHTML
			msg.DisableWebPagePreview = false
			if _, err_text := b.api.Send(msg); err_text != nil {
				log.Printf("Failed to send message as text either: %v", err_text)
				return err_text
			}
		}
	}
	log.Printf("Successfully posted article to channel: %s", article.Title)
	return nil
}

func (b *TelegramBot) formatCaption(article *news_fetcher.Article, summary string, source news_fetcher.Source) string {
	b.configMutex.RLock()
	template := b.cfg.TelegramMessageTemplate
	b.configMutex.RUnlock()

	topicName := source.TopicName
	if topicName == "" {
		topicName = "General"
	}

	sourceURL, err := url.Parse(source.URL)
	sourceName := ""
	if err == nil {
		sourceName = sourceURL.Hostname()
		sourceName = strings.TrimPrefix(sourceName, "www.")
	}

	currentDate := time.Now().Format("January 2, 2006")

	templateReplacer := strings.NewReplacer(
		"{title}", article.Title,
		"{summary}", summary,
		"{link}", article.Link,
		"{description}", article.Description,
		"{topic_name}", topicName,
		"{source_name}", sourceName,
		"{date}", currentDate,
	)

	return templateReplacer.Replace(template)
}

func (b *TelegramBot) sendArticleToModeration(article *news_fetcher.Article, summary string, source news_fetcher.Source) error {
	lang := b.getLang()
	sourceURL, _ := url.Parse(source.URL)
	sourceName := strings.TrimPrefix(sourceURL.Hostname(), "www.")
	topicName := source.TopicName
	if topicName == "" {
		topicName = "General"
	}
	
	pendingArticle := storage.PendingArticle{
		Title:     article.Title,
		Summary:   summary,
		Link:      article.Link,
		ImageURL:  article.ImageURL,
		TopicName: topicName,
		SourceName: sourceName,
	}

	pendingID, err := b.storage.AddPendingArticle(pendingArticle)
	if err != nil {
		return fmt.Errorf("failed to add pending article to db: %w", err)
	}

	b.configMutex.RLock()
	approvalChatID := b.cfg.ApprovalChatID
	if approvalChatID == 0 {
		approvalChatID = b.cfg.SuperAdminID
	}
	b.configMutex.RUnlock()

	caption := b.formatCaption(article, summary, source)
	
	moderationText := fmt.Sprintf("%s\n\n%s", b.localizer.GetMessage(lang, "approval_header"), caption)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_approve"), fmt.Sprintf("approve_article:%d", pendingID)),
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit"), fmt.Sprintf("edit_article:%d", pendingID)),
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_reject"), fmt.Sprintf("reject_article:%d", pendingID)),
		),
	)

	msg := tgbotapi.NewMessage(approvalChatID, moderationText)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &keyboard
	
	if _, err := b.api.Send(msg); err != nil {
		return fmt.Errorf("failed to send moderation notification: %w", err)
	}

	log.Printf("Article '%s' sent for moderation.", article.Title)
	return nil
}