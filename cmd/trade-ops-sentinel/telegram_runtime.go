package main

import (
	telegramiface "trade-ops-sentinel/internal/interfaces/telegram"
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
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
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitDateFrom(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Date range input canceled.", chartsKeyboard())
				return
			}
			from, ok := parseUserDateTime(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid FROM date. Use: `YYYY-MM-DD HH:MM` (UTC), example `2026-03-05 14:30`.", chartsKeyboard())
				return
			}
			if from.After(time.Now().UTC()) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "FROM date cannot be in the future.", chartsKeyboard())
				return
			}
			setAwaitingCustomCumProfitDateTo(upd.Message.Chat.ID, from)
			safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("FROM set to %s UTC. Now type TO date/time (`YYYY-MM-DD HH:MM`).", from.Format("2006-01-02 15:04")), chartsKeyboard())
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitDateTo(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Date range input canceled.", chartsKeyboard())
				return
			}
			to, ok := parseUserDateTime(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid TO date. Use: `YYYY-MM-DD HH:MM` (UTC), example `2026-03-06 09:00`.", chartsKeyboard())
				return
			}
			from, okFrom := getCustomCumProfitDateFrom(upd.Message.Chat.ID)
			if !okFrom {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Please start again: choose date range first.", chartsKeyboard())
				return
			}
			if !to.After(from) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid TO date. TO must be after FROM.", chartsKeyboard())
				return
			}
			if to.After(time.Now().UTC()) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "TO date cannot be in the future.", chartsKeyboard())
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
				safeSendToChat(notifier, upd.Message.Chat.ID, "Custom cumulative profit input canceled.", chartsKeyboard())
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
		text := normalizeCommand(upd.Message.Text)
		switch text {
		case "/menu":
			safeSendToChat(notifier, upd.Message.Chat.ID, "Main keyboard enabled.", defaultReplyKeyboard())
			safeSendToChat(notifier, upd.Message.Chat.ID, "BNB monitor menu:", defaultKeyboard())
		case "/status":
			report, err := buildStatusReport(ctx, cfg, binance, state)
			if err != nil {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("status error: %v", err), defaultKeyboard())
				return
			}
			safeSendToChat(notifier, upd.Message.Chat.ID, report, defaultKeyboard())
		case "/daily":
			if err := sendDailyReport(ctx, cfg, binance, notifier, state); err != nil {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("daily report error: %v", err), defaultKeyboard())
			}
		case "/help":
			safeSendToChat(notifier, upd.Message.Chat.ID, helpText(), defaultReplyKeyboard())
		default:
			safeSendToChat(notifier, upd.Message.Chat.ID, "Unknown command.\n\n"+helpText(), defaultReplyKeyboard())
		}
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

	switch data {
	case "menu", "menu_main":
		safeSendToChat(notifier, chatID, "Main menu", defaultKeyboard())
	case "menu_actions":
		safeSendToChat(notifier, chatID, "Actions menu", actionsKeyboard())
	case "menu_reports":
		safeSendToChat(notifier, chatID, "Reports menu", reportsKeyboard())
	case "menu_charts":
		safeSendToChat(notifier, chatID, "Charts menu", chartsKeyboard())
	case "menu_settings":
		safeSendToChat(notifier, chatID, "Settings menu", settingsKeyboard())
	case "refill_now":
		msg, err := executeManualBNBBuy(ctx, cfg, binance, state, false)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("refill error: %v", err), defaultKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, msg, defaultKeyboard())
	case "force_buy":
		safeSendToChat(notifier, chatID, "Force buy BNB?\nThis will place a market order now (uses safety caps).", forceBuyConfirmKeyboard())
	case "force_buy_confirm":
		msg, err := executeManualBNBBuy(ctx, cfg, binance, state, true)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("force buy error: %v", err), defaultKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, msg, defaultKeyboard())
	case "force_buy_cancel":
		safeSendToChat(notifier, chatID, "Force buy canceled.", defaultKeyboard())
	case "daily_report_now":
		if err := sendDailyReport(ctx, cfg, binance, notifier, state); err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("daily report error: %v", err), defaultKeyboard())
		}
	case "fee_currency_menu":
		current := state.getDisplayCurrency(cfg.FeeMainCurrency)
		safeSendToChat(notifier, chatID, fmt.Sprintf("Choose display currency (current: %s):", current), feeCurrencyKeyboard())
	case "fee_currency_bnb":
		state.setDisplayCurrency("BNB")
		_ = state.save()
		safeSendToChat(notifier, chatID, "Display currency set to BNB.", settingsKeyboard())
	case "fee_currency_usdt":
		state.setDisplayCurrency("USDT")
		_ = state.save()
		safeSendToChat(notifier, chatID, "Display currency set to USDT.", settingsKeyboard())
	case "report_day", "report_week", "report_month":
		dur := selectDuration(data)
		label := durationLabel(data)
		if err := sendPeriodReport(ctx, cfg, binance, notifier, state, dur, label); err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("%s report error: %v", label, err), defaultKeyboard())
		}
	case "status":
		report, err := buildStatusReport(ctx, cfg, binance, state)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("status error: %v", err), defaultKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, report, defaultKeyboard())
	case "fees_day", "fees_week", "fees_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		v, err := totalFeesBNB(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, dur)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("fee calc error: %v", err), defaultKeyboard())
			return
		}
		spot := spotForDisplay(ctx, cfg, binance, dur)
		mainCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		feeText := formatFeeByMainCurrency(v, cfg, mainCurrency, spot)
		note := ""
		if spot > 0 {
			if cfg.FreqtradeHistoryMode {
				note = ", inferred from Freqtrade"
			} else {
				note = ", spot"
			}
		}
		safeSendToChat(
			notifier,
			chatID,
			fmt.Sprintf("Fees consumed (%s): %s%s", title, feeText, note),
			defaultKeyboard(),
		)
	case "trades_day", "trades_week", "trades_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		bnbPrice := spotForDisplay(ctx, cfg, binance, dur)
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		var table string
		if cfg.FreqtradeHistoryMode {
			ftTrades, err := getFreqtradeTrades30dCached(ctx, cfg)
			if err != nil {
				safeSendToChat(notifier, chatID, fmt.Sprintf("trades error: %v", err), defaultKeyboard())
				return
			}
			table = formatFreqtradeTradesGroupedTable(title, ftTrades, time.Now().UTC().Add(-dur), cfg, displayCurrency, bnbPrice)
		} else {
			trades, err := collectTradesByDuration(ctx, binance, cfg.TrackedSymbols, dur)
			if err != nil {
				safeSendToChat(notifier, chatID, fmt.Sprintf("trades error: %v", err), defaultKeyboard())
				return
			}
			table = formatTradesTable(title, trades, cfg, bnbPrice, displayCurrency)
		}
		safeSendPreLargeToChat(notifier, chatID, table, defaultKeyboard())
	case "leaders_day", "leaders_week", "leaders_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		text, err := buildPairLeaderboard(ctx, cfg, state, binance, dur, title)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("leaderboard error: %v", err), defaultKeyboard())
			return
		}
		safeSendPreToChat(notifier, chatID, text, defaultKeyboard())
	case "pnl_7d_table":
		text, err := buildDailyPnlTable(ctx, cfg, state, 7)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("pnl table error: %v", err), defaultKeyboard())
			return
		}
		safeSendPreToChat(notifier, chatID, text, &inlineKeyboardMarkup{
			InlineKeyboard: [][]inlineKeyboardButton{
				{{Text: "Refresh", CallbackData: "pnl_7d_table"}},
			},
		})
	case "pnl_day", "pnl_week", "pnl_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		if cfg.FreqtradeHistoryMode {
			trades, err := getFreqtradeTrades30dCached(ctx, cfg)
			if err != nil {
				safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s) error: %v", title, err), defaultKeyboard())
				return
			}
			pnl, pct, ok := freqtradePnlSince(trades, time.Now().UTC().Add(-dur))
			if !ok {
				safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): not enough data yet", title), defaultKeyboard())
				return
			}
			displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
			spot := spotForDisplay(ctx, cfg, binance, dur)
			safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): %s (%.2f%%)", title, formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct), defaultKeyboard())
			return
		}
		pnl, pct, ok := state.pnlSince(dur)
		if !ok {
			safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): not enough data yet", title), defaultKeyboard())
			return
		}
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		spot := spotForDisplay(ctx, cfg, binance, dur)
		safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): %s (%.2f%%)", title, formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct), defaultKeyboard())
	case "chart_fees":
		labels, values, err := feeSeriesLastNDaysCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, 30)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("fees chart error: %v", err), defaultKeyboard())
			return
		}
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No fee trade data for chart yet", defaultKeyboard())
			return
		}
		chartURL := buildLineChartURL("BNB Fees (Last 30 Days)", labels, values, "BNB")
		safeSendPhotoToChat(notifier, chatID, chartURL, "Fees chart")
	case "chart_cum_fees_day", "chart_cum_fees_week", "chart_cum_fees_month":
		dur := selectDuration(data)
		labels, values, unit, err := cumulativeFeesSeriesWindow(ctx, cfg, state, binance, dur)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("cumulative fees chart error: %v", err), defaultKeyboard())
			return
		}
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No cumulative fee data yet", defaultKeyboard())
			return
		}
		chartURL := buildCumulativeProfitChartURL("Cumulative Fees", labels, values, unit)
		safeSendPhotoToChat(notifier, chatID, chartURL, "Cumulative Fees")
	case "chart_pnl":
		labels, values := state.pnlSeriesLastNDays(30)
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No PnL data for chart yet", defaultKeyboard())
			return
		}
		chartURL := buildLineChartURL("PnL Delta (Last 30 Days)", labels, values, cfg.QuoteAsset)
		safeSendPhotoToChat(notifier, chatID, chartURL, "PnL chart")
	case "chart_cum_profit_day", "chart_cum_profit_48h", "chart_cum_profit_72h", "chart_cum_profit_week", "chart_cum_profit_month":
		dur := selectDuration(data)
		labels, values, unit := cumulativeProfitSeriesWindow(ctx, cfg, state, binance, dur)
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No cumulative profit data yet", defaultKeyboard())
			return
		}
		chartURL := buildCumulativeProfitChartURL("Cumulative Profit", labels, values, unit)
		safeSendPhotoToChat(notifier, chatID, chartURL, "Cumulative Profit")
	case "chart_cum_profit_custom":
		setAwaitingCustomCumProfitWindow(chatID, true)
		safeSendToChat(notifier, chatID, "Choose cumulative profit window or type it (example: `36h`, `3d`).", customCumProfitWindowKeyboard())
	case "chart_cum_profit_range":
		clearRangeFromSelection(chatID)
		safeSendToChat(notifier, chatID, "Choose FROM (how long ago to start):", customCumProfitRangeFromKeyboard())
	case "chart_cum_profit_date_range":
		clearCalendarRangeState(chatID)
		clearCustomCumProfitDateRangeState(chatID)
		safeSendToChat(notifier, chatID, "Choose date-range input method:", customCumProfitDateRangeEntryKeyboard())
	case "chart_cum_profit_date_range_manual":
		clearCalendarRangeState(chatID)
		setAwaitingCustomCumProfitDateFrom(chatID)
		safeSendToChat(notifier, chatID, "Type FROM date/time in UTC (`YYYY-MM-DD HH:MM`).\nExample: `2026-03-01 08:00`\nType `cancel` to stop.", chartsKeyboard())
	case "chart_cum_profit_calendar_range":
		clearCustomCumProfitDateRangeState(chatID)
		setCalendarRangePhase(chatID, "from_day")
		now := time.Now().UTC()
		safeSendToChat(notifier, chatID, "Pick FROM date:", customCumProfitCalendarKeyboard("from", now.Year(), now.Month()))
	case "chart_cum_profit_custom_history":
		history := state.customCumWindows()
		if len(history) == 0 {
			safeSendToChat(notifier, chatID, "No custom history yet. Type one first (example: `36h`, `10d`).", customCumProfitWindowKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, "Custom cumulative history:", customCumProfitHistoryKeyboard(history))
	case "chart_cum_profit_range_history":
		history := state.customRangeHistory()
		if len(history) == 0 {
			safeSendToChat(notifier, chatID, "No range history yet.", chartsKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, "Last 5 ranges:", customCumProfitRangeHistoryKeyboard(history))
	case "freqtrade_health":
		report := buildFreqtradeHealthReport(ctx, cfg)
		safeSendToChat(notifier, chatID, report, defaultKeyboard())
	default:
		route := telegramiface.ParseCallbackData(data)
		switch route.Kind {
		case telegramiface.CallbackCustomWindow:
			if len(route.Parts) != 1 {
				safeSendToChat(notifier, chatID, "Invalid custom window.", chartsKeyboard())
				return
			}
			token := route.Parts[0]
			_, _, label, ok := parseCumProfitWindowInput(token)
			if !ok {
				safeSendToChat(notifier, chatID, "Invalid custom window.", chartsKeyboard())
				return
			}
			state.addCustomCumWindow(token)
			_ = state.save()
			setAwaitingCustomCumProfitWindow(chatID, false)
			safeSendToChat(notifier, chatID, fmt.Sprintf("Window %s selected. Choose timeline mode:", label), customCumProfitGranularityKeyboard(token))
			return
		case telegramiface.CallbackCustomGran:
			if len(route.Parts) != 2 {
				safeSendToChat(notifier, chatID, "Invalid custom chart selection.", chartsKeyboard())
				return
			}
			dur, _, label, ok := parseCumProfitWindowInput(route.Parts[0])
			if !ok {
				safeSendToChat(notifier, chatID, "Invalid custom window.", chartsKeyboard())
				return
			}
			mode, modeLabel := parseCumProfitGranularity(route.Parts[1])
			labels, values, unit := cumulativeProfitSeriesWindowMode(ctx, cfg, state, binance, dur, mode)
			if len(labels) == 0 {
				safeSendToChat(notifier, chatID, "No cumulative profit data yet", defaultKeyboard())
				return
			}
			title := fmt.Sprintf("Cumulative Profit (%s, %s)", label, modeLabel)
			chartURL := buildCumulativeProfitChartURL(title, labels, values, unit)
			safeSendPhotoToChat(notifier, chatID, chartURL, title)
			return
		case telegramiface.CallbackCalendarIgnore:
			return
		case telegramiface.CallbackCalendar:
			if len(route.Parts) != 3 {
				safeSendToChat(notifier, chatID, "Invalid calendar action.", chartsKeyboard())
				return
			}
			phase := route.Parts[0]
			action := route.Parts[1]
			payload := route.Parts[2]
			if phase != "from" && phase != "to" {
				safeSendToChat(notifier, chatID, "Invalid calendar phase.", chartsKeyboard())
				return
			}
			switch action {
			case "nav":
				year, month, ok := parseCalendarMonthToken(payload)
				if !ok {
					safeSendToChat(notifier, chatID, "Invalid calendar month.", chartsKeyboard())
					return
				}
				setCalendarRangePhase(chatID, phase+"_day")
				prompt := "Pick FROM date:"
				if phase == "to" {
					prompt = "Pick TO date:"
				}
				safeSendToChat(notifier, chatID, prompt, customCumProfitCalendarKeyboard(phase, year, month))
				return
			case "day":
				dt, ok := parseCalendarDayToken(payload)
				if !ok {
					safeSendToChat(notifier, chatID, "Invalid calendar day.", chartsKeyboard())
					return
				}
				if dt.After(time.Now().UTC()) {
					safeSendToChat(notifier, chatID, "Date cannot be in the future.", chartsKeyboard())
					return
				}
				if phase == "from" {
					setCalendarRangeFromDate(chatID, dt)
					setCalendarRangePhase(chatID, "from_hour")
					safeSendToChat(notifier, chatID, fmt.Sprintf("FROM date %s selected. Pick FROM hour:", dt.Format("2006-01-02")), customCumProfitHourKeyboard("from"))
				} else {
					setCalendarRangeToDate(chatID, dt)
					setCalendarRangePhase(chatID, "to_hour")
					safeSendToChat(notifier, chatID, fmt.Sprintf("TO date %s selected. Pick TO hour:", dt.Format("2006-01-02")), customCumProfitHourKeyboard("to"))
				}
				return
			case "hour":
				hour, err := strconv.Atoi(payload)
				if err != nil || hour < 0 || hour > 23 {
					safeSendToChat(notifier, chatID, "Invalid hour.", chartsKeyboard())
					return
				}
				st, ok := getCalendarRangeState(chatID)
				if !ok {
					safeSendToChat(notifier, chatID, "Calendar session expired. Start again.", chartsKeyboard())
					return
				}
				if phase == "from" {
					if st.From.IsZero() {
						safeSendToChat(notifier, chatID, "Choose FROM date first.", chartsKeyboard())
						return
					}
					from := time.Date(st.From.Year(), st.From.Month(), st.From.Day(), hour, 0, 0, 0, time.UTC)
					if from.After(time.Now().UTC()) {
						safeSendToChat(notifier, chatID, "FROM datetime cannot be in the future.", chartsKeyboard())
						return
					}
					setCalendarRangeFromDate(chatID, from)
					setCalendarRangePhase(chatID, "to_day")
					safeSendToChat(notifier, chatID, fmt.Sprintf("FROM set to %s UTC. Now pick TO date:", from.Format("2006-01-02 15:04")), customCumProfitCalendarKeyboard("to", from.Year(), from.Month()))
				} else {
					if st.To.IsZero() || st.From.IsZero() {
						safeSendToChat(notifier, chatID, "Choose TO date first.", chartsKeyboard())
						return
					}
					to := time.Date(st.To.Year(), st.To.Month(), st.To.Day(), hour, 0, 0, 0, time.UTC)
					if to.After(time.Now().UTC()) {
						safeSendToChat(notifier, chatID, "TO datetime cannot be in the future.", chartsKeyboard())
						return
					}
					if !to.After(st.From) {
						safeSendToChat(notifier, chatID, "Invalid TO range. FROM must be older than TO.", customCumProfitCalendarKeyboard("to", st.From.Year(), st.From.Month()))
						return
					}
					clearCalendarRangeState(chatID)
					safeSendToChat(
						notifier,
						chatID,
						fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", st.From.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")),
						customCumProfitDateRangeGranularityKeyboard(st.From.Unix(), to.Unix()),
					)
				}
				return
			default:
				safeSendToChat(notifier, chatID, "Invalid calendar action.", chartsKeyboard())
				return
			}
		case telegramiface.CallbackRangeFrom:
			if len(route.Parts) != 1 {
				safeSendToChat(notifier, chatID, "Invalid FROM range.", chartsKeyboard())
				return
			}
			fromToken := route.Parts[0]
			fromAgo, fromLabel, ok := parseRangeAgoToken(fromToken)
			if !ok {
				safeSendToChat(notifier, chatID, "Invalid FROM range.", chartsKeyboard())
				return
			}
			setRangeFromSelection(chatID, fromToken)
			safeSendToChat(notifier, chatID, fmt.Sprintf("From set: %s ago. Choose TO:", fromLabel), customCumProfitRangeToKeyboard(fromToken))
			_ = fromAgo
			return
		case telegramiface.CallbackRangeTo:
			if len(route.Parts) != 1 {
				safeSendToChat(notifier, chatID, "Invalid TO range.", chartsKeyboard())
				return
			}
			toToken := route.Parts[0]
			fromToken, okFrom := getRangeFromSelection(chatID)
			if !okFrom {
				safeSendToChat(notifier, chatID, "Please choose FROM first.", customCumProfitRangeFromKeyboard())
				return
			}
			fromAgo, fromLabel, okA := parseRangeAgoToken(fromToken)
			toAgo, toLabel, okB := parseRangeAgoToken(toToken)
			if !okA || !okB || fromAgo <= toAgo {
				safeSendToChat(notifier, chatID, "Invalid TO range. FROM must be older than TO.", customCumProfitRangeToKeyboard(fromToken))
				return
			}
			safeSendToChat(notifier, chatID, fmt.Sprintf("Range: %s ago -> %s ago. Choose timeline:", fromLabel, toLabel), customCumProfitRangeGranularityKeyboard(fromToken, toToken))
			return
		case telegramiface.CallbackRangeGran:
			if len(route.Parts) != 3 {
				safeSendToChat(notifier, chatID, "Invalid range chart selection.", chartsKeyboard())
				return
			}
			fromAgo, fromLabel, okA := parseRangeAgoToken(route.Parts[0])
			toAgo, toLabel, okB := parseRangeAgoToken(route.Parts[1])
			if !okA || !okB || fromAgo <= toAgo {
				safeSendToChat(notifier, chatID, "Invalid range. FROM must be older than TO.", customCumProfitRangeFromKeyboard())
				return
			}
			mode, modeLabel := parseCumProfitGranularity(route.Parts[2])
			labels, values, unit := cumulativeProfitSeriesRangeMode(ctx, cfg, state, binance, fromAgo, toAgo, mode)
			if len(labels) == 0 {
				safeSendToChat(notifier, chatID, "No cumulative profit data in this range yet.", chartsKeyboard())
				return
			}
			now := time.Now().UTC()
			from := now.Add(-fromAgo)
			to := now.Add(-toAgo)
			state.addCustomRange(from, to)
			_ = state.save()
			clearRangeFromSelection(chatID)
			title := fmt.Sprintf("Cumulative Profit (%s ago -> %s ago, %s)", fromLabel, toLabel, modeLabel)
			chartURL := buildCumulativeProfitChartURL(title, labels, values, unit)
			safeSendPhotoToChat(notifier, chatID, chartURL, title)
			return
		case telegramiface.CallbackRangeHistory:
			if len(route.Parts) != 2 {
				safeSendToChat(notifier, chatID, "Invalid range history item.", chartsKeyboard())
				return
			}
			fromSec, errA := strconv.ParseInt(route.Parts[0], 10, 64)
			toSec, errB := strconv.ParseInt(route.Parts[1], 10, 64)
			if errA != nil || errB != nil {
				safeSendToChat(notifier, chatID, "Invalid range history value.", chartsKeyboard())
				return
			}
			from := time.Unix(fromSec, 0).UTC()
			to := time.Unix(toSec, 0).UTC()
			if !to.After(from) {
				safeSendToChat(notifier, chatID, "Invalid history range.", chartsKeyboard())
				return
			}
			safeSendToChat(
				notifier,
				chatID,
				fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")),
				customCumProfitDateRangeGranularityKeyboard(from.Unix(), to.Unix()),
			)
			return
		case telegramiface.CallbackDateRangeGran:
			if len(route.Parts) != 3 {
				safeSendToChat(notifier, chatID, "Invalid date range chart selection.", chartsKeyboard())
				return
			}
			fromSec, errA := strconv.ParseInt(route.Parts[0], 10, 64)
			toSec, errB := strconv.ParseInt(route.Parts[1], 10, 64)
			if errA != nil || errB != nil {
				safeSendToChat(notifier, chatID, "Invalid date range values.", chartsKeyboard())
				return
			}
			from := time.Unix(fromSec, 0).UTC()
			to := time.Unix(toSec, 0).UTC()
			if !to.After(from) {
				safeSendToChat(notifier, chatID, "Invalid range: TO must be after FROM.", chartsKeyboard())
				return
			}
			mode, modeLabel := parseCumProfitGranularity(route.Parts[2])
			labels, values, unit := cumulativeProfitSeriesBetweenMode(ctx, cfg, state, binance, from, to, mode)
			if len(labels) == 0 {
				safeSendToChat(notifier, chatID, "No cumulative profit data in this date range.", chartsKeyboard())
				return
			}
			state.addCustomRange(from, to)
			_ = state.save()
			title := fmt.Sprintf("Cumulative Profit (%s -> %s, %s)", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"), modeLabel)
			chartURL := buildCumulativeProfitChartURL(title, labels, values, unit)
			safeSendPhotoToChat(notifier, chatID, chartURL, title)
			return
		default:
			safeSendToChat(notifier, chatID, "Unknown action", defaultKeyboard())
		}
	}
}

func normalizeCommand(raw string) string {
	return telegramiface.NormalizeCommand(raw)
}
