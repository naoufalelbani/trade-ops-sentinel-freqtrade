package main

func setAwaitingFreqtradeRestartInput(chatID int64, v bool) {
	customFreqtradeRestartInput.mu.Lock()
	defer customFreqtradeRestartInput.mu.Unlock()
	if v {
		customFreqtradeRestartInput.awaiting[chatID] = true
		return
	}
	delete(customFreqtradeRestartInput.awaiting, chatID)
}

func isAwaitingFreqtradeRestartInput(chatID int64) bool {
	customFreqtradeRestartInput.mu.Lock()
	defer customFreqtradeRestartInput.mu.Unlock()
	_, ok := customFreqtradeRestartInput.awaiting[chatID]
	return ok
}

func setAwaitingPnLHistoryInput(chatID int64, v bool) {
	customPnLHistoryInput.mu.Lock()
	defer customPnLHistoryInput.mu.Unlock()
	if v {
		customPnLHistoryInput.awaiting[chatID] = true
		return
	}
	delete(customPnLHistoryInput.awaiting, chatID)
}

func isAwaitingPnLHistoryInput(chatID int64) bool {
	customPnLHistoryInput.mu.Lock()
	defer customPnLHistoryInput.mu.Unlock()
	_, ok := customPnLHistoryInput.awaiting[chatID]
	return ok
}
