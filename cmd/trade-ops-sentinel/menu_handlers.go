package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	telegramiface "trade-ops-sentinel/internal/interfaces/telegram"
)

func RegisterAllHandlers(r *MenuRegistry) {
	// Navigation Handlers
	r.RegisterHandler("menu", func(c *MenuContext) error {
		safeSendToChat(c.Notifier, c.Update.Message.Chat.ID, "Main keyboard enabled.", defaultReplyKeyboard())
		c.Reply("BNB monitor menu:", "menu_main")
		return nil
	})
	r.RegisterHandler("/menu", func(c *MenuContext) error {
		safeSendToChat(c.Notifier, c.Update.Message.Chat.ID, "Main keyboard enabled.", defaultReplyKeyboard())
		c.Reply("BNB monitor menu:", "menu_main")
		return nil
	})
	r.RegisterHandler("/status", func(c *MenuContext) error {
		report, err := buildStatusReport(c.Ctx, c.Cfg, c.Binance, c.State)
		if err != nil {
			return err
		}
		c.Reply(report, "menu_main")
		return nil
	})
	r.RegisterHandler("status", func(c *MenuContext) error {
		report, err := buildStatusReport(c.Ctx, c.Cfg, c.Binance, c.State)
		if err != nil {
			return err
		}
		c.Reply(report, "menu_main")
		return nil
	})
	r.RegisterHandler("/daily", func(c *MenuContext) error {
		return sendDailyReport(c.Ctx, c.Cfg, c.Binance, c.Notifier, c.State)
	})
	r.RegisterHandler("/help", func(c *MenuContext) error {
		safeSendToChat(c.Notifier, c.Update.Message.Chat.ID, helpText(), defaultReplyKeyboard())
		return nil
	})
	r.RegisterHandler("/version", func(c *MenuContext) error {
		safeSendToChat(c.Notifier, c.Update.Message.Chat.ID, versionReport(), defaultReplyKeyboard())
		return nil
	})

	r.RegisterHandler("menu_main", func(c *MenuContext) error {
		c.Reply("Main menu", "menu_main")
		return nil
	})
	r.RegisterHandler("menu_actions", func(c *MenuContext) error {
		c.Reply("Actions menu", "menu_actions")
		return nil
	})
	r.RegisterHandler("menu_reports", func(c *MenuContext) error {
		c.Reply("Reports menu", "menu_reports")
		return nil
	})
	r.RegisterHandler("menu_charts", func(c *MenuContext) error {
		c.Reply("Charts menu", "menu_charts")
		return nil
	})
	r.RegisterHandler("menu_settings", func(c *MenuContext) error {
		c.Reply("Settings menu", "menu_settings")
		return nil
	})

	// PnL History
	r.RegisterHandler("pnl_history_menu", func(c *MenuContext) error {
		c.Reply("PnL History menu", "pnl_history_menu")
		return nil
	})
	r.RegisterHandler("pnl_history_7d", func(c *MenuContext) error {
		safeSendPnLHistoryReport(c.Ctx, c.Cfg, c.Binance, c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, 7, c.State)
		return nil
	})
	r.RegisterHandler("pnl_history_30d", func(c *MenuContext) error {
		safeSendPnLHistoryReport(c.Ctx, c.Cfg, c.Binance, c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, 30, c.State)
		return nil
	})
	r.RegisterHandler("pnl_history_custom", func(c *MenuContext) error {
		setAwaitingPnLHistoryInput(c.Update.CallbackQuery.Message.Chat.ID, true)
		c.Reply("Enter number of days for PnL History (e.g. `14`, `90`):", "pnl_history_menu")
		return nil
	})

	// Actions
	r.RegisterHandler("refill_now", func(c *MenuContext) error {
		msg, err := executeManualBNBBuy(c.Ctx, c.Cfg, c.Binance, c.State, false)
		if err != nil {
			return err
		}
		c.Reply(msg, "menu_main")
		return nil
	})
	r.RegisterHandler("force_buy", func(c *MenuContext) error {
		c.Reply("Force buy BNB?\nThis will place a market order now (uses safety caps).", "force_buy_confirm_menu")
		return nil
	})
	r.RegisterHandler("force_buy_confirm", func(c *MenuContext) error {
		msg, err := executeManualBNBBuy(c.Ctx, c.Cfg, c.Binance, c.State, true)
		if err != nil {
			return err
		}
		c.Reply(msg, "menu_main")
		return nil
	})
	r.RegisterHandler("force_buy_cancel", func(c *MenuContext) error {
		c.Reply("Force buy canceled.", "menu_main")
		return nil
	})
	r.RegisterHandler("daily_report_now", func(c *MenuContext) error {
		return sendDailyReport(c.Ctx, c.Cfg, c.Binance, c.Notifier, c.State)
	})

	// Reports & Tables
	r.RegisterHandler("pnl_7d_table", func(c *MenuContext) error {
		text, err := buildDailyPnlTable(c.Ctx, c.Cfg, c.State, 7)
		if err != nil {
			return err
		}
		safeSendPreToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, text, &inlineKeyboardMarkup{
			InlineKeyboard: [][]inlineKeyboardButton{
				{{Text: "Refresh", CallbackData: "pnl_7d_table"}},
			},
		})
		return nil
	})

	// Settings Handlers
	r.RegisterHandler("fee_currency_menu", func(c *MenuContext) error {
		current := c.State.getDisplayCurrency(c.Cfg.FeeMainCurrency)
		GetMenuRegistry().SetKeyboard("temp_currency", feeCurrencyKeyboard())
		c.Reply(fmt.Sprintf("Choose display currency (current: %s):", current), "temp_currency")
		return nil
	})
	r.RegisterHandler("fee_currency_*", handleFeeCurrencyChange)

	r.RegisterHandler("chart_theme_menu", func(c *MenuContext) error {
		current := c.State.getChartTheme("dark")
		GetMenuRegistry().SetKeyboard("temp_theme", chartThemeKeyboard(current))
		c.Reply(fmt.Sprintf("Choose chart theme (current: %s):", strings.Title(current)), "temp_theme")
		return nil
	})
	r.RegisterHandler("chart_theme_*", handleChartThemeChange)

	r.RegisterHandler("chart_size_menu", func(c *MenuContext) error {
		current := c.State.getChartSize("standard")
		GetMenuRegistry().SetKeyboard("temp_size", chartSizeKeyboard(current))
		c.Reply(fmt.Sprintf("Choose chart size (current: %s):", strings.Title(current)), "temp_size")
		return nil
	})
	r.RegisterHandler("chart_size_*", handleChartSizeChange)

	r.RegisterHandler("chart_labels_*", func(c *MenuContext) error {
		return handleToggleSetting(c, "Chart Labels", c.State.getChartLabelsEnabled, c.State.setChartLabelsEnabled)
	})

	r.RegisterHandler("chart_grid_*", func(c *MenuContext) error {
		return handleToggleSetting(c, "Chart Grid", c.State.getChartGridEnabled, c.State.setChartGridEnabled)
	})

	r.RegisterHandler("pnl_emoji_*", func(c *MenuContext) error {
		return handleToggleSetting(c, "PnL Emojis", c.State.getPnLEmojisEnabled, c.State.setPnLEmojisEnabled)
	})

	r.RegisterHandler("chart_mode_menu", func(c *MenuContext) error {
		current := c.State.getChartLabelMode("staggered")
		GetMenuRegistry().SetKeyboard("temp_mode", chartLabelModeKeyboard(current))
		c.Reply(fmt.Sprintf("Choose chart label mode (current: %s):", strings.Title(current)), "temp_mode")
		return nil
	})
	r.RegisterHandler("chart_mode_*", handleChartModeChange)
	r.RegisterHandler("alert_*", handleAlertToggle)

	r.RegisterHandler("alerts_menu", func(c *MenuContext) error {
		heartbeatEnabled := c.Cfg.HeartbeatAlertEnabled
		apiEnabled := c.Cfg.APIFailureAlertEnabled
		if runtimeAlerts != nil {
			heartbeatEnabled = runtimeAlerts.heartbeatAlertsOn()
			apiEnabled = runtimeAlerts.apiFailureAlertsOn()
		}
		GetMenuRegistry().SetKeyboard("temp_alerts", alertSettingsKeyboard(heartbeatEnabled, apiEnabled))
		c.Reply("Toggle runtime alerts:", "temp_alerts")
		return nil
	})

	// Prefix Handlers
	r.RegisterHandler("stop_alert_*", func(c *MenuContext) error {
		data := c.Update.CallbackQuery.Data
		key := strings.TrimPrefix(data, "stop_alert_")
		if runtimeAlerts != nil {
			runtimeAlerts.StopAlert(key)
		}
		c.Reply(fmt.Sprintf("✅ Notification for <code>%s</code> stopped until recovery.", key), "menu_main")
		return nil
	})

	r.RegisterHandler("report_*", handlePeriodReport)
	r.RegisterHandler("fees_*", handleFeesReport)
	r.RegisterHandler("trades_*", handleTradesReport)
	r.RegisterHandler("leaders_*", handleLeadersReport)
	r.RegisterHandler("pnl_day", handlePnlReport)
	r.RegisterHandler("pnl_week", handlePnlReport)
	r.RegisterHandler("pnl_month", handlePnlReport)

	// Settings navigation
	r.RegisterHandler("settings_overview", func(c *MenuContext) error {
		c.Reply(c.State.settingsSummary(c.Cfg, runtimeAlerts), "menu_settings")
		return nil
	})
	r.RegisterHandler("settings_ignore", func(c *MenuContext) error {
		c.Reply("Settings menu", "menu_settings")
		return nil
	})

	// Freqtrade restart
	r.RegisterHandler("ft_restart_*", handleFreqtradeRestart)

	// Freqtrade Health
	r.RegisterHandler("freqtrade_health", func(c *MenuContext) error {
		report := buildFreqtradeHealthReport(c.Ctx, c.Cfg)
		c.Reply(report, "menu_settings")
		return nil
	})

	// Complex routing prefixes from callbacks.go
	r.RegisterHandler("ccpw_*", handleSpecialRoutes)
	r.RegisterHandler("ccpg_*", handleSpecialRoutes)
	r.RegisterHandler("ccal_*", handleSpecialRoutes)
	r.RegisterHandler("cprf_*", handleSpecialRoutes)
	r.RegisterHandler("cprt_*", handleSpecialRoutes)
	r.RegisterHandler("cprg_*", handleSpecialRoutes)
	r.RegisterHandler("cprh_*", handleSpecialRoutes)
	r.RegisterHandler("cpdtg_*", handleSpecialRoutes)

	// Chart Handlers
	r.RegisterHandler("chart_fees", handleChartFees)
	r.RegisterHandler("chart_pnl", handleChartPnl)
	r.RegisterHandler("chart_predict_*", handleChartPredict)
	r.RegisterHandler("chart_compound_*", handleChartCompound)
	r.RegisterHandler("chart_cum_fees_*", handleChartCumFees)
	r.RegisterHandler("chart_cum_profit_*", handleChartCumProfit)
}

func handleTradesReport(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	dur := selectDuration(data)
	title := durationLabel(data)
	bnbPrice := spotForDisplay(c.Ctx, c.Cfg, c.Binance, dur)
	displayCurrency := c.State.getDisplayCurrency(c.Cfg.FeeMainCurrency)
	var table string
	if c.Cfg.FreqtradeHistoryMode {
		ftTrades, err := getFreqtradeTrades30dCached(c.Ctx, c.Cfg)
		if err != nil {
			return err
		}
		table = formatFreqtradeTradesGroupedTable(title, ftTrades, time.Now().UTC().Add(-dur), c.Cfg, displayCurrency, bnbPrice)
	} else {
		trades, err := collectTradesByDuration(c.Ctx, c.Binance, c.Cfg.TrackedSymbols, dur)
		if err != nil {
			return err
		}
		table = formatTradesTable(title, trades, c.Cfg, bnbPrice, displayCurrency)
	}
	safeSendPreLargeToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, table, GetMenuRegistry().GetKeyboard("menu_main"))
	return nil
}

func handleLeadersReport(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	dur := selectDuration(data)
	title := durationLabel(data)
	text, err := buildPairLeaderboard(c.Ctx, c.Cfg, c.State, c.Binance, dur, title)
	if err != nil {
		return err
	}
	safeSendPreToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, text, GetMenuRegistry().GetKeyboard("menu_main"))
	return nil
}

func handlePnlReport(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	dur := selectDuration(data)
	title := durationLabel(data)
	var pnl, pct float64
	var ok bool
	if c.Cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(c.Ctx, c.Cfg)
		if err != nil {
			return err
		}
		pnl, pct, ok = freqtradePnlSince(trades, time.Now().UTC().Add(-dur))
	} else {
		pnl, pct, ok = c.State.pnlSince(dur)
	}

	if !ok {
		c.Reply(fmt.Sprintf("PnL (%s): not enough data yet", title), "menu_main")
		return nil
	}
	displayCurrency := c.State.getDisplayCurrency(c.Cfg.FeeMainCurrency)
	spot := spotForDisplay(c.Ctx, c.Cfg, c.Binance, dur)
	c.Reply(fmt.Sprintf("PnL (%s): %s (%.2f%%)", title, formatQuoteByDisplay(pnl, c.Cfg, displayCurrency, spot), pct), "menu_main")
	return nil
}

func handleChartFees(c *MenuContext) error {
	labels, values, err := feeSeriesLastNDaysCached(c.Ctx, c.Binance, c.Cfg.TrackedSymbols, c.Cfg.BNBAsset, 30)
	if err != nil {
		return err
	}
	if len(labels) == 0 {
		c.Reply("No fee trade data for chart yet", "menu_main")
		return nil
	}
	theme := c.State.getChartTheme("dark")
	size := c.State.getChartSize("standard")
	chartLabels := c.State.getChartLabelsEnabled(true)
	grid := c.State.getChartGridEnabled(true)
	chartURL := buildLineChartURL("BNB Fees (Last 30 Days)", labels, values, "BNB", theme, size, chartLabels, grid)
	safeSendPhotoToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, chartURL, "Fees chart")
	return nil
}

func handleChartPnl(c *MenuContext) error {
	labels, values := c.State.pnlSeriesLastNDays(30)
	if len(labels) == 0 {
		c.Reply("No PnL data for chart yet", "menu_main")
		return nil
	}
	theme := c.State.getChartTheme("dark")
	size := c.State.getChartSize("standard")
	chartLabels := c.State.getChartLabelsEnabled(true)
	grid := c.State.getChartGridEnabled(true)
	chartURL := buildLineChartURL("PnL Delta (Last 30 Days)", labels, values, c.Cfg.QuoteAsset, theme, size, chartLabels, grid)
	safeSendPhotoToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, chartURL, "PnL chart")
	return nil
}

func handleChartPredict(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	if data == "chart_predict_custom" {
		setAwaitingPredictionDays(c.Update.CallbackQuery.Message.Chat.ID, "daily", true)
		safeSendToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, fmt.Sprintf("Type forecast horizon in days (`%d`..`%d`). Example: `21`.\nType `cancel` to stop.", minPredictionDays, maxPredictionDays), GetMenuRegistry().GetKeyboard("prediction_days_menu"))
		return nil
	}
	if data == "chart_predict_cum_custom" {
		setAwaitingPredictionDays(c.Update.CallbackQuery.Message.Chat.ID, "cum", true)
		safeSendToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, fmt.Sprintf("Type cumulative forecast horizon in days (`%d`..`%d`). Example: `21`.\nType `cancel` to stop.", minPredictionDays, maxPredictionDays), GetMenuRegistry().GetKeyboard("prediction_days_menu"))
		return nil
	}

	horizon := 7
	if strings.Contains(data, "14d") {
		horizon = 14
	} else if strings.Contains(data, "month") || strings.Contains(data, "30d") {
		horizon = 30
	} else if strings.Contains(data, "60d") {
		horizon = 60
	}

	cumulative := strings.Contains(data, "cum")
	theme := c.State.getChartTheme("dark")
	size := c.State.getChartSize("standard")
	chartLabels := c.State.getChartLabelsEnabled(true)
	grid := c.State.getChartGridEnabled(true)
	_ = chartLabels // predict charts usually don't use labels the same way but we pass theme/size

	return sendPredictionChart(c.Ctx, c.Cfg, c.State, c.Binance, c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, horizon, cumulative, data, theme, size, grid)
}

func handleChartCompound(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	if !c.Cfg.FreqtradeHistoryMode {
		c.Reply("Compound forecast is available in Freqtrade mode only (`TRACKED_SYMBOLS=FREQTRADE`).", "menu_charts")
		return nil
	}
	if data == "chart_compound_custom" {
		setAwaitingCompoundPredictionDays(c.Update.CallbackQuery.Message.Chat.ID, true)
		safeSendToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, fmt.Sprintf("Type compound forecast horizon in days (`%d`..`%d`). Example: `21`.\nType `cancel` to stop.", minPredictionDays, maxPredictionDays), GetMenuRegistry().GetKeyboard("prediction_days_menu"))
		return nil
	}

	horizon := 7
	if strings.Contains(data, "month") {
		horizon = 30
	}
	theme := c.State.getChartTheme("dark")
	size := c.State.getChartSize("standard")
	grid := c.State.getChartGridEnabled(true)
	return sendCompoundPredictionChart(c.Ctx, c.Cfg, c.State, c.Binance, c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, horizon, data, theme, size, grid)
}

func handleChartCumFees(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	dur := selectDuration(data)
	window := "24h"
	if strings.Contains(data, "week") {
		window = "7d"
	} else if strings.Contains(data, "month") {
		window = "30d"
	}
	labels, values, unit, err := cumulativeFeesSeriesWindow(c.Ctx, c.Cfg, c.State, c.Binance, dur)
	if err != nil {
		return err
	}
	if len(labels) == 0 {
		c.Reply("No cumulative fee data yet", "menu_main")
		return nil
	}
	title := fmt.Sprintf("Cumulative Fees (%s)", window)
	theme := c.State.getChartTheme("dark")
	size := c.State.getChartSize("standard")
	mode := c.State.getChartLabelMode("staggered")
	chartLabels := c.State.getChartLabelsEnabled(true)
	grid := c.State.getChartGridEnabled(true)
	chartURL := buildCumulativeProfitChartURL(title, labels, values, unit, theme, size, mode, chartLabels, grid)
	safeSendPhotoToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, chartURL, title)
	return nil
}

func handleChartCumProfit(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	if data == "chart_cum_profit_custom" {
		setAwaitingCustomCumProfitWindow(c.Update.CallbackQuery.Message.Chat.ID, true)
		safeSendToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, "Choose cumulative profit window or type it (example: `36h`, `3d`).", customCumProfitWindowKeyboard())
		return nil
	}
	if data == "chart_cum_profit_range" {
		clearRangeFromSelection(c.Update.CallbackQuery.Message.Chat.ID)
		safeSendToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, "Choose FROM (how long ago to start):", customCumProfitRangeFromKeyboard())
		return nil
	}
	// ... add more sub-actions of cum profit if needed, or keep generic
	dur := selectDuration(data)
	window := "24h"
	if strings.Contains(data, "48h") {
		window = "48h"
	} else if strings.Contains(data, "72h") {
		window = "72h"
	} else if strings.Contains(data, "week") {
		window = "7d"
	} else if strings.Contains(data, "month") {
		window = "30d"
	}
	labels, values, unit := cumulativeProfitSeriesWindow(c.Ctx, c.Cfg, c.State, c.Binance, dur)
	if len(labels) == 0 {
		c.Reply("No cumulative profit data yet", "menu_main")
		return nil
	}
	title := fmt.Sprintf("Cumulative Profit (%s)", window)
	theme := c.State.getChartTheme("dark")
	size := c.State.getChartSize("standard")
	mode := c.State.getChartLabelMode("staggered")
	chartLabels := c.State.getChartLabelsEnabled(true)
	grid := c.State.getChartGridEnabled(true)
	chartURL := buildCumulativeProfitChartURL(title, labels, values, unit, theme, size, mode, chartLabels, grid)
	safeSendPhotoToChatWithMarkup(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, chartURL, title, chartRefreshKeyboard(data))
	return nil
}

func handleFeeCurrencyChange(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	currency := strings.ToUpper(strings.TrimPrefix(data, "fee_currency_"))
	old := c.State.getDisplayCurrency(c.Cfg.FeeMainCurrency)
	c.State.setDisplayCurrency(currency)
	_ = c.State.save()
	recordSettingChange(c.State, c.Update, "display_currency", old, currency)
	c.Reply(fmt.Sprintf("Display currency set to %s.", currency), "menu_settings")
	return nil
}

func handleChartThemeChange(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	theme := strings.TrimPrefix(data, "chart_theme_")
	old := c.State.getChartTheme("dark")
	c.State.setChartTheme(theme)
	_ = c.State.save()
	recordSettingChange(c.State, c.Update, "chart_theme", old, theme)
	c.Reply(fmt.Sprintf("Chart theme set to %s.", strings.Title(theme)), "menu_settings")
	return nil
}

func handleChartSizeChange(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	size := strings.TrimPrefix(data, "chart_size_")
	old := c.State.getChartSize("standard")
	c.State.setChartSize(size)
	_ = c.State.save()
	recordSettingChange(c.State, c.Update, "chart_size", old, size)
	c.Reply(fmt.Sprintf("Chart size set to %s.", strings.Title(size)), "menu_settings")
	return nil
}

func handleToggleSetting(c *MenuContext, name string, getter func(bool) bool, setter func(bool)) error {
	data := c.Update.CallbackQuery.Data
	enabled := strings.HasSuffix(data, "_on")
	old := strconv.FormatBool(getter(true))
	setter(enabled)
	_ = c.State.save()
	recordSettingChange(c.State, c.Update, strings.ToLower(strings.ReplaceAll(name, " ", "_")), old, strconv.FormatBool(enabled))
	label := "disabled"
	if enabled {
		label = "enabled"
	}
	c.Reply(fmt.Sprintf("%s %s.", name, label), "menu_settings")
	return nil
}

func handleChartModeChange(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	mode := strings.TrimPrefix(data, "chart_mode_")
	old := c.State.getChartLabelMode("staggered")
	c.State.setChartLabelMode(mode)
	_ = c.State.save()
	recordSettingChange(c.State, c.Update, "chart_label_mode", old, mode)
	c.Reply(fmt.Sprintf("Chart labels set to %s.", strings.Title(mode)), "menu_settings")
	return nil
}

func handleAlertToggle(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	if strings.Contains(data, "_heartbeat_") {
		enabled := strings.HasSuffix(data, "_on")
		old := strconv.FormatBool(c.State.getHeartbeatAlertsEnabled(c.Cfg.HeartbeatAlertEnabled))
		if runtimeAlerts != nil {
			runtimeAlerts.setHeartbeatAlertsEnabled(enabled)
		}
		c.State.setHeartbeatAlertsEnabled(enabled)
		_ = c.State.save()
		recordSettingChange(c.State, c.Update, "heartbeat_alerts_enabled", old, strconv.FormatBool(enabled))
		label := "disabled"
		if enabled {
			label = "enabled"
		}
		c.Reply(fmt.Sprintf("Heartbeat alerts %s.", label), "menu_settings")
	} else if strings.Contains(data, "_api_") {
		enabled := strings.HasSuffix(data, "_on")
		old := strconv.FormatBool(c.State.getAPIFailureAlertsEnabled(c.Cfg.APIFailureAlertEnabled))
		if runtimeAlerts != nil {
			runtimeAlerts.setAPIFailureAlertsEnabled(enabled)
		}
		c.State.setAPIFailureAlertsEnabled(enabled)
		_ = c.State.save()
		recordSettingChange(c.State, c.Update, "api_failure_alerts_enabled", old, strconv.FormatBool(enabled))
		label := "disabled"
		if enabled {
			label = "enabled"
		}
		c.Reply(fmt.Sprintf("API failure alerts %s.", label), "menu_settings")
	}
	return nil
}

func recordSettingChange(state *MonitorState, upd tgUpdate, setting, oldValue, newValue string) {
	chatActorID, userActorID, username := actorFromUpdate(upd)
	appendSettingsAudit(state, settingsAuditEvent{
		TS:       time.Now().UTC().Format(time.RFC3339),
		ChatID:   chatActorID,
		UserID:   userActorID,
		Username: username,
		Setting:  setting,
		OldValue: oldValue,
		NewValue: newValue,
	})
}

func handleSpecialRoutes(c *MenuContext) error {
	return handleComplexCallback(c)
}

func handleComplexCallback(c *MenuContext) error {
	route := telegramiface.ParseCallbackData(c.Update.CallbackQuery.Data)
	chatID := c.Update.CallbackQuery.Message.Chat.ID
	theme := c.State.getChartTheme("dark")
	size := c.State.getChartSize("standard")
	chartLabels := c.State.getChartLabelsEnabled(true)
	grid := c.State.getChartGridEnabled(true)

	switch route.Kind {
	case telegramiface.CallbackCustomWindow:
		if len(route.Parts) != 1 {
			c.Reply("Invalid custom window.", "menu_charts")
			return nil
		}
		token := route.Parts[0]
		_, _, label, ok := parseCumProfitWindowInput(token)
		if !ok {
			c.Reply("Invalid custom window.", "menu_charts")
			return nil
		}
		c.State.addCustomCumWindow(token)
		_ = c.State.save()
		setAwaitingCustomCumProfitWindow(chatID, false)
		GetMenuRegistry().SetKeyboard("temp_cum_gran", customCumProfitGranularityKeyboard(token))
		c.Reply(fmt.Sprintf("Window %s selected. Choose timeline mode:", label), "temp_cum_gran")
		return nil

	case telegramiface.CallbackCustomGran:
		if len(route.Parts) != 2 {
			c.Reply("Invalid custom chart selection.", "menu_charts")
			return nil
		}
		dur, _, label, ok := parseCumProfitWindowInput(route.Parts[0])
		if !ok {
			c.Reply("Invalid custom window.", "menu_charts")
			return nil
		}
		mode, modeLabel := parseCumProfitGranularity(route.Parts[1])
		labels, values, unit := cumulativeProfitSeriesWindowMode(c.Ctx, c.Cfg, c.State, c.Binance, dur, mode)
		if len(labels) == 0 {
			c.Reply("No cumulative profit data yet", "menu_main")
			return nil
		}
		title := fmt.Sprintf("Cumulative Profit (%s, %s)", label, modeLabel)
		chartMode := c.State.getChartLabelMode("staggered")
		chartURL := buildCumulativeProfitChartURL(title, labels, values, unit, theme, size, chartMode, chartLabels, grid)
		safeSendPhotoToChatWithMarkup(c.Notifier, chatID, chartURL, title, chartRefreshKeyboard(route.Raw))
		return nil

	case telegramiface.CallbackCalendarIgnore:
		return nil

	case telegramiface.CallbackCalendar:
		if len(route.Parts) != 3 {
			c.Reply("Invalid calendar action.", "menu_charts")
			return nil
		}
		phase := route.Parts[0]
		action := route.Parts[1]
		payload := route.Parts[2]
		if phase != "from" && phase != "to" {
			c.Reply("Invalid calendar phase.", "menu_charts")
			return nil
		}
		switch action {
		case "nav":
			year, month, ok := parseCalendarMonthToken(payload)
			if !ok {
				c.Reply("Invalid calendar month.", "menu_charts")
				return nil
			}
			setCalendarRangePhase(chatID, phase+"_day")
			prompt := "Pick FROM date:"
			if phase == "to" {
				prompt = "Pick TO date:"
			}
			GetMenuRegistry().SetKeyboard("temp_calendar", customCumProfitCalendarKeyboard(phase, year, month))
			c.Reply(prompt, "temp_calendar")
			return nil
		case "day":
			dt, ok := parseCalendarDayToken(payload)
			if !ok {
				c.Reply("Invalid calendar day.", "menu_charts")
				return nil
			}
			if dt.After(time.Now().UTC()) {
				c.Reply("Date cannot be in the future.", "menu_charts")
				return nil
			}
			if phase == "from" {
				setCalendarRangeFromDate(chatID, dt)
				setCalendarRangePhase(chatID, "from_hour")
				GetMenuRegistry().SetKeyboard("temp_hour", customCumProfitHourKeyboard("from"))
				c.Reply(fmt.Sprintf("FROM date %s selected. Pick FROM hour:", dt.Format("2006-01-02")), "temp_hour")
			} else {
				setCalendarRangeToDate(chatID, dt)
				setCalendarRangePhase(chatID, "to_hour")
				GetMenuRegistry().SetKeyboard("temp_hour_to", customCumProfitHourKeyboard("to"))
				c.Reply(fmt.Sprintf("TO date %s selected. Pick TO hour:", dt.Format("2006-01-02")), "temp_hour_to")
			}
			return nil
		case "hour":
			hour, err := strconv.Atoi(payload)
			if err != nil || hour < 0 || hour > 23 {
				c.Reply("Invalid hour.", "menu_charts")
				return nil
			}
			st, ok := getCalendarRangeState(chatID)
			if !ok {
				c.Reply("Calendar session expired. Start again.", "menu_charts")
				return nil
			}
			if phase == "from" {
				if st.From.IsZero() {
					c.Reply("Choose FROM date first.", "menu_charts")
					return nil
				}
				from := time.Date(st.From.Year(), st.From.Month(), st.From.Day(), hour, 0, 0, 0, time.UTC)
				if from.After(time.Now().UTC()) {
					c.Reply("FROM datetime cannot be in the future.", "menu_charts")
					return nil
				}
				setCalendarRangeFromDate(chatID, from)
				setCalendarRangePhase(chatID, "to_day")
				GetMenuRegistry().SetKeyboard("temp_cal_to", customCumProfitCalendarKeyboard("to", from.Year(), from.Month()))
				c.Reply(fmt.Sprintf("FROM set to %s UTC. Now pick TO date:", from.Format("2006-01-02 15:04")), "temp_cal_to")
			} else {
				if st.To.IsZero() || st.From.IsZero() {
					c.Reply("Choose TO date first.", "menu_charts")
					return nil
				}
				to := time.Date(st.To.Year(), st.To.Month(), st.To.Day(), hour, 0, 0, 0, time.UTC)
				if to.After(time.Now().UTC()) {
					c.Reply("TO datetime cannot be in the future.", "menu_charts")
					return nil
				}
				if !to.After(st.From) {
					GetMenuRegistry().SetKeyboard("temp_cal_to_err", customCumProfitCalendarKeyboard("to", st.From.Year(), st.From.Month()))
					c.Reply("Invalid TO range. FROM must be older than TO.", "temp_cal_to_err")
					return nil
				}
				clearCalendarRangeState(chatID)
				GetMenuRegistry().SetKeyboard("temp_cal_range", customCumProfitDateRangeGranularityKeyboard(st.From.Unix(), to.Unix()))
				c.Reply(fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", st.From.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")), "temp_cal_range")
			}
			return nil
		}

	case telegramiface.CallbackRangeFrom:
		if len(route.Parts) != 1 {
			c.Reply("Invalid FROM range.", "menu_charts")
			return nil
		}
		fromToken := route.Parts[0]
		_, fromLabel, ok := parseRangeAgoToken(fromToken)
		if !ok {
			c.Reply("Invalid FROM range.", "menu_charts")
			return nil
		}
		setRangeFromSelection(chatID, fromToken)
		GetMenuRegistry().SetKeyboard("temp_range_to", customCumProfitRangeToKeyboard(fromToken))
		c.Reply(fmt.Sprintf("From set: %s ago. Choose TO:", fromLabel), "temp_range_to")
		return nil

	case telegramiface.CallbackRangeTo:
		if len(route.Parts) != 1 {
			c.Reply("Invalid TO range.", "menu_charts")
			return nil
		}
		toToken := route.Parts[0]
		fromToken, okFrom := getRangeFromSelection(chatID)
		if !okFrom {
			GetMenuRegistry().SetKeyboard("temp_range_from", customCumProfitRangeFromKeyboard())
			c.Reply("Please choose FROM first.", "temp_range_from")
			return nil
		}
		fromAgo, fromLabel, okA := parseRangeAgoToken(fromToken)
		toAgo, toLabel, okB := parseRangeAgoToken(toToken)
		if !okA || !okB || fromAgo <= toAgo {
			GetMenuRegistry().SetKeyboard("temp_range_to_err", customCumProfitRangeToKeyboard(fromToken))
			c.Reply("Invalid TO range. FROM must be older than TO.", "temp_range_to_err")
			return nil
		}
		GetMenuRegistry().SetKeyboard("temp_range_gran", customCumProfitRangeGranularityKeyboard(fromToken, toToken))
		c.Reply(fmt.Sprintf("Range: %s ago -> %s ago. Choose timeline:", fromLabel, toLabel), "temp_range_gran")
		return nil

	case telegramiface.CallbackRangeGran:
		if len(route.Parts) != 3 {
			c.Reply("Invalid range chart selection.", "menu_charts")
			return nil
		}
		fromAgo, fromLabel, okA := parseRangeAgoToken(route.Parts[0])
		toAgo, toLabel, okB := parseRangeAgoToken(route.Parts[1])
		if !okA || !okB || fromAgo <= toAgo {
			GetMenuRegistry().SetKeyboard("temp_range_from_err", customCumProfitRangeFromKeyboard())
			c.Reply("Invalid range. FROM must be older than TO.", "temp_range_from_err")
			return nil
		}
		mode, modeLabel := parseCumProfitGranularity(route.Parts[2])
		labels, values, unit := cumulativeProfitSeriesRangeMode(c.Ctx, c.Cfg, c.State, c.Binance, fromAgo, toAgo, mode)
		if len(labels) == 0 {
			c.Reply("No cumulative profit data in this range yet.", "menu_charts")
			return nil
		}
		now := time.Now().UTC()
		from := now.Add(-fromAgo)
		to := now.Add(-toAgo)
		c.State.addCustomRange(from, to)
		_ = c.State.save()
		clearRangeFromSelection(chatID)
		title := fmt.Sprintf("Cumulative Profit (%s ago -> %s ago, %s)", fromLabel, toLabel, modeLabel)
		chartMode := c.State.getChartLabelMode("staggered")
		chartURL := buildCumulativeProfitChartURL(title, labels, values, unit, theme, size, chartMode, chartLabels, grid)
		safeSendPhotoToChatWithMarkup(c.Notifier, chatID, chartURL, title, chartRefreshKeyboard(route.Raw))
		return nil

	case telegramiface.CallbackRangeHistory:
		if len(route.Parts) != 2 {
			c.Reply("Invalid range history item.", "menu_charts")
			return nil
		}
		fromSec, errA := strconv.ParseInt(route.Parts[0], 10, 64)
		toSec, errB := strconv.ParseInt(route.Parts[1], 10, 64)
		if errA != nil || errB != nil {
			c.Reply("Invalid range history value.", "menu_charts")
			return nil
		}
		from := time.Unix(fromSec, 0).UTC()
		to := time.Unix(toSec, 0).UTC()
		if !to.After(from) {
			c.Reply("Invalid history range.", "menu_charts")
			return nil
		}
		GetMenuRegistry().SetKeyboard("temp_cal_range", customCumProfitDateRangeGranularityKeyboard(from.Unix(), to.Unix()))
		c.Reply(fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")), "temp_cal_range")
		return nil

	case telegramiface.CallbackDateRangeGran:
		if len(route.Parts) != 3 {
			c.Reply("Invalid date range chart selection.", "menu_charts")
			return nil
		}
		fromSec, errA := strconv.ParseInt(route.Parts[0], 10, 64)
		toSec, errB := strconv.ParseInt(route.Parts[1], 10, 64)
		if errA != nil || errB != nil {
			c.Reply("Invalid date range values.", "menu_charts")
			return nil
		}
		from := time.Unix(fromSec, 0).UTC()
		to := time.Unix(toSec, 0).UTC()
		if !to.After(from) {
			c.Reply("Invalid range: TO must be after FROM.", "menu_charts")
			return nil
		}
		mode, modeLabel := parseCumProfitGranularity(route.Parts[2])
		labels, values, unit := cumulativeProfitSeriesBetweenMode(c.Ctx, c.Cfg, c.State, c.Binance, from, to, mode)
		if len(labels) == 0 {
			c.Reply("No cumulative profit data in this date range.", "menu_charts")
			return nil
		}
		c.State.addCustomRange(from, to)
		_ = c.State.save()
		title := fmt.Sprintf("Cumulative Profit (%s -> %s, %s)", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"), modeLabel)
		chartMode := c.State.getChartLabelMode("staggered")
		chartURL := buildCumulativeProfitChartURL(title, labels, values, unit, theme, size, chartMode, chartLabels, grid)
		safeSendPhotoToChatWithMarkup(c.Notifier, chatID, chartURL, title, chartRefreshKeyboard(route.Raw))
		return nil
	}
	return nil
}

func handlePeriodReport(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	dur := selectDuration(data)
	label := durationLabel(data)
	return sendPeriodReport(c.Ctx, c.Cfg, c.Binance, c.Notifier, c.State, dur, label)
}

func handleFeesReport(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	dur := selectDuration(data)
	title := durationLabel(data)
	v, err := totalFeesBNB(c.Ctx, c.Binance, c.Cfg.TrackedSymbols, c.Cfg.BNBAsset, dur)
	if err != nil {
		return err
	}
	spot := spotForDisplay(c.Ctx, c.Cfg, c.Binance, dur)
	mainCurrency := c.State.getDisplayCurrency(c.Cfg.FeeMainCurrency)
	feeText := formatFeeByMainCurrency(v, c.Cfg, mainCurrency, spot)
	note := ""
	if spot > 0 {
		if c.Cfg.FreqtradeHistoryMode {
			note = ", inferred from Freqtrade"
		} else {
			note = ", spot"
		}
	}
	c.Reply(fmt.Sprintf("Fees consumed (%s): %s%s", title, feeText, note), "menu_main")
	return nil
}

func handleFreqtradeRestart(c *MenuContext) error {
	data := c.Update.CallbackQuery.Data
	if data == "ft_restart_custom" {
		setAwaitingFreqtradeRestartInput(c.Update.CallbackQuery.Message.Chat.ID, true)
		c.Reply("Type restart delay (e.g. `10m`, `1h`, `1d`) or `cancel`:", "menu_actions")
		return nil
	}
	durTag := strings.TrimPrefix(data, "ft_restart_")
	dur, err := time.ParseDuration(durTag)
	if err != nil {
		return fmt.Errorf("invalid duration: %v", err)
	}
	restartAt := time.Now().UTC().Add(dur)
	c.State.setFreqtradeRestartAt(restartAt)
	_ = c.State.save()
	c.Reply(fmt.Sprintf("✅ Freqtrade restart scheduled at %s UTC (in %v).", restartAt.Format("15:04:05"), dur.Round(time.Second)), "menu_main")
	return nil
}
