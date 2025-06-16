package bot

import (
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
	// Menghapus "topics" dari perintah yang dilindungi
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
	// Menghapus case untuk "/topics"
	case "analyzelinks":
		b.handleAnalyzeLinksCommand(message)
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
	// Menambahkan tombol "Manage Topics" ke keyboard
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
			tgbotapi.NewInlineKeyboardButtonData("Manage Sources", "manage_sources"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Manage Topics", "manage_topics"),
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