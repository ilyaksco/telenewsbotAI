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
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	StateAwaitingAIPrompt        = "awaiting_ai_prompt"
	StateAwaitingPostLimit       = "awaiting_post_limit"
	StateAwaitingMessageTemplate = "awaiting_message_template"
	StateAwaitingSchedule        = "awaiting_schedule"
	StateAwaitingSourceURL       = "awaiting_source_url"
	StateAwaitingSourceSelector  = "awaiting_source_selector"
	newsFetchingJobTag           = "news_fetching_job"
)

type ConversationState struct {
	Step          string
	PendingSource news_fetcher.Source
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

func (b *TelegramBot) newsFetchingJob() {
	log.Println("Scheduler fired: Fetching news...")
	b.fetchAndPostNews(context.Background())
}

func (b *TelegramBot) scheduleNewsFetching() {
	b.configMutex.RLock()
	interval := b.cfg.ScheduleIntervalMinutes
	limit := b.cfg.PostLimitPerRun
	b.configMutex.RUnlock()
	log.Printf("Scheduling news fetching job. Interval: %d minutes, Post Limit: %d", interval, limit)
	jobInterval := time.Duration(interval) * time.Minute
	b.scheduler.AddJob(newsFetchingJobTag, jobInterval, b.newsFetchingJob)
}

func (b *TelegramBot) fetchAndPostNews(ctx context.Context) {
	sources, err := b.storage.GetNewsSources()
	if err != nil {
		log.Printf("Error getting sources from DB: %v", err)
		return
	}
	log.Println("Discovering article links from configured sources...")
	discoveredArticles, err := b.fetcher.DiscoverArticles(sources)
	if err != nil {
		log.Printf("Error discovering articles: %v", err)
		return
	}
	log.Printf("Found %d total article links. Checking against %d known posts...", len(discoveredArticles), len(b.postedArticles))
	b.configMutex.RLock()
	postLimit := b.cfg.PostLimitPerRun
	b.configMutex.RUnlock()
	postsCount := 0
	for _, articleStub := range discoveredArticles {
		if postsCount >= postLimit {
			log.Printf("Post limit of %d reached for this run. Stopping.", postLimit)
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
	b.configMutex.RLock()
	chatIDStr := b.cfg.TelegramChatID
	b.configMutex.RUnlock()
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if err != nil {
		log.Printf("Invalid TelegramChatID. It must be a number. Value: %s", chatIDStr)
		return err
	}
	if article.ImageURL == "" {
		msg := tgbotapi.NewMessage(chatID, caption)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = false
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send text message: %v", err)
			return err
		}
	} else {
		photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileURL(article.ImageURL))
		photoMsg.Caption = caption
		photoMsg.ParseMode = tgbotapi.ModeHTML
		if _, err := b.api.Send(photoMsg); err != nil {
			log.Printf("Failed to send photo message: %v. Trying to send as text.", err)
			msg := tgbotapi.NewMessage(chatID, caption)
			msg.ParseMode = tgbotapi.ModeHTML
			msg.DisableWebPagePreview = false
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
	htmlEscaper := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	escapedTitle := htmlEscaper.Replace(article.Title)
	escapedSummary := htmlEscaper.Replace(summary)
	escapedDescription := htmlEscaper.Replace(article.Description)
	b.configMutex.RLock()
	template := b.cfg.TelegramMessageTemplate
	b.configMutex.RUnlock()
	templateReplacer := strings.NewReplacer("{title}", escapedTitle, "{summary}", escapedSummary, "{link}", article.Link, "{description}", escapedDescription)
	return templateReplacer.Replace(template)
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

func (b *TelegramBot) isAdmin(userID int64) bool {
	b.configMutex.RLock()
	superAdminID := b.cfg.SuperAdminID
	b.configMutex.RUnlock()
	if userID == superAdminID {
		return true
	}
	isAdmin, err := b.storage.IsUserAdmin(userID)
	if err != nil {
		log.Printf("Could not check admin status for user %d: %v", userID, err)
		return false
	}
	return isAdmin
}

func (b *TelegramBot) handleCancelCommand(message *tgbotapi.Message) {
	userID := message.From.ID
	b.stateMutex.Lock()
	_, inState := b.userStates[userID]
	if inState {
		delete(b.userStates, userID)
	}
	b.stateMutex.Unlock()
	if inState {
		lang := b.getLang()
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "setting_update_cancelled"))
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send cancel confirmation: %v", err)
		}
	}
}

func (b *TelegramBot) handleCommand(message *tgbotapi.Message) {
	lang := b.getLang()
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	cmd := message.Command()
	protectedCommands := map[string]bool{"settings": true, "setadmin": true, "cancel": true}
	if protectedCommands[cmd] && !b.isAdmin(message.From.ID) {
		msg.Text = b.localizer.GetMessage(lang, "permission_denied")
		b.api.Send(msg)
		return
	}
	switch cmd {
	case "start":
		msg.Text = b.localizer.GetMessage(lang, "welcome_message")
	case "help":
		msg.Text = b.localizer.GetMessage(lang, "help_message")
	case "settings":
		b.handleSettingsCommand(message)
		return
	case "setadmin":
		b.handleSetAdminCommand(message)
		return
	case "cancel":
		b.handleCancelCommand(message)
		return
	default:
		return
	}
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("Failed to send command response: %v", err)
	}
}

func (b *TelegramBot) handleSetAdminCommand(message *tgbotapi.Message) {
	lang := b.getLang()
	b.configMutex.RLock()
	superAdminID := b.cfg.SuperAdminID
	b.configMutex.RUnlock()
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	if message.From.ID != superAdminID {
		msg.Text = b.localizer.GetMessage(lang, "permission_denied")
		b.api.Send(msg)
		return
	}
	args := message.CommandArguments()
	parts := strings.Fields(args)
	if len(parts) != 2 {
		msg.Text = b.localizer.GetMessage(lang, "setadmin_usage")
		b.api.Send(msg)
		return
	}
	targetID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		msg.Text = b.localizer.GetMessage(lang, "setadmin_usage")
		b.api.Send(msg)
		return
	}
	if targetID == superAdminID {
		msg.Text = b.localizer.GetMessage(lang, "setadmin_superadmin_fail")
		b.api.Send(msg)
		return
	}
	isAdmin, err := strconv.ParseBool(parts[1])
	if err != nil {
		msg.Text = b.localizer.GetMessage(lang, "setadmin_usage")
		b.api.Send(msg)
		return
	}
	if err := b.storage.SetUserAdmin(targetID, isAdmin); err != nil {
		log.Printf("Failed to set admin status for user %d: %v", targetID, err)
		return
	}
	msg.Text = fmt.Sprintf(b.localizer.GetMessage(lang, "setadmin_success"), targetID)
	b.api.Send(msg)
}

func (b *TelegramBot) handleCallbackQuery(callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID
	messageID := callback.Message.MessageID
	lang := b.getLang()
	callbackData := strings.Split(callback.Data, ":")
	action := callbackData[0]
	var data string
	if len(callbackData) > 1 {
		data = callbackData[1]
	}
	msg := tgbotapi.NewMessage(chatID, "")
	callbackAns := tgbotapi.NewCallback(callback.ID, "")
	switch action {
	case "edit_ai_prompt":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingAIPrompt})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_ai_prompt")
		b.api.Send(msg)
	case "edit_post_limit":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingPostLimit})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_post_limit")
		b.api.Send(msg)
	case "edit_schedule":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingSchedule})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_schedule")
		b.api.Send(msg)
	case "edit_gemini_model":
		b.sendModelSelectionMenu(chatID, messageID)
	case "edit_msg_template":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingMessageTemplate})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_msg_template")
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
	case "set_gemini_model":
		b.configMutex.Lock()
		b.cfg.GeminiModel = data
		b.configMutex.Unlock()
		if err := b.storage.SetSetting("gemini_model", data); err != nil {
			log.Printf("Failed to update gemini_model in db: %v", err)
		}
		b.reloadSummarizer()
		successMsg := tgbotapi.NewEditMessageText(chatID, messageID, b.localizer.GetMessage(lang, "setting_updated_success"))
		b.api.Send(successMsg)
	case "manage_sources":
		b.sendSourcesMenu(chatID, messageID)
	case "view_sources":
		b.handleViewSources(chatID, messageID)
	case "add_source":
		b.handleAddSource(chatID, messageID)
	case "delete_source_menu":
		b.handleDeleteSourceMenu(chatID, messageID)
	case "delete_source":
		sourceID, _ := strconv.ParseInt(data, 10, 64)
		b.sendDeleteConfirmation(chatID, messageID, sourceID)
	case "execute_delete_source":
		sourceID, _ := strconv.ParseInt(data, 10, 64)
		if err := b.storage.DeleteNewsSource(sourceID); err != nil {
			log.Printf("Failed to delete source with id %d: %v", sourceID, err)
		}
		callbackAns.Text = b.localizer.GetMessage(lang, "source_deleted_success")
		b.handleDeleteSourceMenu(chatID, messageID)
	case "chose_source_type":
		sourceType := data
		state := &ConversationState{Step: StateAwaitingSourceURL, PendingSource: news_fetcher.Source{Type: sourceType}}
		b.setUserState(userID, state)
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, b.localizer.GetMessage(lang, "ask_source_url"))
		b.api.Send(editMsg)
	case "cancel_edit":
		b.sendSourcesMenu(chatID, messageID)
	case "back_to_settings":
		deleteConfig := tgbotapi.NewDeleteMessage(chatID, messageID)
		b.api.Request(deleteConfig)
		b.handleSettingsCommand(callback.Message)
	}
	if _, err := b.api.Request(callbackAns); err != nil {
		log.Printf("Failed to answer callback query: %v", err)
	}
}

func (b *TelegramBot) sendDeleteConfirmation(chatID int64, messageID int, sourceID int64) {
	lang := b.getLang()
	sources, _ := b.storage.GetNewsSources()
	var sourceURL string
	for _, s := range sources {
		if s.ID == sourceID {
			sourceURL = s.URL
			break
		}
	}
	text := fmt.Sprintf(b.localizer.GetMessage(lang, "confirm_delete_prompt"), sourceURL)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_yes_delete"), fmt.Sprintf("execute_delete_source:%d", sourceID)), tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_no_cancel"), "delete_source_menu")))
	msg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *TelegramBot) sendSourcesMenu(chatID int64, messageID int) {
	lang := b.getLang()
	text := b.localizer.GetMessage(lang, "sources_menu_title")
	sourcesKeyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_view_sources"), "view_sources"), tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_add_source"), "add_source")), tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_delete_source"), "delete_source_menu")), tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_back_to_main_settings"), "back_to_settings")))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ParseMode = tgbotapi.ModeHTML
	editMsg.ReplyMarkup = &sourcesKeyboard
	b.api.Send(editMsg)
}

func (b *TelegramBot) handleAddSource(chatID int64, messageID int) {
	lang := b.getLang()
	text := b.localizer.GetMessage(lang, "ask_source_type")
	typeKeyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_source_type_rss"), "chose_source_type:rss"), tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_source_type_scrape"), "chose_source_type:scrape")), tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_cancel"), "manage_sources")))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ReplyMarkup = &typeKeyboard
	b.api.Send(editMsg)
}

func (b *TelegramBot) handleDeleteSourceMenu(chatID int64, messageID int) {
	lang := b.getLang()
	sources, err := b.storage.GetNewsSources()
	if err != nil {
		log.Printf("Failed to get sources for deletion menu: %v", err)
		return
	}
	text := b.localizer.GetMessage(lang, "delete_source_title")
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, source := range sources {
		displayURL := source.URL
		if len(displayURL) > 30 {
			displayURL = displayURL[:27] + "..."
		}
		buttonText := fmt.Sprintf("❌ %s (%s)", displayURL, source.Type)
		row := tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(buttonText, fmt.Sprintf("delete_source:%d", source.ID)))
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_back_to_menu"), "manage_sources")))
	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ReplyMarkup = &keyboard
	b.api.Send(editMsg)
}

func (b *TelegramBot) sendModelSelectionMenu(chatID int64, messageID int) {
	lang := b.getLang()
	text := b.localizer.GetMessage(lang, "ask_for_new_gemini_model")
	modelKeyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Gemini 1.5 Flash", "set_gemini_model:gemini-1.5-flash")), tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("Gemini 1.5 Pro", "set_gemini_model:gemini-1.5-pro")), tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_cancel"), "cancel_edit")))
	editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
	editMsg.ReplyMarkup = &modelKeyboard
	b.api.Send(editMsg)
}

func (b *TelegramBot) handleViewSources(chatID int64, messageID int) {
	lang := b.getLang()
	sources, err := b.storage.GetNewsSources()
	if err != nil {
		log.Printf("Failed to get sources for viewing: %v", err)
		return
	}
	var builder strings.Builder
	builder.WriteString(b.localizer.GetMessage(lang, "sources_list_title"))
	if len(sources) == 0 {
		builder.WriteString(b.localizer.GetMessage(lang, "no_sources_found"))
	} else {
		for _, source := range sources {
			format := b.localizer.GetMessage(lang, "sources_list_format")
			builder.WriteString(fmt.Sprintf(format, source.ID, source.Type, source.URL))
		}
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_back_to_menu"), "manage_sources")))
	msg := tgbotapi.NewEditMessageText(chatID, messageID, builder.String())
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *TelegramBot) handleStatefulMessage(message *tgbotapi.Message, state *ConversationState) {
	userID := message.From.ID
	lang := b.getLang()
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	switch state.Step {
	case StateAwaitingAIPrompt:
		b.configMutex.Lock()
		b.cfg.AiPrompt = message.Text
		b.configMutex.Unlock()
		if err := b.storage.SetSetting("ai_prompt", message.Text); err != nil {
			log.Printf("Failed to update ai_prompt in db: %v", err)
		}
		b.reloadSummarizer()
		msg.Text = b.localizer.GetMessage(lang, "setting_updated_success")
		b.clearUserState(userID)
	case StateAwaitingPostLimit:
		limit, err := strconv.Atoi(message.Text)
		if err != nil || limit <= 0 {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			b.configMutex.Lock()
			b.cfg.PostLimitPerRun = limit
			b.configMutex.Unlock()
			if err := b.storage.SetSetting("post_limit_per_run", message.Text); err != nil {
				log.Printf("Failed to update post_limit_per_run in db: %v", err)
			}
			msg.Text = b.localizer.GetMessage(lang, "setting_updated_success")
			b.clearUserState(userID)
		}
	case StateAwaitingMessageTemplate:
		b.configMutex.Lock()
		b.cfg.TelegramMessageTemplate = message.Text
		b.configMutex.Unlock()
		if err := b.storage.SetSetting("telegram_message_template", message.Text); err != nil {
			log.Printf("Failed to update telegram_message_template in db: %v", err)
		}
		msg.Text = b.localizer.GetMessage(lang, "setting_updated_success")
		b.clearUserState(userID)
	case StateAwaitingSchedule:
		interval, err := strconv.Atoi(message.Text)
		if err != nil || interval <= 0 {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			b.scheduler.RemoveJobByTag(newsFetchingJobTag)
			newInterval := time.Duration(interval) * time.Minute
			b.scheduler.AddJob(newsFetchingJobTag, newInterval, b.newsFetchingJob)
			log.Printf("Rescheduled news fetching job with new interval: %d minutes", interval)
			b.configMutex.Lock()
			b.cfg.ScheduleIntervalMinutes = interval
			b.configMutex.Unlock()
			if err := b.storage.SetSetting("schedule_interval_minutes", message.Text); err != nil {
				log.Printf("Failed to update schedule_interval_minutes in db: %v", err)
			}
			msg.Text = b.localizer.GetMessage(lang, "setting_updated_success")
			b.clearUserState(userID)
		}
	case StateAwaitingSourceURL:
		state.PendingSource.URL = message.Text
		if state.PendingSource.Type == "rss" {
			if err := b.storage.AddNewsSource(state.PendingSource); err != nil {
				log.Printf("Failed to add new RSS source to db: %v", err)
			}
			msg.Text = b.localizer.GetMessage(lang, "source_added_success")
			b.clearUserState(userID)
		} else {
			state.Step = StateAwaitingSourceSelector
			b.setUserState(userID, state)
			msg.Text = b.localizer.GetMessage(lang, "ask_source_selector")
		}
	case StateAwaitingSourceSelector:
		state.PendingSource.LinkSelector = message.Text
		if err := b.storage.AddNewsSource(state.PendingSource); err != nil {
			log.Printf("Failed to add new Scrape source to db: %v", err)
		}
		msg.Text = b.localizer.GetMessage(lang, "source_added_success")
		b.clearUserState(userID)
	}
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("Failed to send state response message: %v", err)
	}
}

func (b *TelegramBot) handleSettingsCommand(message *tgbotapi.Message) {
	lang := b.getLang()
	settings, err := b.storage.GetAllSettings()
	if err != nil {
		log.Printf("Could not get settings for display: %v", err)
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "settings_error"))
		if _, err_send := b.api.Send(msg); err_send != nil {
			log.Printf("Failed to send settings error message: %v", err_send)
		}
		return
	}
	if len(settings) == 0 {
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "settings_empty"))
		if _, err_send := b.api.Send(msg); err_send != nil {
			log.Printf("Failed to send settings empty message: %v", err_send)
		}
		return
	}
	var builder strings.Builder
	builder.WriteString(b.localizer.GetMessage(lang, "settings_title") + "\n\n")
	displayOrder := []string{"super_admin_id", "telegram_chat_id", "ai_prompt", "post_limit_per_run", "schedule_interval_minutes", "gemini_model", "telegram_message_template", "default_language", "news_sources_file_path"}
	sensitiveKeys := map[string]bool{"telegram_bot_token": true, "gemini_api_key": true}

	for _, key := range displayOrder {
		value, ok := settings[key]
		if !ok {
			continue
		}
		if sensitiveKeys[key] {
			value = "********"
		}
		displayName := b.localizer.GetMessage(lang, "setting_name_"+key)
		format := b.localizer.GetMessage(lang, "settings_format")
		builder.WriteString(fmt.Sprintf(format, displayName, value))
	}
	builder.WriteString(b.localizer.GetMessage(lang, "settings_edit_prompt"))
	msg := tgbotapi.NewMessage(message.Chat.ID, builder.String())
	msg.ParseMode = tgbotapi.ModeHTML
	keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_ai_prompt"), "edit_ai_prompt"), tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_post_limit"), "edit_post_limit")), tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_gemini_model"), "edit_gemini_model"), tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_msg_template"), "edit_msg_template")), tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_schedule"), "edit_schedule"), tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_manage_sources"), "manage_sources")))
	msg.ReplyMarkup = &keyboard
	if _, err_send := b.api.Send(msg); err_send != nil {
		log.Printf("Failed to send settings message: %v", err_send)
	}
}

func (b *TelegramBot) setUserState(userID int64, state *ConversationState) {
	b.stateMutex.Lock()
	defer b.stateMutex.Unlock()
	b.userStates[userID] = state
}

func (b *TelegramBot) clearUserState(userID int64) {
	b.stateMutex.Lock()
	defer b.stateMutex.Unlock()
	delete(b.userStates, userID)
}

func (b *TelegramBot) getLang() string {
	b.configMutex.RLock()
	defer b.configMutex.RUnlock()
	return b.cfg.DefaultLanguage
}