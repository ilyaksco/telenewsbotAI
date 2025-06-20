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
	lang := b.getLang()
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	cmd := message.Command()

	// MODIFIED: Added fetch_now and fetch_stop
	protectedCommands := map[string]bool{"settings": true, "setadmin": true, "cancel": true, "fetch_now": true, "fetch_stop": true}
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
	case "analyzelinks":
		b.handleAnalyzeLinksCommand(message)
		return
	case "setadmin":
		b.handleSetAdminCommand(message)
		return
	case "set_target":
		b.handleSetTargetCommand(message)
		return
	case "fetch_now":
		b.handleFetchNowCommand(message)
		return
	case "fetch_stop": // ADDED
		b.handleFetchStopCommand(message)
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

func (b *TelegramBot) handleFetchNowCommand(message *tgbotapi.Message) {
	lang := b.getLang()
	b.fetchingMutex.Lock()
	if b.isFetching {
		b.fetchingMutex.Unlock()
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "fetch_now_already_running"))
		b.api.Send(msg)
		return
	}
	b.fetchingMutex.Unlock()

	msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "fetch_now_started"))
	b.api.Send(msg)
	go b.fetchAndPostNews(context.Background(), message.Chat.ID)
}

// ADDED: New function to handle fetch_stop
func (b *TelegramBot) handleFetchStopCommand(message *tgbotapi.Message) {
	lang := b.getLang()
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
	lang := b.getLang()
	if !b.isAdmin(message.From.ID) {
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "permission_denied"))
		b.api.Send(msg)
		return
	}

	args := message.CommandArguments()
	parts := strings.Fields(args)

	if len(parts) != 3 {
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "set_target_usage"))
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
		return
	}

	topicName := parts[0]
	chatIDStr := parts[1]
	messageIDStr := parts[2]

	chatID, errChat := strconv.ParseInt(chatIDStr, 10, 64)
	messageID, errMsg := strconv.ParseInt(messageIDStr, 10, 64)

	if errChat != nil || errMsg != nil {
		msg := tgbotapi.NewMessage(message.Chat.ID, b.localizer.GetMessage(lang, "set_target_invalid_id"))
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
		return
	}

	topic, err := b.storage.GetTopicByName(topicName)
	if err != nil {
		msgText := fmt.Sprintf(b.localizer.GetMessage(lang, "set_target_topic_not_found"), topicName)
		msg := tgbotapi.NewMessage(message.Chat.ID, msgText)
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
		return
	}

	err = b.storage.UpdateTopicDestination(topic.ID, chatID, messageID)
	if err != nil {
		log.Printf("Failed to update topic destination: %v", err)
		msg := tgbotapi.NewMessage(message.Chat.ID, "Error saving destination.")
		b.api.Send(msg)
		return
	}

	successText := fmt.Sprintf(b.localizer.GetMessage(lang, "set_target_success"), topicName, chatID, messageID)
	msg := tgbotapi.NewMessage(message.Chat.ID, successText)
	msg.ParseMode = tgbotapi.ModeHTML
	b.api.Send(msg)
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
	displayOrder := []string{"super_admin_id", "telegram_chat_id", "ai_prompt", "post_limit_per_run", "schedule_interval_minutes", "rss_max_age_hours", "gemini_model", "telegram_message_template", "default_language", "news_sources_file_path", "enable_approval_system", "approval_chat_id"}
	sensitiveKeys := map[string]bool{"telegram_bot_token": true, "gemini_api_key": true}

	for _, key := range displayOrder {
		value, ok := settings[key]
		if !ok {
			continue
		}
		if sensitiveKeys[key] {
			value = "********"
		}
		if key == "approval_chat_id" && value == "0" {
			value = "Not Set (Defaults to Superadmin)"
		}
		if key == "rss_max_age_hours" {
			value = fmt.Sprintf("%s hours", value)
		}
		displayName := b.localizer.GetMessage(lang, "setting_name_"+key)
		format := b.localizer.GetMessage(lang, "settings_format")
		builder.WriteString(fmt.Sprintf(format, displayName, value))
	}
	builder.WriteString(b.localizer.GetMessage(lang, "settings_edit_prompt"))
	msg := tgbotapi.NewMessage(message.Chat.ID, builder.String())
	msg.ParseMode = tgbotapi.ModeHTML

	approvalStatusText := "Enable Approval"
	if b.cfg.EnableApprovalSystem {
		approvalStatusText = "Disable Approval"
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("AI Prompt", "edit_ai_prompt"),
			tgbotapi.NewInlineKeyboardButtonData("Post Limit", "edit_post_limit"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("AI Model", "edit_gemini_model"),
			tgbotapi.NewInlineKeyboardButtonData("Msg Template", "edit_msg_template"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Schedule", "edit_schedule"),
			tgbotapi.NewInlineKeyboardButtonData("RSS Max Age", "edit_rss_max_age"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Approval Chat ID", "edit_approval_chat_id"),
			tgbotapi.NewInlineKeyboardButtonData(approvalStatusText, "toggle_approval_system"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Manage Sources", "manage_sources"),
			tgbotapi.NewInlineKeyboardButtonData("Manage Topics", "manage_topics"),
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