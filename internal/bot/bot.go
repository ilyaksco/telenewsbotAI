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
	Step                string
	PendingSource       news_fetcher.Source
	PendingArticleID    int64
	PendingTopicName    string
	OriginalMessageID   int
	OriginalChatID      int64
	OriginalMessageText string
}

type TelegramBot struct {
	api             *tgbotapi.BotAPI
	globalCfg       *config.GlobalConfig
	defaultChatCfg  *config.Config
	localizer       *localization.Localizer
	fetcher         *news_fetcher.Fetcher
	scheduler       *scheduler.Scheduler
	storage         *storage.Storage
	ctx             context.Context
	userStates      map[int64]*ConversationState
	stateMutex      sync.Mutex
	summarizers     map[string]*ai.Summarizer
	summarizerMutex sync.RWMutex
	isFetching      map[int64]bool
	fetchingMutex   sync.Mutex
	cancelFunc      context.CancelFunc
}

func NewBot(
	ctx context.Context,
	globalCfg *config.GlobalConfig,
	defaultChatCfg *config.Config,
	localizer *localization.Localizer,
	fetcher *news_fetcher.Fetcher,
	scheduler *scheduler.Scheduler,
	storage *storage.Storage,
) (*TelegramBot, error) {
	api, err := tgbotapi.NewBotAPI(globalCfg.TelegramBotToken)
	if err != nil {
		return nil, err
	}

	bot := &TelegramBot{
		api:            api,
		globalCfg:      globalCfg,
		defaultChatCfg: defaultChatCfg,
		localizer:      localizer,
		fetcher:        fetcher,
		scheduler:      scheduler,
		storage:        storage,
		userStates:     make(map[int64]*ConversationState),
		summarizers:    make(map[string]*ai.Summarizer),
		isFetching:     make(map[int64]bool),
		ctx:            ctx,
	}

	return bot, nil
}

func (b *TelegramBot) getSummarizerForChat(chatCfg *config.Config) (*ai.Summarizer, error) {
	b.summarizerMutex.RLock()
	configKey := fmt.Sprintf("%s-%s", chatCfg.GeminiModel, chatCfg.AiPrompt)
	summarizer, exists := b.summarizers[configKey]
	b.summarizerMutex.RUnlock()

	if exists {
		return summarizer, nil
	}

	b.summarizerMutex.Lock()
	defer b.summarizerMutex.Unlock()

	summarizer, exists = b.summarizers[configKey]
	if exists {
		return summarizer, nil
	}

	log.Printf("Creating new summarizer instance for model %s", chatCfg.GeminiModel)
	newSummarizer, err := ai.NewSummarizer(b.ctx, b.globalCfg.GeminiAPIKey, chatCfg.GeminiModel, chatCfg.AiPrompt)
	if err != nil {
		return nil, fmt.Errorf("failed to create new summarizer instance: %w", err)
	}

	b.summarizers[configKey] = newSummarizer
	return newSummarizer, nil
}

func (b *TelegramBot) Start() {
	b.api.Debug = false
	log.Printf("Authorized on account %s", b.api.Self.UserName)

	b.scheduleNewsDispatcher()
	b.scheduler.Start()

	b.listenForUpdates()
}

func (b *TelegramBot) listenForUpdates() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	for update := range updates {
		if update.MyChatMember != nil {
			go b.handleChatMemberUpdate(update.MyChatMember)
			continue
		}

		if update.CallbackQuery != nil {
			go b.handleCallbackQuery(update.CallbackQuery)
			continue
		}

		if update.Message == nil {
			continue
		}

		if update.Message.IsCommand() {
			go b.handleCommand(update.Message)
			continue
		}

		b.stateMutex.Lock()
		_, ok := b.userStates[update.Message.From.ID]
		b.stateMutex.Unlock()

		if ok {
			go b.handleStatefulMessage(update.Message)
		}
	}
}

func (b *TelegramBot) handleChatMemberUpdate(update *tgbotapi.ChatMemberUpdated) {
	chatID := update.Chat.ID

	if (update.NewChatMember.Status == "member" || update.NewChatMember.Status == "administrator") && update.NewChatMember.User.ID == b.api.Self.ID {
		log.Printf("Bot was added to new chat: %s (ID: %d)", update.Chat.Title, chatID)

		isConfigured, err := b.storage.IsChatConfigured(chatID)
		if err != nil {
			log.Printf("Error checking if chat %d is configured: %v", chatID, err)
			return
		}

		if !isConfigured {
			log.Printf("Chat %d is new. Creating default configuration...", chatID)
			err := b.storage.CreateDefaultChatConfig(chatID, b.defaultChatCfg)
			if err != nil {
				log.Printf("Failed to create default config for chat %d: %v", chatID, err)
				return
			}
			lang := b.getLangForChat(chatID)
			welcomeText := b.localizer.GetMessage(lang, "welcome_message")
			guidanceText := "\n\nAdmins of this chat can now configure me using /settings or type /help for more info."
			if lang == "id" {
				guidanceText = "\n\nAdmin dari chat ini sekarang dapat mengonfigurasi saya menggunakan /settings atau ketik /help untuk info lebih lanjut."
			}

			msg := tgbotapi.NewMessage(chatID, welcomeText+guidanceText)
			b.api.Send(msg)
		} else {
			log.Printf("Bot re-joined chat %d. Configuration already exists.", chatID)
		}
	}
}