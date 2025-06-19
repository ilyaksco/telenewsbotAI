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
			b.clearUserState(userID)
			log.Printf("Summary for pending article %d updated by user %d.", articleID, userID)
			
			pendingArticle, _ := b.storage.GetPendingArticle(articleID)
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
			
			responseMsg := tgbotapi.NewMessage(message.Chat.ID, moderationText)
			responseMsg.ParseMode = tgbotapi.ModeHTML
			responseMsg.ReplyMarkup = &keyboard
			b.api.Send(responseMsg)
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