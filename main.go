package main

import (
	"context"
	"embed"
	"log"
	"news-bot/config"
	"news-bot/internal/ai"
	"news-bot/internal/bot"
	"news-bot/internal/localization"
	"news-bot/internal/news_fetcher"
	"news-bot/internal/scheduler"
	"news-bot/internal/storage"
	"os" 
)


var localeFiles embed.FS

func main() {
	log.Println("Starting AI News Bot...")

	ctx := context.Background()
	cfg := config.LoadConfig()

	sourcesJSON, err := os.ReadFile(cfg.NewsSourcesFilePath)
	if err != nil {
		log.Fatalf("Failed to read news sources file from path %s: %v", cfg.NewsSourcesFilePath, err)
	}

	dbStorage, err := storage.NewStorage("newsbot.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer dbStorage.Close()

	localizer := localization.NewLocalizer(localeFiles)
	fetcher := news_fetcher.NewFetcher()

	appScheduler, err := scheduler.NewScheduler()
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
	}

	summarizer, err := ai.NewSummarizer(ctx, cfg.GeminiAPIKey, cfg.AiPrompt)
	if err != nil {
		log.Fatalf("Failed to create AI summarizer: %v", err)
	}

	telegramBot, err := bot.NewBot(cfg, localizer, fetcher, appScheduler, summarizer, dbStorage, string(sourcesJSON))
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	log.Println("Bot is running...")
	telegramBot.Start()
}