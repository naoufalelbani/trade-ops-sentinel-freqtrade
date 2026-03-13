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
