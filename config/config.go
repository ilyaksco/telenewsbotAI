package config

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type GlobalConfig struct {
	TelegramBotToken string `envconfig:"TELEGRAM_BOT_TOKEN" required:"true"`
	GeminiAPIKey     string `envconfig:"GEMINI_API_KEY"     required:"true"`
	SuperAdminID     int64  `envconfig:"SUPER_ADMIN_ID"     required:"true"`
	GlobalScheduleMinutes int    `envconfig:"GLOBAL_SCHEDULE_MINUTES" default:"15"`
}

type Config struct {
	AiPrompt                string `json:"ai_prompt"`
	GeminiModel             string `json:"gemini_model"`
	TelegramMessageTemplate string `json:"telegram_message_template"`
	PostLimitPerRun         int    `json:"post_limit_per_run"`
	EnableApprovalSystem    bool   `json:"enable_approval_system"`
	ApprovalChatID          int64  `json:"approval_chat_id"`
	RSSMaxAgeHours          int    `json:"rss_max_age_hours"`
	LanguageCode            string `json:"language_code"`
	ScheduleIntervalMinutes int    `json:"schedule_interval_minutes"`
}

func LoadGlobalConfig() (*GlobalConfig, error) {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, reading from environment variables")
	}

	var cfg GlobalConfig
	err = envconfig.Process("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to process global configuration from env: %w", err)
	}
	return &cfg, nil
}

func GetDefaultChatConfig() (*Config, error) {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, reading from environment variables for default chat config")
	}

	geminiModel := os.Getenv("GEMINI_MODEL")
	if geminiModel == "" {
		geminiModel = "gemini-1.5-flash"
	}

	aiPrompt := os.Getenv("AI_PROMPT")
	if aiPrompt == "" {
		aiPrompt = "Summarize this article for a Telegram post, in a neutral and informative tone:"
	}

	template := os.Getenv("TELEGRAM_MESSAGE_TEMPLATE")
	if template == "" {
		template = "<b>{title}</b>\n\n{summary}\n\n<a href=\"{link}\">Sumber</a>"
	}

	schedule, _ := strconv.Atoi(os.Getenv("SCHEDULE_INTERVAL_MINUTES"))
	if schedule == 0 {
		schedule = 60
	}

	postLimit, _ := strconv.Atoi(os.Getenv("POST_LIMIT_PER_RUN"))
	if postLimit == 0 {
		postLimit = 5
	}

	rssMaxAge, _ := strconv.Atoi(os.Getenv("RSS_MAX_AGE_HOURS"))
	if rssMaxAge == 0 {
		rssMaxAge = 24
	}

	approval, _ := strconv.ParseBool(os.Getenv("ENABLE_APPROVAL_SYSTEM"))

	approvalChat, _ := strconv.ParseInt(os.Getenv("APPROVAL_CHAT_ID"), 10, 64)

	return &Config{
		GeminiModel:             geminiModel,
		AiPrompt:                aiPrompt,
		TelegramMessageTemplate: template,
		PostLimitPerRun:         postLimit,
		EnableApprovalSystem:    approval,
		ApprovalChatID:          approvalChat,
		RSSMaxAgeHours:          rssMaxAge,
		ScheduleIntervalMinutes: schedule,
	}, nil
}