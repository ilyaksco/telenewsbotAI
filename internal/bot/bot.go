package bot

import (
	"context"
	"fmt"
	"log"
	"news-bot/config"
	"news-bot/internal/ai"
	"news-bot/internal/localization"
	"news-bot/internal/news_fetcher"
	"news-bot/internal/scheduler"
	"news-bot/internal/storage"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type TelegramBot struct {
	api             *tgbotapi.BotAPI
	cfg             config.Config
	localizer       *localization.Localizer
	fetcher         *news_fetcher.Fetcher
	scheduler       *scheduler.Scheduler
	summarizer      *ai.Summarizer
	storage         *storage.Storage
	postedArticles  map[string]bool
	newsSourcesJSON string
}

func NewBot(
	cfg config.Config,
	localizer *localization.Localizer,
	fetcher *news_fetcher.Fetcher,
	scheduler *scheduler.Scheduler,
	summarizer *ai.Summarizer,
	storage *storage.Storage,
	newsSourcesJSON string,
) (*TelegramBot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return nil, err
	}

	postedLinks, err := storage.LoadPostedLinks()
	if err != nil {
		return nil, fmt.Errorf("could not load posted links from db: %w", err)
	}

	return &TelegramBot{
		api:             api,
		cfg:             cfg,
		localizer:       localizer,
		fetcher:         fetcher,
		scheduler:       scheduler,
		summarizer:      summarizer,
		storage:         storage,
		postedArticles:  postedLinks,
		newsSourcesJSON: newsSourcesJSON,
	}, nil
}

func (b *TelegramBot) Start() {
	b.api.Debug = false
	log.Printf("Authorized on account %s", b.api.Self.UserName)

	b.scheduleNewsFetching()
	b.scheduler.Start()

	b.listenForCommands()
}

func (b *TelegramBot) scheduleNewsFetching() {
	log.Printf(
		"Scheduling news fetching job. Interval: %d minutes, Post Limit: %d",
		b.cfg.ScheduleIntervalMinutes,
		b.cfg.PostLimitPerRun,
	)

	jobInterval := time.Duration(b.cfg.ScheduleIntervalMinutes) * time.Minute

	b.scheduler.AddJob(jobInterval, func() {
		log.Println("Scheduler fired: Fetching news...")
		ctx := context.Background()
		b.fetchAndPostNews(ctx)
	})
}

func (b *TelegramBot) fetchAndPostNews(ctx context.Context) {
	log.Println("Discovering article links from configured sources file...")
	discoveredArticles, err := b.fetcher.DiscoverArticles(b.newsSourcesJSON)
	if err != nil {
		log.Printf("Error discovering articles: %v", err)
		return
	}

	log.Printf("Found %d total article links. Checking against %d known posts...", len(discoveredArticles), len(b.postedArticles))

	postsCount := 0
	for _, articleStub := range discoveredArticles {
		if postsCount >= b.cfg.PostLimitPerRun {
			log.Printf("Post limit of %d reached for this run. Stopping.", b.cfg.PostLimitPerRun)
			break
		}

		if articleStub.Link == "" {
			continue
		}

		if b.postedArticles[articleStub.Link] {
			continue
		}

		log.Printf("Found new article link: %s. Scraping full content...", articleStub.Link)
		fullArticle, err := b.fetcher.ScrapeArticleDetails(articleStub.Link)
		if err != nil {
			log.Printf("Could not scrape article '%s': %v", articleStub.Link, err)
			continue
		}

		summary, err := b.summarizer.Summarize(ctx, fullArticle.TextContent)
		if err != nil {
			log.Printf("Could not summarize article '%s': %v", fullArticle.Title, err)
			continue
		}

		err = b.sendArticleToChannel(fullArticle, summary)
		if err != nil {
			log.Printf("Failed to send article '%s', it will be retried next cycle: %v", fullArticle.Title, err)
			continue
		}

		err = b.storage.MarkAsPosted(fullArticle.Link)
		if err != nil {
			log.Printf("CRITICAL: Failed to mark article as posted in DB: %v", err)
		}
		b.postedArticles[fullArticle.Link] = true
		postsCount++

		time.Sleep(5 * time.Second)
	}
}

func (b *TelegramBot) sendArticleToChannel(article *news_fetcher.Article, summary string) error {
	caption := b.formatCaption(article, summary)

	chatID, err := strconv.ParseInt(b.cfg.TelegramChatID, 10, 64)
	if err != nil {
		log.Printf("Invalid TelegramChatID. It must be a number. Value: %s", b.cfg.TelegramChatID)
		return err
	}

	if article.ImageURL == "" {
		msg := tgbotapi.NewMessage(chatID, caption)
		msg.ParseMode = tgbotapi.ModeMarkdownV2
		msg.DisableWebPagePreview = true
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send text message: %v", err)
			return err
		}
	} else {
		photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(article.ImageURL))
		photoMsg.Caption = caption
		photoMsg.ParseMode = tgbotapi.ModeMarkdownV2
		if _, err := b.api.Send(photoMsg); err != nil {
			log.Printf("Failed to send photo message: %v. Trying to send as text.", err)

			msg := tgbotapi.NewMessage(chatID, caption)
			msg.ParseMode = tgbotapi.ModeMarkdownV2
			msg.DisableWebPagePreview = true
			if _, err_text := b.api.Send(msg); err_text != nil {
				log.Printf("Failed to send message as text either: %v", err_text)
				return err_text
			}
		}
	}
	log.Printf("Successfully posted article to channel: %s", article.Title)
	return nil
}

func (b *TelegramBot) formatCaption(article *news_fetcher.Article, summary string) string {
	markdownEscaper := strings.NewReplacer(
		"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]", "(",
		"\\(", ")", "\\)", "~", "\\~", "`", "\\`", ">", "\\>",
		"#", "\\#", "+", "\\+", "-", "\\-", "=", "\\=", "|",
		"\\|", "{", "\\{", "}", "\\}", ".", "\\.", "!", "\\!",
	)

	escapedTitle := markdownEscaper.Replace(article.Title)
	escapedSummary := markdownEscaper.Replace(summary)
	escapedDescription := markdownEscaper.Replace(article.Description)

	templateReplacer := strings.NewReplacer(
		"{title}", escapedTitle,
		"{summary}", escapedSummary,
		"{link}", article.Link,
		"{description}", escapedDescription,
	)

	caption := templateReplacer.Replace(b.cfg.TelegramMessageTemplate)

	return caption
}

func (b *TelegramBot) listenForCommands() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		if update.Message == nil {
			continue
		}
		if update.Message.IsCommand() {
			b.handleCommand(update.Message)
		}
	}
}

func (b *TelegramBot) handleCommand(message *tgbotapi.Message) {
	switch message.Command() {
	case "start":
		msgText := b.localizer.GetMessage(b.cfg.DefaultLanguage, "welcome_message")
		msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
		b.api.Send(msg)
	}
}