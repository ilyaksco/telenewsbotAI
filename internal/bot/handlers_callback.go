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
	case "edit_rss_max_age":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingRSSMaxAge})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_rss_max_age")
		b.api.Send(msg)
	case "edit_gemini_model":
		b.sendModelSelectionMenu(chatID, messageID)
	case "edit_msg_template":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingMessageTemplate})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_new_msg_template")
		msg.ParseMode = tgbotapi.ModeHTML
		b.api.Send(msg)
	case "toggle_approval_system":
		b.configMutex.Lock()
		b.cfg.EnableApprovalSystem = !b.cfg.EnableApprovalSystem
		currentValue := b.cfg.EnableApprovalSystem
		b.configMutex.Unlock()

		if err := b.storage.SetSetting("enable_approval_system", strconv.FormatBool(currentValue)); err != nil {
			log.Printf("Failed to update enable_approval_system in db: %v", err)
		}

		deleteConfig := tgbotapi.NewDeleteMessage(chatID, messageID)
		b.api.Request(deleteConfig)
		b.handleSettingsCommand(callback.Message)

	case "edit_approval_chat_id":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingApprovalChatID})
		msg.Text = b.localizer.GetMessage(lang, "ask_for_approval_chat_id")
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
	case "chose_topic_for_source":
		topicID, _ := strconv.ParseInt(data, 10, 64)
		b.stateMutex.Lock()
		defer b.stateMutex.Unlock()

		state, ok := b.userStates[userID]
		if ok && state.Step == StateAwaitingTopicSelection {
			state.PendingSource.TopicID = topicID
			var responseText string
			if err := b.storage.AddNewsSource(state.PendingSource); err != nil {
				log.Printf("Failed to add new source to db: %v", err)
				responseText = "Failed to add source. The URL might already exist."
			} else {
				responseText = b.localizer.GetMessage(lang, "source_added_success")
			}
			delete(b.userStates, userID)

			finalMsg := tgbotapi.NewEditMessageText(chatID, messageID, responseText)
			b.api.Send(finalMsg)
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
		deleteConfig := tgbotapi.NewDeleteMessage(chatID, messageID)
		b.api.Request(deleteConfig)
		b.handleSettingsCommand(callback.Message)
	case "manage_topics":
		b.sendTopicsMenu(chatID, messageID)
	case "view_topics_list":
		b.handleViewTopicsList(chatID, messageID)
	case "add_new_topic":
		b.setUserState(userID, &ConversationState{Step: StateAwaitingTopicName})
		msg := tgbotapi.NewEditMessageText(chatID, messageID, "Please send the new topic name.")
		b.api.Send(msg)
	}
	if _, err := b.api.Request(callbackAns); err != nil {
		log.Printf("Failed to answer callback query: %v", err)
	}
}

func (b *TelegramBot) handleApproveArticle(callback *tgbotapi.CallbackQuery) {
	lang := b.getLang()
	articleID, _ := strconv.ParseInt(strings.Split(callback.Data, ":")[1], 10, 64)

	pendingArticle, err := b.storage.GetPendingArticle(articleID)
	if err != nil {
		log.Printf("Failed to get pending article %d: %v", articleID, err)
		return
	}

	articleToPost := &news_fetcher.Article{
		Title:           pendingArticle.Title,
		Link:            pendingArticle.Link,
		ImageURL:        pendingArticle.ImageURL,
		PublicationTime: &pendingArticle.CreatedAt, // Approximation, as we don't store original pub time
	}
	source := news_fetcher.Source{
		URL:       "https://" + pendingArticle.SourceName, // Reconstruct for formatting
		TopicName: pendingArticle.TopicName,
	}

	if err := b.sendArticleToChannel(articleToPost, pendingArticle.Summary, source); err != nil {
		log.Printf("Failed to send approved article to channel: %v", err)
		return
	}

	if err := b.storage.MarkAsPosted(pendingArticle.Link); err != nil {
		log.Printf("CRITICAL: Failed to mark approved article as posted: %v", err)
	}
	b.storage.DeletePendingArticle(articleID)

	approvedText := fmt.Sprintf("%s\n\n%s", callback.Message.Text, fmt.Sprintf(b.localizer.GetMessage(lang, "approval_action_approved"), callback.From.FirstName))
	editMsg := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, approvedText)
	editMsg.ParseMode = tgbotapi.ModeHTML
	b.api.Send(editMsg)
}

func (b *TelegramBot) handleRejectArticle(callback *tgbotapi.CallbackQuery) {
	lang := b.getLang()
	articleID, _ := strconv.ParseInt(strings.Split(callback.Data, ":")[1], 10, 64)

	pendingArticle, err := b.storage.GetPendingArticle(articleID)
	if err != nil {
		log.Printf("Failed to get pending article %d for rejection: %v", articleID, err)
		return
	}

	if err := b.storage.MarkAsPosted(pendingArticle.Link); err != nil {
		log.Printf("Failed to mark rejected article as posted: %v", err)
	}
	b.storage.DeletePendingArticle(articleID)

	rejectedText := fmt.Sprintf("%s\n\n%s", callback.Message.Text, fmt.Sprintf(b.localizer.GetMessage(lang, "approval_action_rejected"), callback.From.FirstName))
	editMsg := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, rejectedText)
	editMsg.ParseMode = tgbotapi.ModeHTML
	b.api.Send(editMsg)
}

func (b *TelegramBot) handleEditArticle(callback *tgbotapi.CallbackQuery) {
	lang := b.getLang()
	articleID, _ := strconv.ParseInt(strings.Split(callback.Data, ":")[1], 10, 64)
	b.setUserState(callback.From.ID, &ConversationState{
		Step:             StateAwaitingArticleEdit,
		PendingArticleID: articleID,
	})

	msg := tgbotapi.NewMessage(callback.Message.Chat.ID, b.localizer.GetMessage(lang, "ask_for_edited_summary"))
	msg.ReplyToMessageID = callback.Message.MessageID
	b.api.Send(msg)
}