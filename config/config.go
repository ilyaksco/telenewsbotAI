package config

import (
	"fmt"
	"log"
	"news-bot/internal/storage"
	"os"
	"strconv"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	TelegramBotToken        string `envconfig:"TELEGRAM_BOT_TOKEN" required:"true" key:"telegram_bot_token"`
	TelegramChatID          string `envconfig:"TELEGRAM_CHAT_ID"   required:"true" key:"telegram_chat_id"`
	SuperAdminID            int64  `envconfig:"SUPER_ADMIN_ID"     required:"true" key:"super_admin_id"`
	GeminiAPIKey            string `envconfig:"GEMINI_API_KEY"     required:"true" key:"gemini_api_key"`
	GeminiModel             string `envconfig:"GEMINI_MODEL" default:"gemini-1.5-flash" key:"gemini_model"`
	DefaultLanguage         string `envconfig:"DEFAULT_LANGUAGE" default:"en" key:"default_language"`
	NewsSourcesFilePath     string `envconfig:"NEWS_SOURCES_FILE_PATH" required:"true" key:"news_sources_file_path"`
	AiPrompt                string `envconfig:"AI_PROMPT"        required:"true" key:"ai_prompt"`
	TelegramMessageTemplate string `envconfig:"TELEGRAM_MESSAGE_TEMPLATE" default:"<b>{title}</b>\n\n{summary}\n\n<a href=\"{link}\">Sumber</a>" key:"telegram_message_template"`
	ScheduleIntervalMinutes int    `envconfig:"SCHEDULE_INTERVAL_MINUTES" default:"60" key:"schedule_interval_minutes"`
	PostLimitPerRun         int    `envconfig:"POST_LIMIT_PER_RUN"         default:"5" key:"post_limit_per_run"`
	EnableApprovalSystem    bool   `envconfig:"ENABLE_APPROVAL_SYSTEM" default:"false" key:"enable_approval_system"`
	ApprovalChatID          int64  `envconfig:"APPROVAL_CHAT_ID" default:"0" key:"approval_chat_id"`
}

func LoadConfigFromEnv() (Config, error) {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, reading from environment variables")
	}

	var cfg Config
	err = envconfig.Process("", &cfg)
	if err != nil {
		return Config{}, fmt.Errorf("failed to process configuration from env: %w", err)
	}
	return cfg, nil
}

func GetSuperAdminFromEnv() (int64, error) {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, reading from environment variables")
	}
	valStr := os.Getenv("SUPER_ADMIN_ID")
	if valStr == "" {
		return 0, fmt.Errorf("SUPER_ADMIN_ID environment variable not set")
	}
	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse SUPER_ADMIN_ID: %w", err)
	}
	return val, nil
}

func LoadConfigFromDB(s *storage.Storage) (*Config, bool, error) {
	settings, err := s.GetAllSettings()
	if err != nil {
		return nil, false, fmt.Errorf("could not get settings from db: %w", err)
	}

	if len(settings) == 0 {
		return nil, false, nil
	}

	scheduleInterval, _ := strconv.Atoi(settings["schedule_interval_minutes"])
	postLimit, _ := strconv.Atoi(settings["post_limit_per_run"])
	superAdminID, _ := strconv.ParseInt(settings["super_admin_id"], 10, 64)
	approvalSystem, _ := strconv.ParseBool(settings["enable_approval_system"])
	approvalChatID, _ := strconv.ParseInt(settings["approval_chat_id"], 10, 64)

	return &Config{
		TelegramBotToken:        settings["telegram_bot_token"],
		TelegramChatID:          settings["telegram_chat_id"],
		SuperAdminID:            superAdminID,
		GeminiAPIKey:            settings["gemini_api_key"],
		GeminiModel:             settings["gemini_model"],
		DefaultLanguage:         settings["default_language"],
		NewsSourcesFilePath:     settings["news_sources_file_path"],
		AiPrompt:                settings["ai_prompt"],
		TelegramMessageTemplate: settings["telegram_message_template"],
		ScheduleIntervalMinutes: scheduleInterval,
		PostLimitPerRun:         postLimit,
		EnableApprovalSystem:    approvalSystem,
		ApprovalChatID:          approvalChatID,
	}, true, nil
}

func SaveConfigToDB(s *storage.Storage, cfg *Config) error {
	settings := map[string]string{
		"telegram_bot_token":        cfg.TelegramBotToken,
		"telegram_chat_id":          cfg.TelegramChatID,
		"super_admin_id":            strconv.FormatInt(cfg.SuperAdminID, 10),
		"gemini_api_key":            cfg.GeminiAPIKey,
		"gemini_model":              cfg.GeminiModel,
		"default_language":          cfg.DefaultLanguage,
		"news_sources_file_path":    cfg.NewsSourcesFilePath,
		"ai_prompt":                 cfg.AiPrompt,
		"telegram_message_template": cfg.TelegramMessageTemplate,
		"schedule_interval_minutes": strconv.Itoa(cfg.ScheduleIntervalMinutes),
		"post_limit_per_run":        strconv.Itoa(cfg.PostLimitPerRun),
		"enable_approval_system":    strconv.FormatBool(cfg.EnableApprovalSystem),
		"approval_chat_id":          strconv.FormatInt(cfg.ApprovalChatID, 10),
	}

	for key, value := range settings {
		if err := s.SetSetting(key, value); err != nil {
			return fmt.Errorf("failed to save setting %s: %w", key, err)
		}
	}
	return nil
}