package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	telegramiface "trade-ops-sentinel/internal/interfaces/telegram"
)

func telegramLoop(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState) {
	if notifier.token == "" || notifier.chatID == "" {
		return
	}

	offset := 0
	for {
		updates, next, err := notifier.GetUpdates(ctx, offset)
		if err != nil {
			log.Printf("telegram poll error: %v", err)
			if runtimeAlerts != nil {
				runtimeAlerts.recordError("telegram.poll", err)
			}
			time.Sleep(3 * time.Second)
			continue
		}
		offset = next

		for _, upd := range updates {
			// Handle updates asynchronously so one heavy report does not block all Telegram interactions.
			go handleTelegramUpdate(ctx, cfg, binance, notifier, state, upd)
		}
	}
}

func handleTelegramUpdate(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState, upd tgUpdate) {
	started := time.Now()
	defer logTiming("telegram_update", started)
	if upd.Message != nil {
		if !notifier.allowedChat(upd.Message.Chat.ID) {
			return
		}
		if strings.TrimSpace(upd.Message.Text) == "" {
			return
		}
		rawText := strings.TrimSpace(upd.Message.Text)
		if !strings.HasPrefix(rawText, "/") && isAwaitingCompoundPredictionDays(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				setAwaitingCompoundPredictionDays(upd.Message.Chat.ID, false)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Compound prediction input canceled.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			days, ok := parsePredictionDaysInput(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("Invalid days. Type a number between `%d` and `%d` (or `cancel`).", minPredictionDays, maxPredictionDays), GetMenuRegistry().GetKeyboard("prediction_days_menu"))
				return
			}
			setAwaitingCompoundPredictionDays(upd.Message.Chat.ID, false)
			chartTheme := state.getChartTheme("dark")
			chartSize := state.getChartSize("standard")
			chartGrid := state.getChartGridEnabled(true)
			refreshAction := "chart_compound_custom_" + strconv.Itoa(days) + "d"
			if err := sendCompoundPredictionChart(ctx, cfg, state, binance, notifier, upd.Message.Chat.ID, days, refreshAction, chartTheme, chartSize, chartGrid); err != nil {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("compound chart error: %v", err), GetMenuRegistry().GetKeyboard("menu_charts"))
			}
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingPredictionDays(upd.Message.Chat.ID) {
			mode := predictionModeForChat(upd.Message.Chat.ID)
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				setAwaitingPredictionDays(upd.Message.Chat.ID, "", false)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Prediction input canceled.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			days, ok := parsePredictionDaysInput(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("Invalid days. Type a number between `%d` and `%d` (or `cancel`).", minPredictionDays, maxPredictionDays), GetMenuRegistry().GetKeyboard("prediction_days_menu"))
				return
			}
			setAwaitingPredictionDays(upd.Message.Chat.ID, "", false)
			chartTheme := state.getChartTheme("dark")
			chartSize := state.getChartSize("standard")
			chartGrid := state.getChartGridEnabled(true)
			refreshAction := "chart_predict_custom_" + strconv.Itoa(days) + "d"
			cumulative := false
			if mode == "cum" {
				refreshAction = "chart_predict_cum_custom_" + strconv.Itoa(days) + "d"
				cumulative = true
			}
			if err := sendPredictionChart(ctx, cfg, state, binance, notifier, upd.Message.Chat.ID, days, cumulative, refreshAction, chartTheme, chartSize, chartGrid); err != nil {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("prediction chart error: %v", err), GetMenuRegistry().GetKeyboard("menu_charts"))
			}
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitDateFrom(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Date range input canceled.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			from, ok := parseUserDateTime(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid FROM date. Use: `YYYY-MM-DD HH:MM` (UTC), example `2026-03-05 14:30`.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			if from.After(time.Now().UTC()) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "FROM date cannot be in the future.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			setAwaitingCustomCumProfitDateTo(upd.Message.Chat.ID, from)
			safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("FROM set to %s UTC. Now type TO date/time (`YYYY-MM-DD HH:MM`).", from.Format("2006-01-02 15:04")), GetMenuRegistry().GetKeyboard("menu_charts"))
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitDateTo(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Date range input canceled.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			to, ok := parseUserDateTime(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid TO date. Use: `YYYY-MM-DD HH:MM` (UTC), example `2026-03-06 09:00`.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			from, okFrom := getCustomCumProfitDateFrom(upd.Message.Chat.ID)
			if !okFrom {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Please start again: choose date range first.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			if !to.After(from) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid TO date. TO must be after FROM.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			if to.After(time.Now().UTC()) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "TO date cannot be in the future.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
			safeSendToChat(
				notifier,
				upd.Message.Chat.ID,
				fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")),
				customCumProfitDateRangeGranularityKeyboard(from.Unix(), to.Unix()),
			)
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitWindow(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				setAwaitingCustomCumProfitWindow(upd.Message.Chat.ID, false)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Custom cumulative profit input canceled.", GetMenuRegistry().GetKeyboard("menu_charts"))
				return
			}
			_, token, label, ok := parseCumProfitWindowInput(rawText)
			if !ok {
				safeSendToChat(
					notifier,
					upd.Message.Chat.ID,
					"Invalid window. Type like `36h`, `72h`, `3d`, `10d` (or `cancel`).",
					customCumProfitWindowKeyboard(),
				)
				return
			}
			setAwaitingCustomCumProfitWindow(upd.Message.Chat.ID, false)
			state.addCustomCumWindow(token)
			_ = state.save()
			safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("Window %s selected. Choose timeline mode:", label), customCumProfitGranularityKeyboard(token))
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingFreqtradeRestartInput(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				setAwaitingFreqtradeRestartInput(upd.Message.Chat.ID, false)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Freqtrade restart input canceled.", GetMenuRegistry().GetKeyboard("menu_main"))
				return
			}
			dur, err := time.ParseDuration(rawText)
			if err != nil {
				// Try again with simpler logic if time.ParseDuration fails (e.g. 1d -> 24h)
				raw := strings.ToLower(rawText)
				if strings.HasSuffix(raw, "d") {
					days, errD := strconv.Atoi(strings.TrimSuffix(raw, "d"))
					if errD == nil {
						dur = time.Duration(days) * 24 * time.Hour
						err = nil
					}
				}
			}
			if err != nil || dur <= 0 {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid duration. Type like `10m`, `1h`, `1d` (or `cancel`).", GetMenuRegistry().GetKeyboard("menu_main"))
				return
			}
			setAwaitingFreqtradeRestartInput(upd.Message.Chat.ID, false)
			restartAt := time.Now().UTC().Add(dur)
			state.setFreqtradeRestartAt(restartAt)
			_ = state.save()
			safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("✅ Freqtrade restart scheduled at %s UTC (in %v).", restartAt.Format("15:04:05"), dur.Round(time.Second)), GetMenuRegistry().GetKeyboard("menu_main"))
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingPnLHistoryInput(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				setAwaitingPnLHistoryInput(upd.Message.Chat.ID, false)
				safeSendToChat(notifier, upd.Message.Chat.ID, "PnL History input canceled.", GetMenuRegistry().GetKeyboard("menu_reports"))
				return
			}
			days, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(rawText), "d"))
			if err != nil || days <= 0 {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid number of days. Type like `14`, `60`, `90d` (or `cancel`).", GetMenuRegistry().GetKeyboard("menu_reports"))
				return
			}
			setAwaitingPnLHistoryInput(upd.Message.Chat.ID, false)
			safeSendPnLHistoryReport(ctx, cfg, binance, notifier, upd.Message.Chat.ID, days, state)
			return
		}
		text := normalizeCommand(rawText)
		mctx := &MenuContext{
			Ctx:      ctx,
			Cfg:      cfg,
			Binance:  binance,
			Notifier: notifier,
			State:    state,
			Update:   upd,
		}
		if handler, ok := GetMenuRegistry().FindHandler(text); ok {
			if err := handler(mctx); err != nil {
				log.Printf("handler error for %s: %v", text, err)
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("Error: %v", err), GetMenuRegistry().GetKeyboard("menu_main"))
			}
			return
		}

		safeSendToChat(notifier, upd.Message.Chat.ID, "Unknown command.\n\n"+helpText(), defaultReplyKeyboard())
		return
	}

	if upd.CallbackQuery == nil || upd.CallbackQuery.Message == nil {
		return
	}
	chatID := upd.CallbackQuery.Message.Chat.ID
	if !notifier.allowedChat(chatID) {
		return
	}

	safeAnswerCallback(notifier, upd.CallbackQuery.ID, "Processing...")
	data := upd.CallbackQuery.Data
	if strings.HasPrefix(data, "refresh_") {
		next := strings.TrimSpace(strings.TrimPrefix(data, "refresh_"))
		if next != "" {
			data = next
		}
	}

	mctx := &MenuContext{
		Ctx:      ctx,
		Cfg:      cfg,
		Binance:  binance,
		Notifier: notifier,
		State:    state,
		Update:   upd,
	}

	if handler, ok := GetMenuRegistry().FindHandler(data); ok {
		if err := handler(mctx); err != nil {
			log.Printf("handler error for %s: %v", data, err)
			safeSendToChat(notifier, chatID, fmt.Sprintf("Error: %v", err), GetMenuRegistry().GetKeyboard("menu_main"))
		}
		return
	}

	log.Printf("unknown telegram callback action: %q", data)
	safeSendToChat(notifier, chatID, "Unknown action", GetMenuRegistry().GetKeyboard("menu_main"))
}

func normalizeCommand(raw string) string {
	return telegramiface.NormalizeCommand(raw)
}

func sendPredictionChart(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, notifier *TelegramNotifier, chatID int64, horizonDays int, cumulative bool, refreshAction, chartTheme, chartSize string, chartGrid bool) error {
	var (
		labels   []string
		history  []float64
		forecast []float64
		unit     string
	)
	if cumulative {
		labels, history, forecast, unit = predictPnLCumulativeSeries(ctx, cfg, state, binance, horizonDays)
	} else {
		labels, history, forecast, unit = predictPnLSeries(ctx, cfg, state, binance, horizonDays)
	}
	if len(labels) == 0 {
		safeSendToChat(notifier, chatID, "Not enough daily data to build prediction yet (need at least ~14 daily points).", GetMenuRegistry().GetKeyboard("menu_charts"))
		return nil
	}
	title := fmt.Sprintf("PnL Forecast (next %dd)", horizonDays)
	if cumulative {
		title = fmt.Sprintf("Cumulative PnL Forecast (next %dd)", horizonDays)
	}
	chartURL := buildForecastChartURL(title, labels, history, forecast, unit, chartTheme, chartSize, chartGrid)
	safeSendPhotoToChatWithMarkup(notifier, chatID, chartURL, title, chartRefreshKeyboard(refreshAction))
	return nil
}

func safeSendPnLHistoryReport(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, chatID int64, days int, state *MonitorState) {
	report, err := buildPnLHistoryTable(ctx, cfg, state, binance, days)
	if err != nil {
		safeSendToChat(notifier, chatID, fmt.Sprintf("PnL History error: %v", err), GetMenuRegistry().GetKeyboard("pnl_history_menu"))
		return
	}
	safeSendPreLargeToChat(notifier, chatID, report, GetMenuRegistry().GetKeyboard("pnl_history_menu"))
}
