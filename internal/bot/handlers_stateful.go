package bot

import (
	"log"
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
	case StateAwaitingSourceURL:
		state.PendingSource.URL = message.Text
		if state.PendingSource.Type == "rss" {
			// Mengirim 0 sebagai messageID memaksa bot mengirim pesan baru, bukan mengedit.
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
		// Mengirim 0 sebagai messageID memaksa bot mengirim pesan baru, bukan mengedit.
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