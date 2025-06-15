package config

import (
	"log"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	TelegramBotToken        string `envconfig:"TELEGRAM_BOT_TOKEN" required:"true"`
	TelegramChatID          string `envconfig:"TELEGRAM_CHAT_ID"   required:"true"`
	GeminiAPIKey            string `envconfig:"GEMINI_API_KEY"     required:"true"`
	DefaultLanguage         string `envconfig:"DEFAULT_LANGUAGE" default:"en"`
	NewsSourcesFilePath     string `envconfig:"NEWS_SOURCES_FILE_PATH" required:"true"`
	AiPrompt                string `envconfig:"AI_PROMPT"        required:"true"`
	ScheduleIntervalMinutes int    `envconfig:"SCHEDULE_INTERVAL_MINUTES" default:"60"`
	PostLimitPerRun         int    `envconfig:"POST_LIMIT_PER_RUN"         default:"5"`
}

func LoadConfig() Config {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, reading from environment variables")
	}

	var cfg Config
	err = envconfig.Process("", &cfg)
	if err != nil {
		log.Fatalf("Failed to process configuration: %v", err)
	}

	return cfg
}