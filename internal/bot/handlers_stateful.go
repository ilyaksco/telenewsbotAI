package bot

import (
	"fmt"
	"log"
	"news-bot/internal/news_fetcher"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *TelegramBot) handleStatefulMessage(message *tgbotapi.Message) {
	userID := message.From.ID
	chatID := message.Chat.ID
	lang := b.getLangForChat(chatID)

	b.stateMutex.Lock()
	state, ok := b.userStates[userID]
	if !ok {
		b.stateMutex.Unlock()
		return
	}
	b.stateMutex.Unlock()

	msg := tgbotapi.NewMessage(chatID, "")
	operationSuccessful := false

	switch state.Step {
	case StateAwaitingAIPrompt:
		if err := b.storage.UpdateChatConfig(chatID, "ai_prompt", message.Text); err != nil {
			log.Printf("Failed to update ai_prompt for chat %d: %v", chatID, err)
		} else {
			operationSuccessful = true
		}
	case StateAwaitingPostLimit:
		limit, err := strconv.Atoi(message.Text)
		if err != nil || limit <= 0 {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			if err := b.storage.UpdateChatConfig(chatID, "post_limit_per_run", limit); err != nil {
				log.Printf("Failed to update post_limit_per_run for chat %d: %v", chatID, err)
			} else {
				operationSuccessful = true
			}
		}
	case StateAwaitingSchedule:
		minutes, err := strconv.Atoi(message.Text)
		if err != nil || minutes <= 0 {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			if err := b.storage.UpdateChatConfig(chatID, "schedule_interval_minutes", minutes); err != nil {
				log.Printf("Failed to update schedule_interval_minutes for chat %d: %v", chatID, err)
			} else {
				if err := b.storage.UpdateLastFetchedTime(chatID, time.Now()); err != nil {
					log.Printf("Failed to reset last_fetched_at for chat %d: %v", chatID, err)
				}
				operationSuccessful = true
			}
		}
	case StateAwaitingMessageTemplate:
		if err := b.storage.UpdateChatConfig(chatID, "message_template", message.Text); err != nil {
			log.Printf("Failed to update telegram_message_template for chat %d: %v", chatID, err)
		} else {
			operationSuccessful = true
		}
	case StateAwaitingRSSMaxAge:
		hours, err := strconv.Atoi(message.Text)
		if err != nil || hours <= 0 {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			if err := b.storage.UpdateChatConfig(chatID, "rss_max_age_hours", hours); err != nil {
				log.Printf("Failed to update rss_max_age_hours for chat %d: %v", chatID, err)
			} else {
				operationSuccessful = true
			}
		}
	case StateAwaitingApprovalChatID:
		approvalChatID, err := strconv.ParseInt(message.Text, 10, 64)
		if err != nil {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			if err := b.storage.UpdateChatConfig(chatID, "approval_chat_id", approvalChatID); err != nil {
				log.Printf("Failed to update approval_chat_id for chat %d: %v", chatID, err)
			} else {
				operationSuccessful = true
			}
		}
	case StateAwaitingArticleEdit:
		newSummary := message.Text
		articleID := state.PendingArticleID

		if err := b.storage.UpdatePendingArticleSummary(articleID, newSummary); err != nil {
			log.Printf("Failed to update summary for pending article %d: %v", articleID, err)
			msg.Text = "Failed to update summary."
		} else {
			log.Printf("Summary for pending article %d updated by user %d.", articleID, userID)

			if state.OriginalMessageID != 0 {
				disableEdit := tgbotapi.NewEditMessageText(state.OriginalChatID, state.OriginalMessageID, state.OriginalMessageText)
				disableEdit.ReplyMarkup = nil
				b.api.Send(disableEdit)
			}

			pendingArticle, err := b.storage.GetPendingArticle(articleID)
			if err != nil {
				log.Printf("Could not get pending article %d after update (it may have been processed): %v", articleID, err)
				msg.Text = "Could not process edit. The article may have already been approved or rejected."
				b.clearUserState(userID)
				break
			}

			chatCfg, err := b.storage.GetChatConfig(pendingArticle.ChatID)
			if err != nil {
				log.Printf("Could not get config for chat %d to format caption: %v", pendingArticle.ChatID, err)
				b.clearUserState(userID)
				break
			}

			articleToFormat := &news_fetcher.Article{Title: pendingArticle.Title, Link: pendingArticle.Link}
			sourceToFormat := news_fetcher.Source{URL: "https://" + pendingArticle.SourceName, TopicName: pendingArticle.TopicName}
			newCaption := b.formatCaption(articleToFormat, newSummary, sourceToFormat, chatCfg)
			moderationText := fmt.Sprintf("%s\n\n%s", b.localizer.GetMessage(lang, "approval_header_edited"), newCaption)
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_approve"), fmt.Sprintf("approve_article:%d", articleID)),
					tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_edit"), fmt.Sprintf("edit_article:%d", articleID)),
					tgbotapi.NewInlineKeyboardButtonData(b.localizer.GetMessage(lang, "btn_reject"), fmt.Sprintf("reject_article:%d", articleID)),
				),
			)
			b.api.Request(tgbotapi.NewDeleteMessage(message.Chat.ID, message.MessageID))
			responseMsg := tgbotapi.NewMessage(message.Chat.ID, moderationText)
			responseMsg.ParseMode = tgbotapi.ModeHTML
			responseMsg.ReplyMarkup = &keyboard
			b.api.Send(responseMsg)
			b.clearUserState(userID)
		}

	case StateAwaitingSourceURL:
		state.PendingSource.URL = message.Text
		if state.PendingSource.Type == "rss" {
			b.sendTopicSelectionMenu(chatID, 0, userID)
			state.Step = StateAwaitingTopicSelection
			b.setUserState(userID, state)
		} else {
			state.Step = StateAwaitingSourceSelector
			b.setUserState(userID, state)
			msg.Text = b.localizer.GetMessage(lang, "ask_source_selector")
		}
	case StateAwaitingSourceSelector:
		state.PendingSource.LinkSelector = message.Text
		b.sendTopicSelectionMenu(chatID, 0, userID)
		state.Step = StateAwaitingTopicSelection
		b.setUserState(userID, state)
	case StateAwaitingTopicName:
		topicName := message.Text
		if err := b.storage.AddTopic(chatID, topicName); err != nil {
			log.Printf("Failed to add topic to db for chat %d: %v", chatID, err)
			msg.Text = "Failed to add topic. It might already exist in this chat."
		} else {
			msg.Text = "Topic successfully added!"
		}
		b.clearUserState(userID)
	}

	if operationSuccessful {
		b.clearUserState(userID)
		b.sendSuccessAndShowSettings(message)
	}

	if msg.Text != "" {
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send state response message: %v", err)
		}
	}
}