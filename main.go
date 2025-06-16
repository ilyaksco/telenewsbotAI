package main

import (
	"context"
	"embed"
	"log"
	"news-bot/config"
	"news-bot/internal/bot"
	"news-bot/internal/localization"
	"news-bot/internal/news_fetcher"
	"news-bot/internal/scheduler"
	"news-bot/internal/storage"
	"strconv"
)

//go:embed locales
var localeFiles embed.FS

func main() {
	log.Println("Starting AI News Bot...")

	ctx := context.Background()

	dbStorage, err := storage.NewStorage("newsbot.db")
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer dbStorage.Close()

	cfg, found, err := config.LoadConfigFromDB(dbStorage)
	if err != nil {
		log.Fatalf("Failed to load config from DB: %v", err)
	}
	if !found {
		log.Println("No settings found in database. Loading from .env and saving to DB...")
		envCfg, err := config.LoadConfigFromEnv()
		if err != nil {
			log.Fatalf("Failed to load config from .env: %v", err)
		}
		err = config.SaveConfigToDB(dbStorage, &envCfg)
		if err != nil {
			log.Fatalf("Failed to save initial config to DB: %v", err)
		}
		cfg = &envCfg
	} else {
		log.Println("Settings successfully loaded from database.")
	}

	// Force-reread SuperAdminID from .env on every startup for security and resilience.
	superAdminIDFromEnv, err := config.GetSuperAdminFromEnv()
	if err != nil {
		log.Printf("WARNING: Could not read SUPER_ADMIN_ID from .env on startup: %v. Using value from DB.", err)
	} else if superAdminIDFromEnv != cfg.SuperAdminID {
		log.Printf("SuperAdminID from .env (%d) differs from DB (%d). Overwriting with .env value.", superAdminIDFromEnv, cfg.SuperAdminID)
		cfg.SuperAdminID = superAdminIDFromEnv
		if err := dbStorage.SetSetting("super_admin_id", strconv.FormatInt(cfg.SuperAdminID, 10)); err != nil {
			log.Printf("WARNING: Could not update super_admin_id in DB with value from .env.")
		}
	}

	if err := dbStorage.SetUserAdmin(cfg.SuperAdminID, true); err != nil {
		log.Fatalf("Failed to set superadmin status in db: %v", err)
	}
	log.Printf("Superadmin with ID %d ensured.", cfg.SuperAdminID)

	// migrateSources(dbStorage, cfg.NewsSourcesFilePath) // This call is already commented out, now we remove the function body below.

	localizer := localization.NewLocalizer(localeFiles)
	fetcher := news_fetcher.NewFetcher()
	appScheduler, err := scheduler.NewScheduler()
	if err != nil {
		log.Fatalf("Failed to create scheduler: %v", err)
	}
	telegramBot, err := bot.NewBot(ctx, cfg, localizer, fetcher, appScheduler, dbStorage)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}
	log.Println("Bot is running...")
	telegramBot.Start()
}

// The migrateSources function that was here has been completely removed.