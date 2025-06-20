package bot

import (
	"fmt"
	"log"
	"news-bot/internal/news_fetcher"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func (b *TelegramBot) handleStatefulMessage(message *tgbotapi.Message, state *ConversationState) {
	userID := message.From.ID
	lang := b.getLang()
	msg := tgbotapi.NewMessage(message.Chat.ID, "")
	operationSuccessful := false

	switch state.Step {
	case StateAwaitingTargetForward:
		if message.ForwardFromChat == nil {
			msg.Text = b.localizer.GetMessage(lang, "set_target_not_a_forward")
			b.api.Send(msg)
			return
		}
		chatID := message.ForwardFromChat.ID
		messageID := message.ForwardFromMessageID

		topic, err := b.storage.GetTopicByName(state.PendingTopicName)
		if err != nil {
			log.Printf("Error getting topic by name in stateful handler: %v", err)
			b.clearUserState(userID)
			return
		}

		err = b.storage.UpdateTopicDestination(topic.ID, chatID, int64(messageID))
		if err != nil {
			log.Printf("Failed to update topic destination: %v", err)
			msg.Text = "Error saving destination."
		} else {
			successText := fmt.Sprintf(b.localizer.GetMessage(lang, "set_target_success"), state.PendingTopicName, chatID, messageID)
			msg.Text = successText
			msg.ParseMode = tgbotapi.ModeHTML
		}
		b.clearUserState(userID)

	case StateAwaitingAIPrompt:
		b.configMutex.Lock()
		b.cfg.AiPrompt = message.Text
		b.configMutex.Unlock()
		if err := b.storage.SetSetting("ai_prompt", message.Text); err != nil {
			log.Printf("Failed to update ai_prompt in db: %v", err)
		}
		b.reloadSummarizer()
		operationSuccessful = true
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
			operationSuccessful = true
		}
	case StateAwaitingMessageTemplate:
		b.configMutex.Lock()
		b.cfg.TelegramMessageTemplate = message.Text
		b.configMutex.Unlock()
		if err := b.storage.SetSetting("telegram_message_template", message.Text); err != nil {
			log.Printf("Failed to update telegram_message_template in db: %v", err)
		}
		operationSuccessful = true
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
			operationSuccessful = true
		}
	case StateAwaitingRSSMaxAge:
		hours, err := strconv.Atoi(message.Text)
		if err != nil || hours <= 0 {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			b.configMutex.Lock()
			b.cfg.RSSMaxAgeHours = hours
			b.configMutex.Unlock()
			if err := b.storage.SetSetting("rss_max_age_hours", message.Text); err != nil {
				log.Printf("Failed to update rss_max_age_hours in db: %v", err)
			}
			operationSuccessful = true
		}
	case StateAwaitingApprovalChatID:
		chatID, err := strconv.ParseInt(message.Text, 10, 64)
		if err != nil {
			msg.Text = b.localizer.GetMessage(lang, "invalid_input_not_a_number")
		} else {
			b.configMutex.Lock()
			b.cfg.ApprovalChatID = chatID
			b.configMutex.Unlock()
			if err := b.storage.SetSetting("approval_chat_id", message.Text); err != nil {
				log.Printf("Failed to update approval_chat_id in db: %v", err)
			}
			operationSuccessful = true
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

			// MODIFIED: Handle the error from GetPendingArticle
			pendingArticle, err := b.storage.GetPendingArticle(articleID)
			if err != nil {
				log.Printf("Could not get pending article %d after update (it may have been processed): %v", articleID, err)
				msg.Text = "Could not process edit. The article may have already been approved or rejected."
				b.clearUserState(userID)
				// The break is sufficient, the message will be sent at the end of the function.
				break
			}

			// This code below will only run if GetPendingArticle is successful
			articleToFormat := &news_fetcher.Article{Title: pendingArticle.Title, Link: pendingArticle.Link}
			sourceToFormat := news_fetcher.Source{URL: "https://" + pendingArticle.SourceName, TopicName: pendingArticle.TopicName}

			newCaption := b.formatCaption(articleToFormat, newSummary, sourceToFormat)
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
			b.sendTopicSelectionMenu(message.Chat.ID, 0, userID)
			state.Step = StateAwaitingTopicSelection
			b.setUserState(userID, state)
		} else {
			state.Step = StateAwaitingSourceSelector
			b.setUserState(userID, state)
			msg.Text = b.localizer.GetMessage(lang, "ask_source_selector")
		}
	case StateAwaitingSourceSelector:
		state.PendingSource.LinkSelector = message.Text
		b.sendTopicSelectionMenu(message.Chat.ID, 0, userID)
		state.Step = StateAwaitingTopicSelection
		b.setUserState(userID, state)
	case StateAwaitingTopicName:
		topicName := message.Text
		if err := b.storage.AddTopic(topicName); err != nil {
			log.Printf("Failed to add new topic to db: %v", err)
			msg.Text = "Failed to add topic. It might already exist."
		} else {
			msg.Text = "Topic successfully added!"
		}
		b.clearUserState(userID)
	}

	if operationSuccessful {
		b.clearUserState(userID)
		b.sendSuccessAndShowSettings(message)
		return
	}

	if msg.Text != "" {
		if _, err := b.api.Send(msg); err != nil {
			log.Printf("Failed to send state response message: %v", err)
		}
	}
}