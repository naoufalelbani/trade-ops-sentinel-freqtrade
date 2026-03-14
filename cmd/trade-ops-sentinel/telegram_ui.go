package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func defaultKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Status", CallbackData: "status"}, {Text: "Actions", CallbackData: "menu_actions"}},
			{{Text: "Reports", CallbackData: "menu_reports"}, {Text: "Charts", CallbackData: "menu_charts"}},
			{{Text: "Settings", CallbackData: "menu_settings"}},
		},
	}
}

func stopAlertKeyboard(key string) *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "🛑 Stop Notification", CallbackData: "stop_alert_" + key}},
		},
	}
}

func freqtradeStoppedKeyboard(key string) *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Restart 10m", CallbackData: "ft_restart_10m"}, {Text: "Restart 30m", CallbackData: "ft_restart_30m"}},
			{{Text: "Restart 1h", CallbackData: "ft_restart_1h"}, {Text: "Custom Restart", CallbackData: "ft_restart_custom"}},
			{{Text: "🛑 Stop Notification", CallbackData: "stop_alert_" + key}},
		},
	}
}

func actionsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Refill Now", CallbackData: "refill_now"}, {Text: "Force Buy BNB", CallbackData: "force_buy"}},
			{{Text: "Daily Report Now", CallbackData: "daily_report_now"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func reportsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Daily", CallbackData: "report_day"}, {Text: "Weekly", CallbackData: "report_week"}, {Text: "Monthly", CallbackData: "report_month"}},
			{{Text: "Fees D", CallbackData: "fees_day"}, {Text: "Fees W", CallbackData: "fees_week"}, {Text: "Fees M", CallbackData: "fees_month"}},
			{{Text: "PnL D", CallbackData: "pnl_day"}, {Text: "PnL W", CallbackData: "pnl_week"}, {Text: "PnL M", CallbackData: "pnl_month"}},
			{{Text: "Trades D", CallbackData: "trades_day"}, {Text: "Trades W", CallbackData: "trades_week"}, {Text: "Trades M", CallbackData: "trades_month"}},
			{{Text: "Leaders D", CallbackData: "leaders_day"}, {Text: "Leaders W", CallbackData: "leaders_week"}, {Text: "Leaders M", CallbackData: "leaders_month"}},
			{{Text: "PnL 7d Table", CallbackData: "pnl_7d_table"}, {Text: "📉 PnL History", CallbackData: "pnl_history_menu"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func pnlHistoryMenuKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "History 7d", CallbackData: "pnl_history_7d"}, {Text: "History 30d", CallbackData: "pnl_history_30d"}},
			{{Text: "History Custom", CallbackData: "pnl_history_custom"}},
			{{Text: "Back", CallbackData: "menu_reports"}},
		},
	}
}

func chartsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Fees Chart", CallbackData: "chart_fees"}, {Text: "PnL Chart", CallbackData: "chart_pnl"}},
			{{Text: "Predict 7d", CallbackData: "chart_predict_week"}, {Text: "Predict 30d", CallbackData: "chart_predict_month"}},
			{{Text: "Predict Cum 7d", CallbackData: "chart_predict_cum_week"}, {Text: "Predict Cum 30d", CallbackData: "chart_predict_cum_month"}},
			{{Text: "Predict Custom", CallbackData: "chart_predict_custom"}, {Text: "Predict Cum Custom", CallbackData: "chart_predict_cum_custom"}},
			{{Text: "Compound 7d", CallbackData: "chart_compound_week"}, {Text: "Compound 30d", CallbackData: "chart_compound_month"}},
			{{Text: "Compound Custom", CallbackData: "chart_compound_custom"}},
			{{Text: "Cum Fees 24h", CallbackData: "chart_cum_fees_day"}, {Text: "Cum Fees 7d", CallbackData: "chart_cum_fees_week"}, {Text: "Cum Fees 30d", CallbackData: "chart_cum_fees_month"}},
			{{Text: "Cum Profit 24h", CallbackData: "chart_cum_profit_day"}, {Text: "Cum Profit 48h", CallbackData: "chart_cum_profit_48h"}, {Text: "Cum Profit 72h", CallbackData: "chart_cum_profit_72h"}},
			{{Text: "Cum Profit 7d", CallbackData: "chart_cum_profit_week"}, {Text: "Cum Profit 30d", CallbackData: "chart_cum_profit_month"}},
			{{Text: "Cum Profit Custom", CallbackData: "chart_cum_profit_custom"}, {Text: "Custom History", CallbackData: "chart_cum_profit_custom_history"}},
			{{Text: "Range From->To", CallbackData: "chart_cum_profit_range"}},
			{{Text: "Range Date&Hour", CallbackData: "chart_cum_profit_date_range"}, {Text: "Calendar Range", CallbackData: "chart_cum_profit_calendar_range"}},
			{{Text: "Range History", CallbackData: "chart_cum_profit_range_history"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func predictionDaysKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "7 days", CallbackData: "chart_predict_week"}, {Text: "14 days", CallbackData: "chart_predict_14d"}, {Text: "30 days", CallbackData: "chart_predict_month"}},
			{{Text: "60 days", CallbackData: "chart_predict_60d"}, {Text: "Custom Input", CallbackData: "chart_predict_custom"}},
			{{Text: "Cum 7 days", CallbackData: "chart_predict_cum_week"}, {Text: "Cum 30 days", CallbackData: "chart_predict_cum_month"}},
			{{Text: "Cum 60 days", CallbackData: "chart_predict_cum_60d"}, {Text: "Cum Custom", CallbackData: "chart_predict_cum_custom"}},
			{{Text: "Compound 7 days", CallbackData: "chart_compound_week"}, {Text: "Compound 30 days", CallbackData: "chart_compound_month"}},
			{{Text: "Compound Custom", CallbackData: "chart_compound_custom"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitWindowKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "24h", CallbackData: "ccpw_24h"}, {Text: "48h", CallbackData: "ccpw_48h"}, {Text: "72h", CallbackData: "ccpw_72h"}},
			{{Text: "3d", CallbackData: "ccpw_3d"}, {Text: "5d", CallbackData: "ccpw_5d"}, {Text: "7d", CallbackData: "ccpw_7d"}},
			{{Text: "14d", CallbackData: "ccpw_14d"}, {Text: "30d", CallbackData: "ccpw_30d"}},
			{{Text: "History", CallbackData: "chart_cum_profit_custom_history"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitGranularityKeyboard(windowToken string) *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Minutes", CallbackData: "ccpg_" + windowToken + "_m"}, {Text: "Hours", CallbackData: "ccpg_" + windowToken + "_h"}, {Text: "Days", CallbackData: "ccpg_" + windowToken + "_d"}},
			{{Text: "Trades", CallbackData: "ccpg_" + windowToken + "_t"}, {Text: "Auto", CallbackData: "ccpg_" + windowToken + "_a"}},
			{{Text: "Window", CallbackData: "chart_cum_profit_custom"}, {Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitHistoryKeyboard(tokens []string) *inlineKeyboardMarkup {
	rows := make([][]inlineKeyboardButton, 0, 8)
	for i := 0; i < len(tokens); i += 3 {
		row := make([]inlineKeyboardButton, 0, 3)
		for j := i; j < i+3 && j < len(tokens); j++ {
			t := strings.ToLower(strings.TrimSpace(tokens[j]))
			if t == "" {
				continue
			}
			row = append(row, inlineKeyboardButton{Text: t, CallbackData: "ccpw_" + t})
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Custom Input", CallbackData: "chart_cum_profit_custom"},
		{Text: "Back", CallbackData: "menu_charts"},
	})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitRangeFromKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "24h ago", CallbackData: "cprf_24h"}, {Text: "48h ago", CallbackData: "cprf_48h"}, {Text: "72h ago", CallbackData: "cprf_72h"}},
			{{Text: "7d ago", CallbackData: "cprf_7d"}, {Text: "14d ago", CallbackData: "cprf_14d"}, {Text: "30d ago", CallbackData: "cprf_30d"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitRangeToKeyboard(fromToken string) *inlineKeyboardMarkup {
	rows := [][]inlineKeyboardButton{
		{{Text: "now", CallbackData: "cprt_now"}, {Text: "12h ago", CallbackData: "cprt_12h"}, {Text: "24h ago", CallbackData: "cprt_24h"}},
		{{Text: "48h ago", CallbackData: "cprt_48h"}, {Text: "72h ago", CallbackData: "cprt_72h"}, {Text: "7d ago", CallbackData: "cprt_7d"}},
		{{Text: "From", CallbackData: "chart_cum_profit_range"}, {Text: "Back", CallbackData: "menu_charts"}},
	}
	_ = fromToken
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitRangeGranularityKeyboard(fromToken, toToken string) *inlineKeyboardMarkup {
	prefix := "cprg_" + fromToken + "_" + toToken + "_"
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Minutes", CallbackData: prefix + "m"}, {Text: "Hours", CallbackData: prefix + "h"}, {Text: "Days", CallbackData: prefix + "d"}},
			{{Text: "Trades", CallbackData: prefix + "t"}, {Text: "Auto", CallbackData: prefix + "a"}},
			{{Text: "To", CallbackData: "cprf_" + fromToken}, {Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitDateRangeGranularityKeyboard(fromTS, toTS int64) *inlineKeyboardMarkup {
	prefix := fmt.Sprintf("cpdtg_%d_%d_", fromTS, toTS)
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Minutes", CallbackData: prefix + "m"}, {Text: "Hours", CallbackData: prefix + "h"}, {Text: "Days", CallbackData: prefix + "d"}},
			{{Text: "Trades", CallbackData: prefix + "t"}, {Text: "Auto", CallbackData: prefix + "a"}},
			{{Text: "New Date Range", CallbackData: "chart_cum_profit_date_range"}, {Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitDateRangeEntryKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Type Manually", CallbackData: "chart_cum_profit_date_range_manual"}},
			{{Text: "Open Calendar", CallbackData: "chart_cum_profit_calendar_range"}},
			{{Text: "Range History", CallbackData: "chart_cum_profit_range_history"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func chartRefreshKeyboard(action string) *inlineKeyboardMarkup {
	refreshAction := strings.TrimSpace(action)
	if refreshAction == "" {
		refreshAction = "menu_charts"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Refresh", CallbackData: "refresh_" + refreshAction}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitRangeHistoryKeyboard(history []rangeRecord) *inlineKeyboardMarkup {
	rows := make([][]inlineKeyboardButton, 0, len(history)+1)
	for _, h := range history {
		from := time.Unix(h.FromTS, 0).UTC()
		to := time.Unix(h.ToTS, 0).UTC()
		if !to.After(from) {
			continue
		}
		label := fmt.Sprintf("%s -> %s", from.Format("01-02 15:04"), to.Format("01-02 15:04"))
		cb := fmt.Sprintf("cprh_%d_%d", h.FromTS, h.ToTS)
		rows = append(rows, []inlineKeyboardButton{{Text: label, CallbackData: cb}})
	}
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Back", CallbackData: "menu_charts"},
	})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitCalendarKeyboard(phase string, year int, month time.Month) *inlineKeyboardMarkup {
	if phase != "from" && phase != "to" {
		phase = "from"
	}
	rows := make([][]inlineKeyboardButton, 0, 10)
	title := fmt.Sprintf("%s %04d", month.String(), year)
	rows = append(rows, []inlineKeyboardButton{{Text: title, CallbackData: "ccal_ignore"}})
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Mo", CallbackData: "ccal_ignore"},
		{Text: "Tu", CallbackData: "ccal_ignore"},
		{Text: "We", CallbackData: "ccal_ignore"},
		{Text: "Th", CallbackData: "ccal_ignore"},
		{Text: "Fr", CallbackData: "ccal_ignore"},
		{Text: "Sa", CallbackData: "ccal_ignore"},
		{Text: "Su", CallbackData: "ccal_ignore"},
	})

	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	daysInMonth := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	weekday := int(first.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	offset := weekday - 1 // monday-based

	day := 1
	for rowIdx := 0; rowIdx < 6 && day <= daysInMonth; rowIdx++ {
		row := make([]inlineKeyboardButton, 0, 7)
		for col := 0; col < 7; col++ {
			if rowIdx == 0 && col < offset {
				row = append(row, inlineKeyboardButton{Text: " ", CallbackData: "ccal_ignore"})
				continue
			}
			if day > daysInMonth {
				row = append(row, inlineKeyboardButton{Text: " ", CallbackData: "ccal_ignore"})
				continue
			}
			dateToken := fmt.Sprintf("%04d%02d%02d", year, int(month), day)
			row = append(row, inlineKeyboardButton{
				Text:         strconv.Itoa(day),
				CallbackData: "ccal_" + phase + "_day_" + dateToken,
			})
			day++
		}
		rows = append(rows, row)
	}

	prev := first.AddDate(0, -1, 0)
	next := first.AddDate(0, 1, 0)
	rows = append(rows, []inlineKeyboardButton{
		{Text: "<", CallbackData: fmt.Sprintf("ccal_%s_nav_%04d%02d", phase, prev.Year(), int(prev.Month()))},
		{Text: ">", CallbackData: fmt.Sprintf("ccal_%s_nav_%04d%02d", phase, next.Year(), int(next.Month()))},
	})
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Back", CallbackData: "menu_charts"},
	})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitHourKeyboard(phase string) *inlineKeyboardMarkup {
	if phase != "from" && phase != "to" {
		phase = "from"
	}
	rows := make([][]inlineKeyboardButton, 0, 8)
	for i := 0; i < 24; i += 6 {
		row := make([]inlineKeyboardButton, 0, 6)
		for j := 0; j < 6; j++ {
			h := i + j
			row = append(row, inlineKeyboardButton{
				Text:         fmt.Sprintf("%02d:00", h),
				CallbackData: fmt.Sprintf("ccal_%s_hour_%02d", phase, h),
			})
		}
		rows = append(rows, row)
	}
	rows = append(rows, []inlineKeyboardButton{{Text: "Back", CallbackData: "chart_cum_profit_calendar_range"}})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func settingsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Currency", CallbackData: "fee_currency_menu"}},
			{{Text: "Chart Theme", CallbackData: "chart_theme_menu"}},
			{{Text: "Chart Size", CallbackData: "chart_size_menu"}},
			{{Text: "Chart Labels", CallbackData: "chart_labels_menu"}},
			{{Text: "Chart Grid", CallbackData: "chart_grid_menu"}},
			{{Text: "Chart Mode", CallbackData: "chart_mode_menu"}},
			{{Text: "PnL Emojis", CallbackData: "pnl_emoji_menu"}},
			{{Text: "Alert Settings", CallbackData: "alerts_menu"}},
			{{Text: "Settings Overview", CallbackData: "settings_overview"}},
			{{Text: "Freqtrade Health", CallbackData: "freqtrade_health"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func defaultReplyKeyboard() *replyKeyboardMarkup {
	return &replyKeyboardMarkup{
		Keyboard: [][]keyboardButton{
			{{Text: "Status"}, {Text: "Daily Report"}},
			{{Text: "Menu"}, {Text: "Help"}},
		},
		ResizeKeyboard: true,
	}
}

func forceBuyConfirmKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Confirm Force Buy", CallbackData: "force_buy_confirm"}, {Text: "Cancel", CallbackData: "force_buy_cancel"}},
			{{Text: "Menu", CallbackData: "menu"}},
		},
	}
}

func feeCurrencyKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "BNB", CallbackData: "fee_currency_bnb"}, {Text: "USDT", CallbackData: "fee_currency_usdt"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func chartThemeKeyboard(current string) *inlineKeyboardMarkup {
	label := strings.ToUpper(strings.TrimSpace(current))
	if label != "LIGHT" {
		label = "DARK"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Dark", CallbackData: "chart_theme_dark"}, {Text: "Light", CallbackData: "chart_theme_light"}},
			{{Text: "Current: " + label, CallbackData: "settings_ignore"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func alertSettingsKeyboard(heartbeatEnabled, apiEnabled bool) *inlineKeyboardMarkup {
	heartbeatLabel := "Heartbeat: OFF"
	if heartbeatEnabled {
		heartbeatLabel = "Heartbeat: ON"
	}
	apiLabel := "API Alerts: OFF"
	if apiEnabled {
		apiLabel = "API Alerts: ON"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "HB On", CallbackData: "alert_heartbeat_on"}, {Text: "HB Off", CallbackData: "alert_heartbeat_off"}},
			{{Text: "API On", CallbackData: "alert_api_on"}, {Text: "API Off", CallbackData: "alert_api_off"}},
			{{Text: heartbeatLabel, CallbackData: "settings_ignore"}, {Text: apiLabel, CallbackData: "settings_ignore"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func chartSizeKeyboard(current string) *inlineKeyboardMarkup {
	c := strings.ToLower(strings.TrimSpace(current))
	if c != "compact" && c != "wide" {
		c = "standard"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Compact", CallbackData: "chart_size_compact"}, {Text: "Standard", CallbackData: "chart_size_standard"}},
			{{Text: "Wide", CallbackData: "chart_size_wide"}},
			{{Text: "Current: " + strings.Title(c), CallbackData: "settings_ignore"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func chartLabelsKeyboard(enabled bool) *inlineKeyboardMarkup {
	label := "Current: OFF"
	if enabled {
		label = "Current: ON"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Labels ON", CallbackData: "chart_labels_on"}, {Text: "Labels OFF", CallbackData: "chart_labels_off"}},
			{{Text: label, CallbackData: "settings_ignore"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func chartGridKeyboard(enabled bool) *inlineKeyboardMarkup {
	label := "Current: OFF"
	if enabled {
		label = "Current: ON"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Grid ON", CallbackData: "chart_grid_on"}, {Text: "Grid OFF", CallbackData: "chart_grid_off"}},
			{{Text: label, CallbackData: "settings_ignore"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func pnlEmojiKeyboard(enabled bool) *inlineKeyboardMarkup {
	label := "Current: OFF"
	if enabled {
		label = "Current: ON"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Emojis ON", CallbackData: "pnl_emoji_on"}, {Text: "Emojis OFF", CallbackData: "pnl_emoji_off"}},
			{{Text: label, CallbackData: "settings_ignore"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func chartLabelModeKeyboard(current string) *inlineKeyboardMarkup {
	c := strings.ToLower(strings.TrimSpace(current))
	if c != "horizontal" && c != "vertical" {
		c = "staggered"
	}
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Horizontal", CallbackData: "chart_mode_horizontal"}, {Text: "Vertical", CallbackData: "chart_mode_vertical"}},
			{{Text: "Staggered", CallbackData: "chart_mode_staggered"}},
			{{Text: "Current: " + strings.Title(c), CallbackData: "settings_ignore"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func helpText() string {
	return strings.Join([]string{
		"Trade Ops Sentinel - Help",
		"",
		"Commands:",
		"/start or /menu - open menu",
		"/status - snapshot (balance, fees, pnl, system, watchdog)",
		"/daily - full daily report + charts",
		"/version - app version, commit, build date",
		"/help - this help",
		"",
		"Menu sections:",
		"Actions: Refill Now, Force Buy BNB, Daily Report Now",
		"Reports: day/week/month for Report, Fees, PnL, Trades, Leaders, plus PnL 7d table",
		"Charts: fees, pnl, cumulative fees/profit, custom windows, range tools",
		"Prediction charts: daily/cumulative and compound forecast, next 7d/30d or custom days (3..365)",
		"Settings: currency, chart theme/size/labels/grid, pnl emojis, alert toggles, Freqtrade Health",
		"",
		"Chart options:",
		"Prediction: daily/cumulative next 7d / 30d or custom horizon (3..365 days)",
		"Compound forecast (Freqtrade): uses max_open_trades + balance + trade-return model",
		"Cum Profit presets: 24h, 48h, 72h, 7d, 30d",
		"Cum Profit Custom: choose preset buttons or type window (examples: 36h, 3d, 10d)",
		"Custom History: re-use previous custom windows",
		"Range From->To: relative range picker (from/to ago) + timeline",
		"Range Date&Hour: exact UTC datetime input for from/to",
		"Calendar Range: pick FROM/TO date via calendar + hour buttons",
		"Range History: re-use last 5 saved ranges",
		"Timeline mode: Minutes / Hours / Days / Trades / Auto",
		"",
		"Date/time format (manual range):",
		"Use UTC. Accepted: YYYY-MM-DD HH:MM, YYYY-MM-DD HH, YYYY-MM-DD",
		"Example: 2026-03-07 14:30",
		"Type 'cancel' or 'back' to stop custom/date input flows",
		"",
		"Reliability / health:",
		"Freqtrade Health shows API checks + dashboard",
		"Watchdog tracks stale heartbeat and recovery/restart info",
		"",
		"Notes:",
		"Display currency affects reports/charts values (BNB or USDT)",
		"Some range/history features need prior usage before history appears",
	}, "\n")
}
