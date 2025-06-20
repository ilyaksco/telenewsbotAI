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

func (b *TelegramBot) scheduleNewsDispatcher() {
	interval := 1 * time.Minute
	log.Printf("Scheduling news dispatcher job. Interval: %v", interval)
	b.scheduler.AddJob(newsFetchingJobTag, interval, b.dispatchScheduledFetches)
}

func (b *TelegramBot) dispatchScheduledFetches() {
	allConfigs, err := b.storage.GetAllChatConfigs()
	if err != nil {
		log.Printf("Dispatcher: Failed to get all chat configs: %v", err)
		return
	}

	now := time.Now()
	for _, chatConfigWithID := range allConfigs {
		chatID := chatConfigWithID.ChatID
		chatCfg := chatConfigWithID.Config
		lastFetched := chatConfigWithID.LastFetchedAt

		nextFetchTime := lastFetched.Add(time.Duration(chatCfg.ScheduleIntervalMinutes) * time.Minute)

		if now.After(nextFetchTime) {
			log.Printf("Dispatcher: Chat %d is due for news fetch. Triggering now.", chatID)
			go b.fetchNewsForChat(b.ctx, chatID, false)
		}
	}
}

func (b *TelegramBot) fetchNewsForChat(parentCtx context.Context, chatID int64, manual bool) {
	b.fetchingMutex.Lock()
	if b.isFetching[chatID] {
		log.Printf("Fetch process for chat %d ignored: another process is already running for this chat.", chatID)
		b.fetchingMutex.Unlock()
		return
	}
	b.isFetching[chatID] = true
	b.fetchingMutex.Unlock()

	ctx, cancel := context.WithCancel(parentCtx)
	defer func() {
		b.fetchingMutex.Lock()
		delete(b.isFetching, chatID)
		if manual {
			b.cancelFunc = nil
		}
		b.fetchingMutex.Unlock()
		cancel()

		lang := b.getLangForChat(chatID)
		if manual {
			if errors.Is(ctx.Err(), context.Canceled) {
				log.Printf("Manual news fetching process for chat %d was stopped.", chatID)
				msg := tgbotapi.NewMessage(chatID, b.localizer.GetMessage(lang, "fetch_stop_success"))
				b.api.Send(msg)
			} else {
				log.Printf("Manual news fetching process for chat %d finished.", chatID)
				msg := tgbotapi.NewMessage(chatID, b.localizer.GetMessage(lang, "fetch_now_completed"))
				b.api.Send(msg)
			}
		} else {
			log.Printf("Scheduled news fetching for chat %d finished.", chatID)
		}
	}()

	if manual {
		b.fetchingMutex.Lock()
		b.cancelFunc = cancel
		b.fetchingMutex.Unlock()
	}

	log.Printf("Starting news fetching process for chat %d...", chatID)

	chatCfg, err := b.storage.GetChatConfig(chatID)
	if err != nil {
		log.Printf("[Chat %d] Could not get config, aborting fetch. Error: %v", chatID, err)
		return
	}

	sources, err := b.storage.GetNewsSourcesForChat(chatID)
	if err != nil {
		log.Printf("[Chat %d] Error getting sources from DB: %v", chatID, err)
		return
	}
	if len(sources) == 0 {
		log.Printf("[Chat %d] No news sources configured. Skipping fetch cycle.", chatID)
		if !manual {
			if err := b.storage.UpdateLastFetchedTime(chatID, time.Now()); err != nil {
				log.Printf("[Chat %d] Failed to update last fetched time even with no sources: %v", chatID, err)
			}
		}
		return
	}

	discoveredArticles, err := b.fetcher.DiscoverArticles(sources, chatCfg.RSSMaxAgeHours)
	if err != nil {
		log.Printf("[Chat %d] Error discovering articles: %v", chatID, err)
		return
	}
	log.Printf("[Chat %d] Discovered %d total article links.", chatID, len(discoveredArticles))

	postedCount := 0
	for _, articleStub := range discoveredArticles {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if postedCount >= chatCfg.PostLimitPerRun {
			log.Printf("[Chat %d] Post limit of %d reached for this run.", chatID, chatCfg.PostLimitPerRun)
			break
		}

		posted, _ := b.storage.IsAlreadyPosted(articleStub.Link, chatID)
		pending, _ := b.storage.IsArticlePending(articleStub.Link, chatID)
		if posted || pending {
			continue
		}

		log.Printf("[Chat %d] Found new article: %s. Scraping...", chatID, articleStub.Link)
		fullArticle, err := b.fetcher.ScrapeArticleDetails(articleStub.Link)
		if err != nil {
			log.Printf("[Chat %d] Could not scrape article '%s': %v", chatID, articleStub.Link, err)
			b.storage.MarkAsPosted(articleStub.Link, chatID)
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
		postedCount++

		select {
		case <-time.After(5 * time.Second):
		case <-ctx.Done():
			return
		}
	}

	if !manual {
		if err := b.storage.UpdateLastFetchedTime(chatID, time.Now()); err != nil {
			log.Printf("[Chat %d] Failed to update last fetched time after a successful run: %v", chatID, err)
		}
	}
}

func (b *TelegramBot) sendArticleToChannel(article *news_fetcher.Article, summary string, source news_fetcher.Source, chatCfg *config.Config) error {
	caption := b.formatCaption(article, summary, source, chatCfg)

	chatID := source.DestinationChatID
	replyToID := int(source.ReplyToMessageID)

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
	lang := b.getLangForChat(source.ChatID)
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