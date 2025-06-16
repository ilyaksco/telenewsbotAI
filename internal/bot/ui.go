package bot

import (
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *TelegramBot) sendSuccessAndShowSettings(originalMessage *tgbotapi.Message) {
	lang := b.getLang()
	successMsg := tgbotapi.NewMessage(originalMessage.Chat.ID, b.localizer.GetMessage(lang, "setting_updated_success"))
	if _, err := b.api.Send(successMsg); err != nil {
		log.Printf("Failed to send success message: %v", err)
	}
	b.handleSettingsCommand(originalMessage)
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

	availableModels := []struct {
		DisplayName string // Teks yang tampil di tombol
		InternalID  string // ID model resmi dari Google
	}{
		{DisplayName: "Gemini 1.5 Flash Latest", InternalID: "gemini-1.5-flash-8b-latest"},
		{DisplayName: "Gemini 1.5 Flash", InternalID: "gemini-1.5-flash"},
		{DisplayName: "Gemini 2.0 Flash Lite", InternalID: "gemini-2.0-flash-lite"},
		{DisplayName: "Gemini 2.0 Flash", InternalID: "gemini-2.0-flash"},
		{DisplayName: "Gemini 2.5 Flash 0520", InternalID: "gemini-2.5-flash-preview-05-20"},
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, model := range availableModels {
		button := tgbotapi.NewInlineKeyboardButtonData(model.DisplayName, "set_gemini_model:"+model.InternalID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(button))
	}

	// Menambahkan tombol Batal di baris terakhir
	cancelButton := tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_cancel"), "cancel_edit")
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(cancelButton))

	modelKeyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
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
	builder.WriteString("<b>Current News Sources & Topics:</b>\n\n")
	if len(sources) == 0 {
		builder.WriteString(b.localizer.GetMessage(lang, "no_sources_found"))
	} else {
		for _, source := range sources {
			topic := source.TopicName
			if topic == "" {
				topic = "N/A"
			}
			format := "<b>ID:</b> %d\n<b>Topic:</b> %s\n<b>Type:</b> %s\n<b>URL:</b> %s\n\n"
			builder.WriteString(fmt.Sprintf(format, source.ID, topic, source.Type, source.URL))
		}
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_back_to_menu"), "manage_sources")))
	msg := tgbotapi.NewEditMessageText(chatID, messageID, builder.String())
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *TelegramBot) sendTopicsMenu(chatID int64, messageID int) {
	text := "<b>Topic Management</b>\n\nSelect an option:"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("View All Topics", "view_topics_list"),
			tgbotapi.NewInlineKeyboardButtonData("Add New Topic", "add_new_topic"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to Settings", "back_to_settings"),
		),
	)

	var msg tgbotapi.Chattable
	// Jika messageID bukan 0, berarti kita mengedit pesan yang ada (dari callback).
	// Jika 0, kita mengirim pesan baru (dari perintah /settings -> manage_topics).
	if messageID != 0 {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.ParseMode = tgbotapi.ModeHTML
		editMsg.ReplyMarkup = &keyboard
		msg = editMsg
	} else {
		newMsg := tgbotapi.NewMessage(chatID, text)
		newMsg.ParseMode = tgbotapi.ModeHTML
		newMsg.ReplyMarkup = &keyboard
		msg = newMsg
	}

	if _, err := b.api.Send(msg); err != nil {
		log.Printf("Failed to send topics menu: %v", err)
	}
}

func (b *TelegramBot) handleViewTopicsList(chatID int64, messageID int) {
	topics, err := b.storage.GetTopics()
	if err != nil {
		log.Printf("Failed to get topics for viewing: %v", err)
		return
	}

	var builder strings.Builder
	builder.WriteString("<b>Available Topics:</b>\n\n")
	if len(topics) == 0 {
		builder.WriteString("No topics found. Add one first!")
	} else {
		for _, topic := range topics {
			builder.WriteString(fmt.Sprintf("- %s (ID: %d)\n", topic.Name, topic.ID))
		}
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("⬅️ Back to Topics Menu", "manage_topics")))
	msg := tgbotapi.NewEditMessageText(chatID, messageID, builder.String())
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = &keyboard
	b.api.Send(msg)
}

func (b *TelegramBot) sendTopicSelectionMenu(chatID int64, messageID int, userID int64) {
	topics, err := b.storage.GetTopics()
	if err != nil || len(topics) == 0 {
		text := "No topics available. Please add a topic first via /settings -> Manage Topics."
		var msg tgbotapi.Chattable
		if messageID == 0 {
			msg = tgbotapi.NewMessage(chatID, text)
		} else {
			msg = tgbotapi.NewEditMessageText(chatID, messageID, text)
		}
		b.api.Send(msg)
		b.clearUserState(userID)
		return
	}

	text := "Please select a topic for this news source:"
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, topic := range topics {
		btn := tgbotapi.NewInlineKeyboardButtonData(topic.Name, fmt.Sprintf("chose_topic_for_source:%d", topic.ID))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(rows...)
	var msg tgbotapi.Chattable
	if messageID == 0 {
		newMsg := tgbotapi.NewMessage(chatID, text)
		newMsg.ReplyMarkup = &keyboard
		msg = newMsg
	} else {
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, text)
		editMsg.ReplyMarkup = &keyboard
		msg = editMsg
	}

	b.api.Send(msg)
}