package main

import (
	"context"
	"strings"
)

type MenuContext struct {
	Ctx      context.Context
	Cfg      Config
	Binance  *BinanceClient
	Notifier *TelegramNotifier
	State    *MonitorState
	Update   tgUpdate
}

func (c *MenuContext) Reply(text string, menuID string) {
	kb := GetMenuRegistry().GetKeyboard(menuID)
	chatID := int64(0)
	if c.Update.Message != nil {
		chatID = c.Update.Message.Chat.ID
	} else if c.Update.CallbackQuery != nil && c.Update.CallbackQuery.Message != nil {
		chatID = c.Update.CallbackQuery.Message.Chat.ID
	}
	if chatID != 0 {
		safeSendToChat(c.Notifier, chatID, text, kb)
	}
}

type MenuHandler func(c *MenuContext) error

type MenuItem struct {
	Text         string
	CallbackData string
	Handler      MenuHandler
	SubMenuID    string // If set, clicking this button opens another menu
}

type Menu struct {
	ID    string
	Title string
	Rows  [][]MenuItem
}

type MenuRegistry struct {
	Menus    map[string]Menu
	Handlers map[string]MenuHandler
}

func NewMenuRegistry() *MenuRegistry {
	return &MenuRegistry{
		Menus:    make(map[string]Menu),
		Handlers: make(map[string]MenuHandler),
	}
}

func (r *MenuRegistry) RegisterMenu(m Menu) {
	r.Menus[m.ID] = m
}

func (r *MenuRegistry) RegisterHandler(callbackData string, handler MenuHandler) {
	r.Handlers[callbackData] = handler
}

func (r *MenuRegistry) SetKeyboard(menuID string, keyboard *inlineKeyboardMarkup) {
	if keyboard == nil {
		return
	}
	rows := make([][]MenuItem, 0, len(keyboard.InlineKeyboard))
	for _, row := range keyboard.InlineKeyboard {
		mRow := make([]MenuItem, 0, len(row))
		for _, btn := range row {
			mRow = append(mRow, MenuItem{
				Text:         btn.Text,
				CallbackData: btn.CallbackData,
			})
		}
		rows = append(rows, mRow)
	}
	r.Menus[menuID] = Menu{
		ID:   menuID,
		Rows: rows,
	}
}

func (r *MenuRegistry) GetKeyboard(menuID string) *inlineKeyboardMarkup {
	menu, ok := r.Menus[menuID]
	if !ok {
		return nil
	}

	keyboard := &inlineKeyboardMarkup{
		InlineKeyboard: make([][]inlineKeyboardButton, 0, len(menu.Rows)),
	}

	for _, row := range menu.Rows {
		kbRow := make([]inlineKeyboardButton, 0, len(row))
		for _, item := range row {
			kbRow = append(kbRow, inlineKeyboardButton{
				Text:         item.Text,
				CallbackData: item.CallbackData,
			})
		}
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, kbRow)
	}

	return keyboard
}

// FindHandler looks up a handler for the given callback data.
func (r *MenuRegistry) FindHandler(callbackData string) (MenuHandler, bool) {
	// 1. Exact match
	if h, ok := r.Handlers[callbackData]; ok {
		return h, true
	}

	// 2. Prefix match for dynamic routes (e.g., "stop_alert_", "ccpw_")
	for prefix, h := range r.Handlers {
		if strings.HasSuffix(prefix, "*") {
			p := strings.TrimSuffix(prefix, "*")
			if strings.HasPrefix(callbackData, p) {
				return h, true
			}
		}
	}

	// 3. Check if it's a menu open action
	if m, ok := r.Menus[callbackData]; ok {
		return func(c *MenuContext) error {
			safeSendToChat(c.Notifier, c.Update.CallbackQuery.Message.Chat.ID, m.Title, r.GetKeyboard(callbackData))
			return nil
		}, true
	}

	return nil, false
}

var (
	menuRegistry *MenuRegistry
)

func GetMenuRegistry() *MenuRegistry {
	if menuRegistry == nil {
		menuRegistry = NewMenuRegistry()
		menuRegistry.InitMenus()
	}
	return menuRegistry
}

func (r *MenuRegistry) InitMenus() {
	// Root Menu
	r.RegisterMenu(Menu{
		ID:    "menu_main",
		Title: "BNB monitor menu:",
		Rows: [][]MenuItem{
			{{Text: "Status", CallbackData: "status"}, {Text: "Actions", CallbackData: "menu_actions"}},
			{{Text: "Reports", CallbackData: "menu_reports"}, {Text: "Charts", CallbackData: "menu_charts"}},
			{{Text: "Settings", CallbackData: "menu_settings"}},
		},
	})

	// Actions Menu
	r.RegisterMenu(Menu{
		ID:    "menu_actions",
		Title: "Actions menu",
		Rows: [][]MenuItem{
			{{Text: "Refill Now", CallbackData: "refill_now"}, {Text: "Force Buy BNB", CallbackData: "force_buy"}},
			{{Text: "Daily Report Now", CallbackData: "daily_report_now"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	})

	// Reports Menu
	r.RegisterMenu(Menu{
		ID:    "menu_reports",
		Title: "Reports Menu:",
		Rows: [][]MenuItem{
			{{Text: "Summaries", CallbackData: "menu_reports_summary"}, {Text: "PnL & History", CallbackData: "menu_reports_pnl"}},
			{{Text: "Fees", CallbackData: "menu_reports_fees"}, {Text: "Trades", CallbackData: "menu_reports_trades"}},
			{{Text: "Leaders", CallbackData: "menu_reports_leaders"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	})

	// Reports Submenus
	r.RegisterMenu(Menu{
		ID:    "menu_reports_summary",
		Title: "Summary Reports:",
		Rows: [][]MenuItem{
			{{Text: "Daily", CallbackData: "report_day"}, {Text: "Weekly", CallbackData: "report_week"}, {Text: "Monthly", CallbackData: "report_month"}},
			{{Text: "Back", CallbackData: "menu_reports"}},
		},
	})
	r.RegisterMenu(Menu{
		ID:    "menu_reports_fees",
		Title: "Fee Reports:",
		Rows: [][]MenuItem{
			{{Text: "Fees D", CallbackData: "fees_day"}, {Text: "Fees W", CallbackData: "fees_week"}, {Text: "Fees M", CallbackData: "fees_month"}},
			{{Text: "Back", CallbackData: "menu_reports"}},
		},
	})
	r.RegisterMenu(Menu{
		ID:    "menu_reports_pnl",
		Title: "PnL Reports:",
		Rows: [][]MenuItem{
			{{Text: "PnL D", CallbackData: "pnl_day"}, {Text: "PnL W", CallbackData: "pnl_week"}, {Text: "PnL M", CallbackData: "pnl_month"}},
			{{Text: "PnL 7d Table", CallbackData: "pnl_7d_table"}, {Text: "📉 PnL History", CallbackData: "pnl_history_menu"}},
			{{Text: "Back", CallbackData: "menu_reports"}},
		},
	})
	r.RegisterMenu(Menu{
		ID:    "menu_reports_trades",
		Title: "Trade Reports:",
		Rows: [][]MenuItem{
			{{Text: "Trades D", CallbackData: "trades_day"}, {Text: "Trades W", CallbackData: "trades_week"}, {Text: "Trades M", CallbackData: "trades_month"}},
			{{Text: "Back", CallbackData: "menu_reports"}},
		},
	})
	r.RegisterMenu(Menu{
		ID:    "menu_reports_leaders",
		Title: "Leaderboards:",
		Rows: [][]MenuItem{
			{{Text: "Leaders D", CallbackData: "leaders_day"}, {Text: "Leaders W", CallbackData: "leaders_week"}, {Text: "Leaders M", CallbackData: "leaders_month"}},
			{{Text: "Back", CallbackData: "menu_reports"}},
		},
	})

	// PnL History Menu
	r.RegisterMenu(Menu{
		ID:    "pnl_history_menu",
		Title: "PnL History menu",
		Rows: [][]MenuItem{
			{{Text: "History 7d", CallbackData: "pnl_history_7d"}, {Text: "History 30d", CallbackData: "pnl_history_30d"}},
			{{Text: "History Custom", CallbackData: "pnl_history_custom"}},
			{{Text: "Back", CallbackData: "menu_reports"}},
		},
	})

	// Charts Menu
	r.RegisterMenu(Menu{
		ID:    "menu_charts",
		Title: "Charts Menu:",
		Rows: [][]MenuItem{
			{{Text: "Portfolio", CallbackData: "menu_charts_portfolio"}, {Text: "Forecasts", CallbackData: "menu_charts_forecast"}},
			{{Text: "Range Tools", CallbackData: "menu_charts_range"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	})

	// Charts Submenus
	r.RegisterMenu(Menu{
		ID:    "menu_charts_portfolio",
		Title: "Portfolio Charts:",
		Rows: [][]MenuItem{
			{{Text: "Fees Chart", CallbackData: "chart_fees"}, {Text: "PnL Chart", CallbackData: "chart_pnl"}},
			{{Text: "Cum Fees 24h", CallbackData: "chart_cum_fees_day"}, {Text: "Cum Fees 7d", CallbackData: "chart_cum_fees_week"}},
			{{Text: "Cum Profit 24h", CallbackData: "chart_cum_profit_day"}, {Text: "Cum Profit 7d", CallbackData: "chart_cum_profit_week"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	})

	r.RegisterMenu(Menu{
		ID:    "menu_charts_forecast",
		Title: "Forecast Charts:",
		Rows: [][]MenuItem{
			{{Text: "Predict 7d", CallbackData: "chart_predict_week"}, {Text: "Predict 30d", CallbackData: "chart_predict_month"}},
			{{Text: "Predict Cum 7d", CallbackData: "chart_predict_cum_week"}, {Text: "Predict Cum 30d", CallbackData: "chart_predict_cum_month"}},
			{{Text: "Compound 7d", CallbackData: "chart_compound_week"}, {Text: "Compound 30d", CallbackData: "chart_compound_month"}},
			{{Text: "More Options", CallbackData: "prediction_days_menu"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	})

	r.RegisterMenu(Menu{
		ID:    "menu_charts_range",
		Title: "Range & Custom Tools:",
		Rows: [][]MenuItem{
			{{Text: "Custom Window", CallbackData: "chart_cum_profit_custom"}},
			{{Text: "Range From->To", CallbackData: "chart_cum_profit_range"}},
			{{Text: "Range Date/Hour", CallbackData: "chart_cum_profit_date_range"}},
			{{Text: "Calendar Range", CallbackData: "chart_cum_profit_calendar_range"}},
			{{Text: "History", CallbackData: "chart_cum_profit_range_history"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	})

	// Settings Menu
	r.RegisterMenu(Menu{
		ID:    "menu_settings",
		Title: "Settings Menu:",
		Rows: [][]MenuItem{
			{{Text: "Display Settings", CallbackData: "menu_settings_display"}},
			{{Text: "Chart Configuration", CallbackData: "menu_settings_charts"}},
			{{Text: "System & Alerts", CallbackData: "menu_settings_system"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	})

	// Settings Submenus
	r.RegisterMenu(Menu{
		ID:    "menu_settings_display",
		Title: "Display Settings:",
		Rows: [][]MenuItem{
			{{Text: "Currency", CallbackData: "fee_currency_menu"}},
			{{Text: "PnL Emojis", CallbackData: "pnl_emoji_menu"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	})

	r.RegisterMenu(Menu{
		ID:    "menu_settings_charts",
		Title: "Chart Settings:",
		Rows: [][]MenuItem{
			{{Text: "Theme", CallbackData: "chart_theme_menu"}, {Text: "Size", CallbackData: "chart_size_menu"}},
			{{Text: "Labels", CallbackData: "chart_labels_menu"}, {Text: "Grid", CallbackData: "chart_grid_menu"}},
			{{Text: "Label Mode", CallbackData: "chart_mode_menu"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	})

	r.RegisterMenu(Menu{
		ID:    "menu_settings_system",
		Title: "System Settings:",
		Rows: [][]MenuItem{
			{{Text: "Alert Settings", CallbackData: "alerts_menu"}},
			{{Text: "Freqtrade Health", CallbackData: "freqtrade_health"}},
			{{Text: "Settings Overview", CallbackData: "settings_overview"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	})

	// Prediction Days Menu
	r.RegisterMenu(Menu{
		ID:    "prediction_days_menu",
		Title: "Choose prediction horizon:",
		Rows: [][]MenuItem{
			{{Text: "7 days", CallbackData: "chart_predict_week"}, {Text: "14 days", CallbackData: "chart_predict_14d"}, {Text: "30 days", CallbackData: "chart_predict_month"}},
			{{Text: "60 days", CallbackData: "chart_predict_60d"}, {Text: "Custom Input", CallbackData: "chart_predict_custom"}},
			{{Text: "Cum 7 days", CallbackData: "chart_predict_cum_week"}, {Text: "Cum 30 days", CallbackData: "chart_predict_cum_month"}},
			{{Text: "Cum 60 days", CallbackData: "chart_predict_cum_60d"}, {Text: "Cum Custom", CallbackData: "chart_predict_cum_custom"}},
			{{Text: "Compound 7 days", CallbackData: "chart_compound_week"}, {Text: "Compound 30 days", CallbackData: "chart_compound_month"}},
			{{Text: "Compound Custom", CallbackData: "chart_compound_custom"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	})

	// Force Buy Confirmation Menu
	r.RegisterMenu(Menu{
		ID:    "force_buy_confirm_menu",
		Title: "Force buy BNB?\nThis will place a market order now (uses safety caps).",
		Rows: [][]MenuItem{
			{{Text: "Confirm Market Buy", CallbackData: "force_buy_confirm"}},
			{{Text: "Cancel", CallbackData: "force_buy_cancel"}},
		},
	})

	// Handlers for core actions are now registered via RegisterAllHandlers
	RegisterAllHandlers(r)
}

