package bot

import (
	"log"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	
)

func (b *TelegramBot) ensureChatIsConfigured(chatID int64) error {
	isConfigured, err := b.storage.IsChatConfigured(chatID)
	if err != nil {
		return fmt.Errorf("error checking if chat %d is configured: %w", chatID, err)
	}

	if !isConfigured {
		log.Printf("Chat %d is interacting for the first time. Creating default configuration...", chatID)
		if err := b.storage.CreateDefaultChatConfig(chatID, b.defaultChatCfg); err != nil {
			return fmt.Errorf("failed to create default config for new chat %d: %w", chatID, err)
		}
	}
	return nil
}

func (b *TelegramBot) getLangForChat(chatID int64) string {
	cfg, err := b.storage.GetChatConfig(chatID)
	if err != nil {
		// If config not found or any error, fallback to a default language
		return "en"
	}
	return cfg.LanguageCode
}

func (b *TelegramBot) isSuperAdmin(userID int64) bool {
	return userID == b.globalCfg.SuperAdminID
}

func (b *TelegramBot) isChatAdmin(chatID int64, userID int64) bool {
	if b.isSuperAdmin(userID) {
		return true
	}

	if chatID > 0 && chatID == userID {
		return true
	}

	if chatID < 0 {
		// MODIFIED: Use the correct config struct type
		config := tgbotapi.ChatAdministratorsConfig{
			ChatConfig: tgbotapi.ChatConfig{
				ChatID: chatID,
			},
		}
		chatAdmins, err := b.api.GetChatAdministrators(config)
		if err != nil {
			log.Printf("Failed to get admins for chat %d: %v", chatID, err)
			return false
		}

		for _, admin := range chatAdmins {
			if admin.User.ID == userID {
				return true
			}
		}
	}

	return false
}

func (b *TelegramBot) setUserState(userID int64, state *ConversationState) {
	b.stateMutex.Lock()
	defer b.stateMutex.Unlock()
	b.userStates[userID] = state
}

func (b *TelegramBot) clearUserState(userID int64) {
	b.stateMutex.Lock()
	defer b.stateMutex.Unlock()
	delete(b.userStates, userID)
}