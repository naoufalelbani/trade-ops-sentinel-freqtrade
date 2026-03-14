package main

import (
	"context"
	"fmt"
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
		Title: "Reports menu",
		Rows: [][]MenuItem{
			{{Text: "Daily", CallbackData: "report_day"}, {Text: "Weekly", CallbackData: "report_week"}, {Text: "Monthly", CallbackData: "report_month"}},
			{{Text: "Fees D", CallbackData: "fees_day"}, {Text: "Fees W", CallbackData: "fees_week"}, {Text: "Fees M", CallbackData: "fees_month"}},
			{{Text: "PnL D", CallbackData: "pnl_day"}, {Text: "PnL W", CallbackData: "pnl_week"}, {Text: "PnL M", CallbackData: "pnl_month"}},
			{{Text: "Trades D", CallbackData: "trades_day"}, {Text: "Trades W", CallbackData: "trades_week"}, {Text: "Trades M", CallbackData: "trades_month"}},
			{{Text: "Leaders D", CallbackData: "leaders_day"}, {Text: "Leaders W", CallbackData: "leaders_week"}, {Text: "Leaders M", CallbackData: "leaders_month"}},
			{{Text: "PnL 7d Table", CallbackData: "pnl_7d_table"}, {Text: "📉 PnL History", CallbackData: "pnl_history_menu"}},
			{{Text: "Back", CallbackData: "menu_main"}},
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
		Title: "Charts menu",
		Rows: [][]MenuItem{
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
	})

	// Settings Menu
	r.RegisterMenu(Menu{
		ID:    "menu_settings",
		Title: "Settings menu",
		Rows: [][]MenuItem{
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

