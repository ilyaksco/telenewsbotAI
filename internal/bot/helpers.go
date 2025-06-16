package bot

import "log"

func (b *TelegramBot) isAdmin(userID int64) bool {
	b.configMutex.RLock()
	superAdminID := b.cfg.SuperAdminID
	b.configMutex.RUnlock()
	if userID == superAdminID {
		return true
	}
	isAdmin, err := b.storage.IsUserAdmin(userID)
	if err != nil {
		log.Printf("Could not check admin status for user %d: %v", userID, err)
		return false
	}
	return isAdmin
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

func (b *TelegramBot) getLang() string {
	b.configMutex.RLock()
	defer b.configMutex.RUnlock()
	return b.cfg.DefaultLanguage
}