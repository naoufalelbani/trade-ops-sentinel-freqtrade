package services

import "time"

func SelectDuration(key string) time.Duration {
	switch key {
	case "fees_day", "pnl_day", "report_day", "trades_day", "leaders_day", "chart_cum_fees_day", "chart_cum_profit_day":
		return 24 * time.Hour
	case "chart_cum_profit_48h":
		return 48 * time.Hour
	case "chart_cum_profit_72h":
		return 72 * time.Hour
	case "fees_week", "pnl_week", "report_week", "trades_week", "leaders_week", "chart_cum_fees_week", "chart_cum_profit_week":
		return 7 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

func DurationLabel(key string) string {
	switch key {
	case "fees_day", "pnl_day", "report_day", "trades_day", "leaders_day":
		return "day"
	case "fees_week", "pnl_week", "report_week", "trades_week", "leaders_week":
		return "week"
	default:
		return "month"
	}
}
