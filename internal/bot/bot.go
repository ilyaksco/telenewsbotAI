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
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type ConversationState struct {
	Step             string
	PendingSource    news_fetcher.Source
	PendingArticleID int64
	PendingTopicName string
}

type TelegramBot struct {
	api            *tgbotapi.BotAPI
	cfg            *config.Config
	localizer      *localization.Localizer
	fetcher        *news_fetcher.Fetcher
	scheduler      *scheduler.Scheduler
	summarizer     *ai.Summarizer
	storage        *storage.Storage
	postedArticles map[string]bool
	ctx            context.Context
	userStates     map[int64]*ConversationState
	stateMutex     sync.Mutex
	configMutex    sync.RWMutex
}

func NewBot(
	ctx context.Context,
	cfg *config.Config,
	localizer *localization.Localizer,
	fetcher *news_fetcher.Fetcher,
	scheduler *scheduler.Scheduler,
	storage *storage.Storage,
) (*TelegramBot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return nil, err
	}
	postedLinks, err := storage.LoadPostedLinks()
	if err != nil {
		return nil, fmt.Errorf("could not load posted links from db: %w", err)
	}
	bot := &TelegramBot{
		api:            api,
		cfg:            cfg,
		localizer:      localizer,
		fetcher:        fetcher,
		scheduler:      scheduler,
		storage:        storage,
		postedArticles: postedLinks,
		userStates:     make(map[int64]*ConversationState),
		ctx:            ctx,
	}
	if err := bot.reloadSummarizer(); err != nil {
		return nil, fmt.Errorf("failed to initialize summarizer: %w", err)
	}
	return bot, nil
}

func (b *TelegramBot) reloadSummarizer() error {
	log.Println("Reloading AI Summarizer with new settings...")
	b.configMutex.RLock()
	apiKey := b.cfg.GeminiAPIKey
	model := b.cfg.GeminiModel
	prompt := b.cfg.AiPrompt
	b.configMutex.RUnlock()
	summarizer, err := ai.NewSummarizer(b.ctx, apiKey, model, prompt)
	if err != nil {
		log.Printf("CRITICAL: Failed to reload summarizer: %v", err)
		return err
	}
	b.summarizer = summarizer
	log.Println("AI Summarizer reloaded successfully.")
	return nil
}

func (b *TelegramBot) Start() {
	b.configMutex.RLock()
	username := b.api.Self.UserName
	b.configMutex.RUnlock()
	b.api.Debug = false
	log.Printf("Authorized on account %s", username)
	b.scheduleNewsFetching()
	b.scheduler.Start()
	b.listenForUpdates()
}

func (b *TelegramBot) listenForUpdates() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		if update.CallbackQuery != nil {
			if !b.isAdmin(update.CallbackQuery.From.ID) {
				lang := b.getLang()
				b.api.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, b.localizer.GetMessage(lang, "permission_denied")))
				continue
			}
			b.handleCallbackQuery(update.CallbackQuery)
			continue
		}
		if update.Message == nil {
			continue
		}
		if update.Message.IsCommand() {
			b.handleCommand(update.Message)
			continue
		}
		userID := update.Message.From.ID
		b.stateMutex.Lock()
		state, ok := b.userStates[userID]
		b.stateMutex.Unlock()
		if ok {
			if !b.isAdmin(userID) {
				b.clearUserState(userID)
				continue
			}
			b.handleStatefulMessage(update.Message, state)
		}
	}
}