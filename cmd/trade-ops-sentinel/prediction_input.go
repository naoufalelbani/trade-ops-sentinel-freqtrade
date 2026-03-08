package main

import (
	"strconv"
	"strings"
)

const (
	minPredictionDays = 3
	maxPredictionDays = 365
)

func parsePredictionDaysInput(raw string) (int, bool) {
	s := strings.TrimSpace(raw)
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	if n < minPredictionDays || n > maxPredictionDays {
		return 0, false
	}
	return n, true
}

func setAwaitingPredictionDays(chatID int64, mode string, v bool) {
	customPredictionInput.mu.Lock()
	defer customPredictionInput.mu.Unlock()
	if v {
		m := strings.ToLower(strings.TrimSpace(mode))
		if m != "cum" {
			m = "daily"
		}
		customPredictionInput.modes[chatID] = m
		return
	}
	delete(customPredictionInput.modes, chatID)
}

func isAwaitingPredictionDays(chatID int64) bool {
	customPredictionInput.mu.Lock()
	defer customPredictionInput.mu.Unlock()
	_, ok := customPredictionInput.modes[chatID]
	return ok
}

func predictionModeForChat(chatID int64) string {
	customPredictionInput.mu.Lock()
	defer customPredictionInput.mu.Unlock()
	m := strings.ToLower(strings.TrimSpace(customPredictionInput.modes[chatID]))
	if m != "cum" {
		return "daily"
	}
	return "cum"
}

func setAwaitingCompoundPredictionDays(chatID int64, v bool) {
	customCompoundPredictionInput.mu.Lock()
	defer customCompoundPredictionInput.mu.Unlock()
	if v {
		customCompoundPredictionInput.awaiting[chatID] = true
		return
	}
	delete(customCompoundPredictionInput.awaiting, chatID)
}

func isAwaitingCompoundPredictionDays(chatID int64) bool {
	customCompoundPredictionInput.mu.Lock()
	defer customCompoundPredictionInput.mu.Unlock()
	_, ok := customCompoundPredictionInput.awaiting[chatID]
	return ok
}
