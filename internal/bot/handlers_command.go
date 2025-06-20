package bot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *TelegramBot) handleCommand(message *tgbotapi.Message) {
	chatID := message.Chat.ID
	userID := message.From.ID
	
	if err := b.ensureChatIsConfigured(chatID); err != nil {
		log.Printf("Critical error ensuring chat config for %d: %v", chatID, err)
		return
	}

	lang := b.getLangForChat(chatID)
	msg := tgbotapi.NewMessage(chatID, "")
	cmd := message.Command()

	protectedCommands := map[string]bool{"settings": true, "set_target": true, "cancel": true, "lang": true}
	if protectedCommands[cmd] && !b.isChatAdmin(chatID, userID) {
		msg.Text = b.localizer.GetMessage(lang, "permission_denied")
		b.api.Send(msg)
		return
	}

	superAdminCommands := map[string]bool{"fetch_now": true, "fetch_stop": true}
	if superAdminCommands[cmd] && !b.isSuperAdmin(userID) {
		msg.Text = b.localizer.GetMessage(lang, "permission_denied")
		b.api.Send(msg)
		return
	}

	switch cmd {
	case "start":
		// MODIFIED: This is now the main entry point for setting up a chat.
		isConfigured, err := b.storage.IsChatConfigured(chatID)
		if err != nil {
			log.Printf("Error checking if chat %d is configured on /start: %v", chatID, err)
			return
		}

		if !isConfigured {
			log.Printf("New chat %d started conversation. Creating default configuration...", chatID)
			if err := b.storage.CreateDefaultChatConfig(chatID, b.defaultChatCfg); err != nil {
				log.Printf("Failed to create default config for new chat %d: %v", chatID, err)
				return
			}
		}
		// Now that config is guaranteed, get the correct language.
		lang = b.getLangForChat(chatID)
		welcomeMsg := tgbotapi.NewMessage(chatID, b.localizer.GetMessage(lang, "welcome_message"))
		b.api.Send(welcomeMsg)

		// Also send the help message to guide new users.
		helpMsg := tgbotapi.NewMessage(chatID, b.localizer.GetMessage(lang, "help_message_user"))
		helpMsg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(helpMsg)
		return // Return here as we've sent our messages.

	case "help":
		msg.Text = b.localizer.GetMessage(lang, "help_message_user")
		msg.ParseMode = tgbotapi.ModeHTML
	case "lang":
		b.handleLangCommand(message)
		return
	case "settings":
		b.handleSettingsCommand(message)
		return
	case "set_target":
		b.handleSetTargetCommand(message)
		return
	case "fetch_now":
		b.handleFetchNowCommand(message)
		return
	case "fetch_stop":
		b.handleFetchStopCommand(message)
		return
	case "cancel":
		b.handleCancelCommand(message)
		return
	case "analyzelinks":
		if !b.isSuperAdmin(userID) {
			return
		}
		b.handleAnalyzeLinksCommand(message)
		return
	default:
		return
	}

	if msg.Text != "" {
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send command response for chat %d: %v", chatID, err)
		}
	}
}


func (b *TelegramBot) handleLangCommand(message *tgbotapi.Message) {
	chatID := message.Chat.ID
	text := "Please choose your preferred language:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Bahasa Indonesia 🇮🇩", "set_lang:id"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("English 🇬🇧", "set_lang:en"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *TelegramBot) handleFetchNowCommand(message *tgbotapi.Message) {
	// MODIFIED: Get language dynamically and use it.
	lang := b.getLangForChat(message.Chat.ID)
	go b.fetchAndPostNews(context.Background(), message.Chat.ID)
	msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "fetch_now_started"))
	b.api.Send(msg)
}

func (b *TelegramBot) handleFetchStopCommand(message *tgbotapi.Message) {
	// MODIFIED: Get language dynamically and use it.
	lang := b.getLangForChat(message.Chat.ID)
	b.fetchingMutex.Lock()
	defer b.fetchingMutex.Unlock()

	if b.isFetching && b.cancelFunc != nil {
		b.cancelFunc()
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "fetch_stop_in_progress"))
		b.api.Send(msg)
	} else {
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "fetch_stop_not_running"))
		b.api.Send(msg)
	}
}

func (b *TelegramBot) handleSetTargetCommand(message *tgbotapi.Message) {
	chatID := message.Chat.ID
	lang := b.getLangForChat(chatID)

	args := message.CommandArguments()
	parts := strings.Fields(args)

	if len(parts) != 3 {
		msg := tgbotapi.NewMessage(chatID, b.localizer.GetMessage(lang, "set_target_usage"))
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
		return
	}

	topicName := parts[0]
	destChatIDStr := parts[1]
	messageIDStr := parts[2]

	destChatID, errChat := strconv.ParseInt(destChatIDStr, 10, 64)
	messageID, errMsg := strconv.ParseInt(messageIDStr, 10, 64)

	if errChat != nil || errMsg != nil {
		msg := tgbotapi.NewMessage(chatID, b.localizer.GetMessage(lang, "set_target_invalid_id"))
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
		return
	}

	topic, err := b.storage.GetTopicByName(chatID, topicName)
	if err != nil {
		msgText := fmt.Sprintf(b.localizer.GetMessage(lang, "set_target_topic_not_found"), topicName)
		msg := tgbotapi.NewMessage(chatID, msgText)
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
		return
	}

	err = b.storage.UpdateTopicDestination(topic.ID, chatID, destChatID, messageID)
	if err != nil {
		log.Printf("Failed to update topic destination for chat %d: %v", chatID, err)
		msg := tgbotapi.NewMessage(chatID, "Error saving destination.")
		b.api.Send(msg)
		return
	}

	successText := fmt.Sprintf(b.localizer.GetMessage(lang, "set_target_success"), topicName, destChatID, messageID)
	msg := tgbotapi.NewMessage(chatID, successText)
	msg.ParseMode = tgbotapi.ModeHTML
	b.api.Send(msg)
}

func (b *TelegramBot) handleSettingsCommand(message *tgbotapi.Message) {
	chatID := message.Chat.ID
	lang := b.getLangForChat(chatID)

	cfg, err := b.storage.GetChatConfig(chatID)
	if err != nil {
		log.Printf("Could not get settings for chat %d: %v", chatID, err)
		msg := tgbotapi.NewMessage(chatID, b.localizer.GetMessage(lang, "settings_error"))
		b.api.Send(msg)
		return
	}

	var builder strings.Builder
	builder.WriteString(b.localizer.GetMessage(lang, "settings_title") + "\n\n")

	builder.WriteString(fmt.Sprintf(b.localizer.GetMessage(lang, "settings_format"), b.localizer.GetMessage(lang, "setting_name_ai_prompt"), cfg.AiPrompt))
	builder.WriteString(fmt.Sprintf(b.localizer.GetMessage(lang, "settings_format"), b.localizer.GetMessage(lang, "setting_name_gemini_model"), cfg.GeminiModel))
	builder.WriteString(fmt.Sprintf(b.localizer.GetMessage(lang, "settings_format"), b.localizer.GetMessage(lang, "setting_name_post_limit_per_run"), strconv.Itoa(cfg.PostLimitPerRun)))
	builder.WriteString(fmt.Sprintf(b.localizer.GetMessage(lang, "settings_format"), b.localizer.GetMessage(lang, "setting_name_rss_max_age_hours"), fmt.Sprintf("%d hours", cfg.RSSMaxAgeHours)))

	approvalStatus := "Disabled"
	if cfg.EnableApprovalSystem {
		approvalStatus = "Enabled"
	}
	builder.WriteString(fmt.Sprintf(b.localizer.GetMessage(lang, "settings_format"), b.localizer.GetMessage(lang, "setting_name_enable_approval_system"), approvalStatus))

	approvalChat := "Not Set (Defaults to this chat)"
	if cfg.ApprovalChatID != 0 {
		approvalChat = strconv.FormatInt(cfg.ApprovalChatID, 10)
	}
	builder.WriteString(fmt.Sprintf(b.localizer.GetMessage(lang, "settings_format"), b.localizer.GetMessage(lang, "setting_name_approval_chat_id"), approvalChat))

	templateStatus := "Default"
	if cfg.TelegramMessageTemplate != b.defaultChatCfg.TelegramMessageTemplate {
		templateStatus = "Custom"
	}
	builder.WriteString(fmt.Sprintf(b.localizer.GetMessage(lang, "settings_format"), b.localizer.GetMessage(lang, "setting_name_telegram_message_template"), templateStatus))

	builder.WriteString(b.localizer.GetMessage(lang, "settings_edit_prompt"))
	msg := tgbotapi.NewMessage(chatID, builder.String())
	msg.ParseMode = tgbotapi.ModeHTML

	approvalStatusText := "Enable Approval"
	if cfg.EnableApprovalSystem {
		approvalStatusText = "Disable Approval"
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_ai_prompt"), "edit_ai_prompt"),
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_post_limit"), "edit_post_limit"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_gemini_model"), "edit_gemini_model"),
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_msg_template"), "edit_msg_template"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_rss_max_age"), "edit_rss_max_age"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit_approval_chat_id"), "edit_approval_chat_id"),
			tgbotapi.NewInlineKeyboardButtonData(approvalStatusText, "toggle_approval_system"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_manage_sources"), "manage_sources"),
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_manage_topics"), "manage_topics"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_refresh"), "refresh_settings"),
		),
	)
	msg.ReplyMarkup = &keyboard
	if _, err_send := b.api.Send(msg); err_send != nil {
		log.Printf("Failed to send settings message: %v", err_send)
	}
}

func (b *TelegramBot) handleCancelCommand(message *tgbotapi.Message) {
	userID := message.From.ID
	lang := b.getLangForChat(message.Chat.ID)
	b.stateMutex.Lock()
	if _, inState := b.userStates[userID]; inState {
		delete(b.userStates, userID)
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "setting_update_cancelled"))
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send cancel confirmation: %v", err)
		}
	}
	b.stateMutex.Unlock()
}