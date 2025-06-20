package main

import (
	"context"
	"embed"
	"io/ioutil"
	"log"
	"news-bot/config"
	"news-bot/internal/bot"
	"news-bot/internal/localization"
	"news-bot/internal/news_fetcher"
	"news-bot/internal/scheduler"
	"news-bot/internal/storage"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

//go:embed locales
var localeFiles embed.FS

const pidFile = "bot.pid"

func main() {
	// --- PID File Handling: Prevent duplicate instances ---
	if _, err := os.Stat(pidFile); err == nil {
		log.Fatalf("PID file '%s' already exists. Another instance might be running. If not, please delete the file manually.", pidFile)
	}

	pid := os.Getpid()
	if err := ioutil.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644); err != nil {
		log.Fatalf("Failed to write PID file: %v", err)
	}
	defer os.Remove(pidFile) // Ensure PID file is removed on exit

	log.Println("Starting AI News Bot (Multi-Tenant Mode)...")
	log.Printf("Process started with PID: %d", pid)

	// --- Graceful Shutdown Handling ---
	ctx, cancel := context.WithCancel(context.Background())
	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-shutdownChan
		log.Println("Shutdown signal received, stopping bot gracefully...")
		cancel()
		os.Remove(pidFile) // backup removal
		os.Exit(0)
	}()

	// --- Bot Initialization ---
	globalCfg, err := config.LoadGlobalConfig()
	if err != nil {
		log.Fatalf("Failed to load global config from .env: %v", err)
	}

	defaultChatCfg, err := config.GetDefaultChatConfig()
	if err != nil {
		log.Fatalf("Failed to load default chat config: %v", err)
	}

	dbStorage, err := storage.NewStorage("newsbot.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer dbStorage.Close()

	if err := dbStorage.SetSuperAdmin(globalCfg.SuperAdminID, true); err != nil {
		log.Fatalf("Failed to set superadmin status in db: %v", err)
	}
	log.Printf("Superadmin with ID %d ensured.", globalCfg.SuperAdminID)

	localizer := localization.NewLocalizer(localeFiles)
	fetcher := news_fetcher.NewFetcher()
	appScheduler, err := scheduler.NewScheduler()
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
	}

	telegramBot, err := bot.NewBot(ctx, globalCfg, defaultChatCfg, localizer, fetcher, appScheduler, dbStorage)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	log.Println("Bot is running... Press Ctrl+C to exit.")
	telegramBot.Start()
}