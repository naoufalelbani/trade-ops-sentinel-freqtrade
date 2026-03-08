package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"trade-ops-sentinel/internal/services"
)

func selectDuration(key string) time.Duration {
	return services.SelectDuration(key)
}

func durationLabel(key string) string {
	return services.DurationLabel(key)
}

func parseCumProfitWindowInput(raw string) (time.Duration, string, string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 2 {
		return 0, "", "", false
	}
	unit := s[len(s)-1]
	if unit != 'h' && unit != 'd' {
		return 0, "", "", false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, "", "", false
	}
	switch unit {
	case 'h':
		if n > 24*30 {
			return 0, "", "", false
		}
		token := fmt.Sprintf("%dh", n)
		return time.Duration(n) * time.Hour, token, token, true
	case 'd':
		if n > 90 {
			return 0, "", "", false
		}
		token := fmt.Sprintf("%dd", n)
		return time.Duration(n) * 24 * time.Hour, token, token, true
	default:
		return 0, "", "", false
	}
}

func setAwaitingCustomCumProfitWindow(chatID int64, v bool) {
	customCumProfitInput.mu.Lock()
	defer customCumProfitInput.mu.Unlock()
	if v {
		customCumProfitInput.awaiting[chatID] = true
		return
	}
	delete(customCumProfitInput.awaiting, chatID)
}

func isAwaitingCustomCumProfitWindow(chatID int64) bool {
	customCumProfitInput.mu.Lock()
	defer customCumProfitInput.mu.Unlock()
	return customCumProfitInput.awaiting[chatID]
}

func parseCumProfitGranularity(token string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "m":
		return "minutes", "minutes"
	case "t":
		return "trades", "trades"
	case "h":
		return "hours", "hours"
	case "d":
		return "days", "days"
	default:
		return "auto", "auto"
	}
}

func parseRangeAgoToken(token string) (time.Duration, string, bool) {
	t := strings.ToLower(strings.TrimSpace(token))
	if t == "now" || t == "0h" || t == "0d" {
		return 0, "now", true
	}
	dur, _, label, ok := parseCumProfitWindowInput(t)
	return dur, label, ok
}

func setRangeFromSelection(chatID int64, token string) {
	customCumProfitRange.mu.Lock()
	defer customCumProfitRange.mu.Unlock()
	customCumProfitRange.from[chatID] = strings.ToLower(strings.TrimSpace(token))
}

func getRangeFromSelection(chatID int64) (string, bool) {
	customCumProfitRange.mu.Lock()
	defer customCumProfitRange.mu.Unlock()
	v, ok := customCumProfitRange.from[chatID]
	return v, ok
}

func clearRangeFromSelection(chatID int64) {
	customCumProfitRange.mu.Lock()
	defer customCumProfitRange.mu.Unlock()
	delete(customCumProfitRange.from, chatID)
}

func setAwaitingCustomCumProfitDateFrom(chatID int64) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	st := customCumProfitDateRange.m[chatID]
	st.AwaitFrom = true
	st.AwaitTo = false
	st.From = time.Time{}
	customCumProfitDateRange.m[chatID] = st
}

func setAwaitingCustomCumProfitDateTo(chatID int64, from time.Time) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	st := customCumProfitDateRange.m[chatID]
	st.AwaitFrom = false
	st.AwaitTo = true
	st.From = from.UTC()
	customCumProfitDateRange.m[chatID] = st
}

func isAwaitingCustomCumProfitDateFrom(chatID int64) bool {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	return customCumProfitDateRange.m[chatID].AwaitFrom
}

func isAwaitingCustomCumProfitDateTo(chatID int64) bool {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	return customCumProfitDateRange.m[chatID].AwaitTo
}

func getCustomCumProfitDateFrom(chatID int64) (time.Time, bool) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	st, ok := customCumProfitDateRange.m[chatID]
	if !ok || st.From.IsZero() {
		return time.Time{}, false
	}
	return st.From, true
}

func clearCustomCumProfitDateRangeState(chatID int64) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	delete(customCumProfitDateRange.m, chatID)
}

func parseUserDateTime(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02 15",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, s, time.UTC)
		if err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func setCalendarRangePhase(chatID int64, phase string) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st := customCumProfitCalendarRange.m[chatID]
	st.Phase = phase
	customCumProfitCalendarRange.m[chatID] = st
}

func setCalendarRangeFromDate(chatID int64, dt time.Time) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st := customCumProfitCalendarRange.m[chatID]
	st.From = dt.UTC()
	customCumProfitCalendarRange.m[chatID] = st
}

func setCalendarRangeToDate(chatID int64, dt time.Time) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st := customCumProfitCalendarRange.m[chatID]
	st.To = dt.UTC()
	customCumProfitCalendarRange.m[chatID] = st
}

func getCalendarRangeState(chatID int64) (calendarRangeState, bool) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st, ok := customCumProfitCalendarRange.m[chatID]
	return st, ok
}

func clearCalendarRangeState(chatID int64) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	delete(customCumProfitCalendarRange.m, chatID)
}

func parseCalendarMonthToken(token string) (int, time.Month, bool) {
	s := strings.TrimSpace(token)
	if len(s) != 6 {
		return 0, 0, false
	}
	y, errA := strconv.Atoi(s[:4])
	m, errB := strconv.Atoi(s[4:])
	if errA != nil || errB != nil || y < 2000 || y > 2100 || m < 1 || m > 12 {
		return 0, 0, false
	}
	return y, time.Month(m), true
}

func parseCalendarDayToken(token string) (time.Time, bool) {
	s := strings.TrimSpace(token)
	if len(s) != 8 {
		return time.Time{}, false
	}
	y, errA := strconv.Atoi(s[:4])
	m, errB := strconv.Atoi(s[4:6])
	d, errC := strconv.Atoi(s[6:])
	if errA != nil || errB != nil || errC != nil {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}
