package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"news-bot/config"
	"news-bot/internal/news_fetcher"
	"news-bot/internal/storage"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *TelegramBot) scheduleGlobalNewsFetching() {
	interval := time.Duration(b.globalCfg.GlobalScheduleMinutes) * time.Minute
	log.Printf("Scheduling global news fetching job. Interval: %v", interval)
	b.scheduler.AddJob(newsFetchingJobTag, interval, b.globalNewsFetchingJob)
}

func (b *TelegramBot) globalNewsFetchingJob() {
	log.Println("Global scheduler fired: Fetching news for all chats...")
	go b.fetchAndPostNews(context.Background())
}

func (b *TelegramBot) fetchAndPostNews(parentCtx context.Context, notifyChatID ...int64) {
	b.fetchingMutex.Lock()
	if b.isFetching {
		log.Println("Global fetch process trigger ignored: another process is already running.")
		b.fetchingMutex.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)
	b.isFetching = true
	b.cancelFunc = cancel
	b.fetchingMutex.Unlock()

	defer func() {
		b.fetchingMutex.Lock()
		b.isFetching = false
		b.cancelFunc = nil
		b.fetchingMutex.Unlock()

		lang := "en"
		if errors.Is(ctx.Err(), context.Canceled) {
			log.Println("News fetching process was stopped by user.")
			if len(notifyChatID) > 0 {
				msg := tgbotapi.NewMessage(notifyChatID[0], b.localizer.GetMessage(lang, "fetch_stop_success"))
				b.api.Send(msg)
			}
		} else {
			log.Println("Global news fetching process finished.")
			if len(notifyChatID) > 0 {
				msg := tgbotapi.NewMessage(notifyChatID[0], b.localizer.GetMessage(lang, "fetch_now_completed"))
				b.api.Send(msg)
			}
		}
	}()

	log.Println("Starting global news fetching process...")

	// 1. Get all sources from all chats
	allSources, err := b.storage.GetAllNewsSources()
	if err != nil {
		log.Printf("Error getting all sources from DB: %v", err)
		return
	}
	if len(allSources) == 0 {
		log.Println("No news sources configured in any chat. Skipping fetch cycle.")
		return
	}

	// 2. Discover articles from all sources
	// We use a fixed max age for the global fetcher for now.
	discoveredArticles, err := b.fetcher.DiscoverArticles(allSources, 24)
	if err != nil {
		log.Printf("Error discovering articles: %v", err)
		return
	}
	log.Printf("Discovered %d total article links from all sources.", len(discoveredArticles))

	// 3. Process each article with its chat-specific context
	for _, articleStub := range discoveredArticles {
		select {
		case <-ctx.Done():
			return // Exit if context is cancelled
		default:
		}

		chatID := articleStub.Source.ChatID

		// Check if article was already posted or is pending for this specific chat
		posted, _ := b.storage.IsAlreadyPosted(articleStub.Link, chatID)
		pending, _ := b.storage.IsArticlePending(articleStub.Link, chatID)
		if posted || pending {
			continue
		}
		
		// Get chat-specific configuration
		chatCfg, err := b.storage.GetChatConfig(chatID)
		if err != nil {
			log.Printf("Could not get config for chat %d, skipping article from %s. Error: %v", chatID, articleStub.Source.URL, err)
			continue
		}

		// Check post limit for this chat (optional, complex to implement perfectly in a global fetcher, skipping for now)

		log.Printf("Found new article for chat %d: %s. Scraping...", chatID, articleStub.Link)
		fullArticle, err := b.fetcher.ScrapeArticleDetails(articleStub.Link)
		if err != nil {
			log.Printf("[Chat %d] Could not scrape article '%s': %v", chatID, articleStub.Link, err)
			b.storage.MarkAsPosted(articleStub.Link, chatID) // Mark as posted to avoid retrying a broken link
			continue
		}
		fullArticle.PublicationTime = articleStub.PubDate

		summarizer, err := b.getSummarizerForChat(chatCfg)
		if err != nil {
			log.Printf("[Chat %d] Could not get summarizer: %v", chatID, err)
			continue
		}
		
		summary, err := summarizer.Summarize(ctx, fullArticle.TextContent)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("[Chat %d] Could not summarize article '%s': %v", chatID, fullArticle.Title, err)
			}
			continue
		}

		if chatCfg.EnableApprovalSystem {
			err = b.sendArticleToModeration(fullArticle, summary, articleStub.Source, chatCfg)
			if err != nil {
				log.Printf("[Chat %d] Failed to send article to moderation '%s': %v", chatID, fullArticle.Title, err)
				continue
			}
		} else {
			err = b.sendArticleToChannel(fullArticle, summary, articleStub.Source, chatCfg)
			if err != nil {
				log.Printf("[Chat %d] Failed to send article '%s', it will be retried next cycle: %v", chatID, fullArticle.Title, err)
				continue
			}
			b.storage.MarkAsPosted(fullArticle.Link, chatID)
		}

		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

func (b *TelegramBot) sendArticleToChannel(article *news_fetcher.Article, summary string, source news_fetcher.Source, chatCfg *config.Config) error {
	caption := b.formatCaption(article, summary, source, chatCfg)

	// If a specific destination topic is set, use it.
	chatID := source.DestinationChatID
	replyToID := int(source.ReplyToMessageID)

	// Otherwise, fallback to the chat where the source was configured.
	if chatID == 0 {
		chatID = source.ChatID
	}

	if article.ImageURL == "" {
		msg := tgbotapi.NewMessage(chatID, caption)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = false
		if replyToID != 0 {
			msg.ReplyToMessageID = replyToID
		}
		if _, err := b.api.Send(msg); err != nil {
			return fmt.Errorf("failed to send text message: %w", err)
		}
	} else {
		photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(article.ImageURL))
		photoMsg.Caption = caption
		photoMsg.ParseMode = tgbotapi.ModeHTML
		if replyToID != 0 {
			photoMsg.ReplyToMessageID = replyToID
		}
		if _, err := b.api.Send(photoMsg); err != nil {
			log.Printf("Failed to send photo message for chat %d: %v. Trying as text.", chatID, err)
			msg := tgbotapi.NewMessage(chatID, caption)
			msg.ParseMode = tgbotapi.ModeHTML
			msg.DisableWebPagePreview = false
			if replyToID != 0 {
				msg.ReplyToMessageID = replyToID
			}
			if _, err_text := b.api.Send(msg); err_text != nil {
				return fmt.Errorf("failed to send message as text either: %w", err_text)
			}
		}
	}
	log.Printf("Successfully posted article to channel for chat %d: %s", source.ChatID, article.Title)
	return nil
}

func (b *TelegramBot) formatCaption(article *news_fetcher.Article, summary string, source news_fetcher.Source, chatCfg *config.Config) string {
	template := chatCfg.TelegramMessageTemplate

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
	publishDate := "N/A"
	publishTime := "N/A"

	if article.PublicationTime != nil {
		publishDate = article.PublicationTime.Format("2 January 2006")
		publishTime = article.PublicationTime.Format("15:04 WIB")
	}

	templateReplacer := strings.NewReplacer(
		"{title}", article.Title,
		"{summary}", summary,
		"{link}", article.Link,
		"{description}", article.Description,
		"{topic_name}", topicName,
		"{source_name}", sourceName,
		"{date}", currentDate,
		"{publish_date}", publishDate,
		"{publish_time}", publishTime,
	)
	return templateReplacer.Replace(template)
}

func (b *TelegramBot) sendArticleToModeration(article *news_fetcher.Article, summary string, source news_fetcher.Source, chatCfg *config.Config) error {
	lang := "en"
	sourceURL, _ := url.Parse(source.URL)
	sourceName := strings.TrimPrefix(sourceURL.Hostname(), "www.")
	topicName := source.TopicName
	if topicName == "" {
		topicName = "General"
	}

	pendingArticle := storage.PendingArticle{
		ChatID:     source.ChatID,
		Title:      article.Title,
		Summary:    summary,
		Link:       article.Link,
		ImageURL:   article.ImageURL,
		TopicName:  topicName,
		SourceName: sourceName,
	}

	pendingID, err := b.storage.AddPendingArticle(source.ChatID, pendingArticle)
	if err != nil {
		return fmt.Errorf("failed to add pending article to db: %w", err)
	}

	approvalChatID := chatCfg.ApprovalChatID
	if approvalChatID == 0 {
		approvalChatID = source.ChatID
	}

	caption := b.formatCaption(article, summary, source, chatCfg)
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
	log.Printf("Article '%s' for chat %d sent for moderation.", article.Title, source.ChatID)
	return nil
}