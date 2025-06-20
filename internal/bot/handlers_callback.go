package bot

import (
	"fmt"
	"log"
	"news-bot/internal/news_fetcher"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *TelegramBot) handleCallbackQuery(callback *tgbotapi.CallbackQuery) {
	if callback.Message == nil {
		log.Printf("Received callback without a message, likely from an inline message. Ignoring. Callback data: %s", callback.Data)
		b.api.Request(tgbotapi.NewCallback(callback.ID, "This action cannot be performed from here."))
		return
	}
	
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID
	messageID := callback.Message.MessageID
	lang := b.getLangForChat(chatID)

	if !b.isChatAdmin(chatID, userID) {
		b.api.Request(tgbotapi.NewCallback(callback.ID, b.localizer.GetMessage(lang, "permission_denied")))
		return
	}

	callbackData := strings.Split(callback.Data, ":")
	action := callbackData[0]
	var data string
	if len(callbackData) > 1 {
		data = strings.Join(callbackData[1:], ":")
	}

	msg := tgbotapi.NewMessage(chatID, "")
	callbackAns := tgbotapi.NewCallback(callback.ID, "")

	switch action {
	case "set_lang":
		newLang := data
		if err := b.storage.UpdateChatConfig(chatID, "language_code", newLang); err != nil {
			log.Printf("Failed to update language_code for chat %d: %v", chatID, err)
			callbackAns.Text = "Failed to update language."
			callbackAns.ShowAlert = true
		} else {
			responseText := "Language has been updated."
			if newLang == "id" {
				responseText = "Bahasa telah berhasil diperbarui."
			}
			editMsg := tgbotapi.NewEditMessageText(chatID, messageID, responseText)
			b.api.Send(editMsg)
			b.handleSettingsCommand(callback.Message)
		}

	case "edit_ai_prompt":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingAIPrompt})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_ai_prompt")
		b.api.Send(msg)
	case "edit_post_limit":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingPostLimit})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_post_limit")
		b.api.Send(msg)
	case "edit_rss_max_age":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingRSSMaxAge})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_rss_max_age")
		b.api.Send(msg)
	case "edit_gemini_model":
		b.sendModelSelectionMenu(chatID, messageID)
	case "edit_schedule":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingSchedule})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_schedule")
		b.api.Send(msg)
	case "edit_msg_template":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingMessageTemplate})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_msg_template")
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
	case "toggle_approval_system":
		cfg, err := b.storage.GetChatConfig(chatID)
		if err != nil {
			log.Printf("Error getting chat config for %d: %v", chatID, err)
			return
		}
		newValue := !cfg.EnableApprovalSystem
		if err := b.storage.UpdateChatConfig(chatID, "enable_approval_system", newValue); err != nil {
			log.Printf("Failed to update enable_approval_system for chat %d: %v", chatID, err)
		}
		b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		b.handleSettingsCommand(callback.Message)

	case "edit_approval_chat_id":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingApprovalChatID})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_approval_chat_id")
		b.api.Send(msg)

	case "set_gemini_model":
		if err := b.storage.UpdateChatConfig(chatID, "gemini_model", data); err != nil {
			log.Printf("Failed to update gemini_model in db for chat %d: %v", chatID, err)
		}
		successMsg := tgbotapi.NewEditMessageText(chatID, messageID, b.localizer.GetMessage(lang, "setting_updated_success"))
		b.api.Send(successMsg)

	case "refresh_settings":
		b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		b.handleSettingsCommand(callback.Message)
		callbackAns.Text = "Settings Refreshed"

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
		if err := b.storage.DeleteNewsSource(sourceID, chatID); err != nil {
			log.Printf("Failed to delete source with id %d for chat %d: %v", sourceID, chatID, err)
		}
		callbackAns.Text = b.localizer.GetMessage(lang, "source_deleted_success")
		b.handleDeleteSourceMenu(chatID, messageID)

	case "chose_source_type":
		sourceType := data
		state := &ConversationState{Step: StateAwaitingSourceURL, PendingSource: news_fetcher.Source{Type: sourceType}}
		b.setUserState(userID, state)
		editMsg := tgbotapi.NewEditMessageText(chatID, messageID, b.localizer.GetMessage(lang, "ask_source_url"))
		b.api.Send(editMsg)
	case "chose_topic_for_source":
		topicID, _ := strconv.ParseInt(data, 10, 64)
		b.stateMutex.Lock()
		defer b.stateMutex.Unlock()

		state, ok := b.userStates[userID]
		if ok && state.Step == StateAwaitingTopicSelection {
			state.PendingSource.TopicID = topicID
			var responseText string
			if err := b.storage.AddNewsSource(chatID, state.PendingSource); err != nil {
				log.Printf("Failed to add new source to db for chat %d: %v", chatID, err)
				responseText = "Failed to add source. The URL might already exist for this chat."
			} else {
				responseText = b.localizer.GetMessage(lang, "source_added_success")
			}
			delete(b.userStates, userID)
			finalMsg := tgbotapi.NewEditMessageText(chatID, messageID, responseText)
			b.api.Send(finalMsg)
		}

	case "manage_topics":
		b.sendTopicsMenu(chatID, messageID)
	case "view_topics_list":
		b.handleViewTopicsList(chatID, messageID)
	case "add_new_topic":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingTopicName})
		msg := tgbotapi.NewEditMessageText(chatID, messageID, "Please send the new topic name.")
		b.api.Send(msg)
	case "manage_delete_topic_menu":
		b.sendDeleteTopicMenu(chatID, messageID)
	case "delete_topic":
		topicID, _ := strconv.ParseInt(data, 10, 64)
		inUse, err := b.storage.IsTopicInUse(topicID, chatID)
		if err != nil {
			log.Printf("Error checking if topic %d is in use for chat %d: %v", topicID, chatID, err)
			callbackAns.Text = "An error occurred."
		} else if inUse {
			callbackAns.Text = b.localizer.GetMessage(lang, "delete_topic_in_use")
			callbackAns.ShowAlert = true
		} else {
			if err := b.storage.DeleteTopic(topicID, chatID); err != nil {
				log.Printf("Failed to delete topic %d for chat %d: %v", topicID, chatID, err)
				callbackAns.Text = "Failed to delete topic."
			} else {
				callbackAns.Text = b.localizer.GetMessage(lang, "delete_topic_success")
			}
			b.sendDeleteTopicMenu(chatID, messageID)
		}

	case "approve_article":
		b.handleApproveArticle(callback)
	case "reject_article":
		b.handleRejectArticle(callback)
	case "edit_article":
		b.handleEditArticle(callback)

	case "cancel_edit":
		b.sendSourcesMenu(chatID, messageID)
	case "back_to_settings":
		b.api.Request(tgbotapi.NewDeleteMessage(chatID, messageID))
		b.handleSettingsCommand(callback.Message)
	}

	if callbackAns.Text != "" {
		if _, err := b.api.Request(callbackAns); err != nil {
			log.Printf("Failed to answer callback query: %v", err)
		}
	}
}

func (b *TelegramBot) handleApproveArticle(callback *tgbotapi.CallbackQuery) {
	articleID, _ := strconv.ParseInt(strings.Split(callback.Data, ":")[1], 10, 64)

	pendingArticle, err := b.storage.GetPendingArticle(articleID)
	if err != nil {
		log.Printf("Failed to get pending article %d: %v", articleID, err)
		b.api.Request(tgbotapi.NewCallback(callback.ID, "This article has already been processed."))
		return
	}

	lang := b.getLangForChat(pendingArticle.ChatID)
	if !b.isChatAdmin(pendingArticle.ChatID, callback.From.ID) {
		b.api.Request(tgbotapi.NewCallback(callback.ID, b.localizer.GetMessage(lang, "permission_denied")))
		return
	}

	chatCfg, err := b.storage.GetChatConfig(pendingArticle.ChatID)
	if err != nil {
		log.Printf("Could not get config for chat %d to approve article: %v", pendingArticle.ChatID, err)
		return
	}

	topic, err := b.storage.GetTopicByName(pendingArticle.ChatID, pendingArticle.TopicName)
	if err != nil {
		log.Printf("Failed to get topic destination for '%s' in chat %d: %v", pendingArticle.TopicName, pendingArticle.ChatID, err)
	}

	articleToPost := &news_fetcher.Article{
		Title:           pendingArticle.Title,
		Link:            pendingArticle.Link,
		ImageURL:        pendingArticle.ImageURL,
		PublicationTime: &pendingArticle.CreatedAt,
	}

	var source news_fetcher.Source
	if topic != nil {
		source = news_fetcher.Source{
			ChatID:            pendingArticle.ChatID,
			URL:               "https://" + pendingArticle.SourceName,
			TopicName:         pendingArticle.TopicName,
			DestinationChatID: topic.DestinationChatID,
			ReplyToMessageID:  topic.ReplyToMessageID,
		}
	} else {
		source = news_fetcher.Source{
			ChatID:    pendingArticle.ChatID,
			URL:       "https://" + pendingArticle.SourceName,
			TopicName: pendingArticle.TopicName,
		}
	}

	if err := b.sendArticleToChannel(articleToPost, pendingArticle.Summary, source, chatCfg); err != nil {
		log.Printf("Failed to send approved article to channel for chat %d: %v", pendingArticle.ChatID, err)
		return
	}

	if err := b.storage.MarkAsPosted(pendingArticle.Link, pendingArticle.ChatID); err != nil {
		log.Printf("CRITICAL: Failed to mark approved article as posted for chat %d: %v", pendingArticle.ChatID, err)
	}
	b.storage.DeletePendingArticle(articleID)

	approvedText := fmt.Sprintf("%s\n\n%s", callback.Message.Text, fmt.Sprintf(b.localizer.GetMessage(lang, "approval_action_approved"), callback.From.FirstName))
	editMsg := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, approvedText)
	editMsg.ParseMode = tgbotapi.ModeHTML
	editMsg.ReplyMarkup = nil
	b.api.Send(editMsg)
}

func (b *TelegramBot) handleRejectArticle(callback *tgbotapi.CallbackQuery) {
	articleID, _ := strconv.ParseInt(strings.Split(callback.Data, ":")[1], 10, 64)

	pendingArticle, err := b.storage.GetPendingArticle(articleID)
	if err != nil {
		log.Printf("Failed to get pending article %d for rejection: %v", articleID, err)
		b.api.Request(tgbotapi.NewCallback(callback.ID, "This article has already been processed."))
		return
	}

	lang := b.getLangForChat(pendingArticle.ChatID)
	if !b.isChatAdmin(pendingArticle.ChatID, callback.From.ID) {
		b.api.Request(tgbotapi.NewCallback(callback.ID, b.localizer.GetMessage(lang, "permission_denied")))
		return
	}

	if err := b.storage.MarkAsPosted(pendingArticle.Link, pendingArticle.ChatID); err != nil {
		log.Printf("Failed to mark rejected article as posted for chat %d: %v", pendingArticle.ChatID, err)
	}
	b.storage.DeletePendingArticle(articleID)

	rejectedText := fmt.Sprintf("%s\n\n%s", callback.Message.Text, fmt.Sprintf(b.localizer.GetMessage(lang, "approval_action_rejected"), callback.From.FirstName))
	editMsg := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, rejectedText)
	editMsg.ParseMode = tgbotapi.ModeHTML
	editMsg.ReplyMarkup = nil
	b.api.Send(editMsg)
}

func (b *TelegramBot) handleEditArticle(callback *tgbotapi.CallbackQuery) {
	articleID, _ := strconv.ParseInt(strings.Split(callback.Data, ":")[1], 10, 64)

	pendingArticle, err := b.storage.GetPendingArticle(articleID)
	if err != nil {
		log.Printf("Failed to get pending article %d for edit: %v", articleID, err)
		b.api.Request(tgbotapi.NewCallback(callback.ID, "This article has already been processed."))
		return
	}

	lang := b.getLangForChat(pendingArticle.ChatID)
	if !b.isChatAdmin(pendingArticle.ChatID, callback.From.ID) {
		b.api.Request(tgbotapi.NewCallback(callback.ID, b.localizer.GetMessage(lang, "permission_denied")))
		return
	}

	b.setUserState(callback.From.ID, &ConversationState{
		Step:                StateAwaitingArticleEdit,
		PendingArticleID:    articleID,
		OriginalMessageID:   callback.Message.MessageID,
		OriginalChatID:      callback.Message.Chat.ID,
		OriginalMessageText: callback.Message.Text,
	})

	msg := tgbotapi.NewMessage(callback.Message.Chat.ID, b.localizer.GetMessage(lang, "ask_for_edited_summary"))
	msg.ReplyToMessageID = callback.Message.MessageID
	b.api.Send(msg)
}