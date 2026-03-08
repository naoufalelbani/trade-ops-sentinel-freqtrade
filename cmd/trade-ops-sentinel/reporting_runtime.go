package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
	"trade-ops-sentinel/internal/infra/worldtime"
	"trade-ops-sentinel/internal/services"
)

func dailyReportLoop(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState) {
	if !cfg.DailyReportEnabled {
		log.Print("daily report loop disabled")
		return
	}
	hour, minute, err := parseHHMM(cfg.DailyReportTimeUTC)
	if err != nil {
		log.Printf("daily report time parse error (%s): %v; fallback 00:05", cfg.DailyReportTimeUTC, err)
		hour, minute = 0, 5
	}

	for {
		loc, tzName := resolveDailyTimezone(ctx, cfg, runtimeAlerts)
		nextRun := nextDailyRun(time.Now().In(loc), hour, minute, loc)
		wait := time.Until(nextRun)
		if wait < 0 {
			wait = 10 * time.Second
		}
		log.Printf("next daily report scheduled at %s (%s)", nextRun.Format(time.RFC3339), tzName)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := sendDailyReport(ctx, cfg, binance, notifier, state); err != nil {
			log.Printf("daily report error: %v", err)
			if runtimeAlerts != nil {
				runtimeAlerts.recordError("daily.report", err)
			}
			safeSend(notifier, fmt.Sprintf("Daily report failed: %v", err), defaultKeyboard())
		}
	}
}

func sendDailyReport(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState) error {
	started := time.Now()
	defer logTiming("send_daily_report", started)
	mode := strings.ToLower(strings.TrimSpace(cfg.DailyReportMode))
	if mode == "" {
		mode = "full"
	}
	var (
		report string
		err    error
	)
	if mode == "digest" {
		report, err = buildDailyDigest(ctx, cfg, binance, state, runtimeAlerts)
	} else {
		report, err = buildDailyReport(ctx, cfg, binance, state)
	}
	if err != nil {
		return err
	}
	if err := notifier.Send(report, defaultKeyboard()); err != nil {
		return err
	}
	if mode == "digest" {
		return nil
	}
	chartTheme := state.getChartTheme("dark")
	chartSize := state.getChartSize("standard")
	chartLabels := state.getChartLabelsEnabled(true)
	chartGrid := state.getChartGridEnabled(true)

	feeLabels, feeVals, err := feeSeriesLastNDaysCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, 30)
	if err != nil {
		log.Printf("daily fee chart generation error: %v", err)
	} else if len(feeLabels) > 0 {
		chartURL := buildLineChartURL("BNB Fees (Last 30 Days)", feeLabels, feeVals, cfg.BNBAsset, chartTheme, chartSize, chartLabels, chartGrid)
		safeSendPhoto(notifier, chartURL, "Daily Report: Fees (30d)")
	}

	portLabels, portVals := state.portfolioSeriesLastNDays(30)
	if len(portLabels) > 0 {
		chartURL := buildLineChartURL("Portfolio Value (Last 30 Days)", portLabels, portVals, cfg.QuoteAsset, chartTheme, chartSize, chartLabels, chartGrid)
		safeSendPhoto(notifier, chartURL, "Daily Report: Portfolio (30d)")
	}

	pnlLabels, pnlVals := state.pnlSeriesLastNDays(30)
	if len(pnlLabels) > 0 {
		chartURL := buildLineChartURL("PnL Delta (Last 30 Days)", pnlLabels, pnlVals, cfg.QuoteAsset, chartTheme, chartSize, chartLabels, chartGrid)
		safeSendPhoto(notifier, chartURL, "Daily Report: PnL Delta (30d)")
	}
	return nil
}

func sendPeriodReport(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState, d time.Duration, label string) error {
	report, err := buildPeriodReport(ctx, cfg, binance, state, d, label)
	if err != nil {
		return err
	}
	if err := notifier.Send(report, defaultKeyboard()); err != nil {
		return err
	}
	if cfg.FreqtradeHistoryMode {
		chartTheme := state.getChartTheme("dark")
		chartSize := state.getChartSize("standard")
		chartLabels := state.getChartLabelsEnabled(true)
		chartGrid := state.getChartGridEnabled(true)
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			logIfErr("period.freqtrade.fetch_trades", err)
			return nil
		}
		var feeLabels []string
		var feeVals []float64
		var pnlLabels []string
		var pnlVals []float64
		switch label {
		case "day":
			feeLabels, feeVals = freqtradeFeeSeriesByHour(trades, cfg.BNBAsset, 24)
			pnlLabels, pnlVals = freqtradePnlSeriesByHour(trades, 24)
		case "week":
			feeLabels, feeVals = freqtradeFeeSeriesByDay(trades, cfg.BNBAsset, 7)
			pnlLabels, pnlVals = freqtradePnlSeriesByDay(trades, 7)
		default:
			feeLabels, feeVals = freqtradeFeeSeriesByDay(trades, cfg.BNBAsset, 30)
			pnlLabels, pnlVals = freqtradePnlSeriesByDay(trades, 30)
		}
		if len(feeLabels) > 0 {
			chartURL := buildLineChartURL(fmt.Sprintf("Fees (%s)", strings.Title(label)), feeLabels, feeVals, cfg.BNBAsset, chartTheme, chartSize, chartLabels, chartGrid)
			safeSendPhoto(notifier, chartURL, fmt.Sprintf("Fees chart (%s)", label))
		}
		if len(pnlLabels) > 0 {
			chartURL := buildLineChartURL(fmt.Sprintf("PnL (%s)", strings.Title(label)), pnlLabels, pnlVals, cfg.QuoteAsset, chartTheme, chartSize, chartLabels, chartGrid)
			safeSendPhoto(notifier, chartURL, fmt.Sprintf("PnL chart (%s)", label))
		}
	}
	return nil
}

func buildDailyDigest(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState, alerts *alertManager) (string, error) {
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}
	fees, err := getFeeSummaryCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset)
	if err != nil {
		return "", err
	}
	pnlDay, pctDay, okDay := state.pnlSince(24 * time.Hour)
	pnlWeek, pctWeek, okWeek := state.pnlSince(7 * 24 * time.Hour)
	if cfg.FreqtradeHistoryMode {
		trades, ftErr := getFreqtradeTrades30dCached(ctx, cfg)
		if ftErr == nil {
			pnlDay, pctDay, okDay = freqtradePnlSince(trades, time.Now().UTC().Add(-24*time.Hour))
			pnlWeek, pctWeek, okWeek = freqtradePnlSince(trades, time.Now().UTC().Add(-7*24*time.Hour))
		}
	}

	lastTrades := buildLastTradesDigest(ctx, cfg, binance, state)
	errors24h := alerts.recentErrorsSince(24*time.Hour, 3)
	errLines := "none"
	if len(errors24h) > 0 {
		var b strings.Builder
		for i := 0; i < len(errors24h); i++ {
			ev := errors24h[i]
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(fmt.Sprintf("%s %s: %s", ev.TS.Format("15:04"), ev.Source, compactErr(ev.Err, 90)))
		}
		errLines = b.String()
	}

	pnlDayLine := "n/a"
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
	if okDay {
		pnlDayLine = fmt.Sprintf("%s (%.2f%%)", formatQuoteByDisplay(pnlDay, cfg, displayCurrency, spot), pctDay)
	}
	pnlWeekLine := "n/a"
	if okWeek {
		pnlWeekLine = fmt.Sprintf("%s (%.2f%%)", formatQuoteByDisplay(pnlWeek, cfg, displayCurrency, spot), pctWeek)
	}
	dayFeeText := formatFeeByMainCurrency(fees.Day, cfg, displayCurrency, spot)
	weekFeeText := formatFeeByMainCurrency(fees.Week, cfg, displayCurrency, spot)
	monthFeeText := formatFeeByMainCurrency(fees.Month, cfg, displayCurrency, spot)
	quoteFreeText := formatQuoteByDisplay(quoteFree, cfg, displayCurrency, spot)
	portfolioText := formatQuoteByDisplay(portfolioQuote, cfg, displayCurrency, spot)

	return fmt.Sprintf(
		"Daily Digest (%s UTC)\nBalance: BNB %.6f | %s | Portfolio %s\nPrice %s: %.4f\nFees: D %s | W %s | M %s\nPnL: D %s | W %s\nLast trades: %s\nErrors(24h): %s",
		time.Now().UTC().Format("2006-01-02 15:04"),
		bnbFree,
		quoteFreeText,
		portfolioText,
		cfg.Symbol,
		price,
		dayFeeText,
		weekFeeText,
		monthFeeText,
		pnlDayLine,
		pnlWeekLine,
		lastTrades,
		errLines,
	), nil
}

func buildLastTradesDigest(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState) string {
	n := cfg.DailyDigestTrades
	if n <= 0 {
		n = 3
	}
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return "n/a (" + compactErr(err.Error(), 80) + ")"
		}
		sort.Slice(trades, func(i, j int) bool {
			return freqtradeTradeLatestTS(trades[i]) > freqtradeTradeLatestTS(trades[j])
		})
		lines := make([]string, 0, n)
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
		for _, tr := range trades {
			ts := freqtradeTradeLatestTS(tr)
			if ts <= 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s %s", normalizePairToSymbol(tr.Pair), formatQuoteByDisplay(tr.ProfitAbs, cfg, displayCurrency, spot)))
			if len(lines) >= n {
				break
			}
		}
		if len(lines) == 0 {
			return "none"
		}
		return strings.Join(lines, " | ")
	}

	trades, err := collectTradesByDuration(ctx, binance, cfg.TrackedSymbols, 24*time.Hour)
	if err != nil {
		return "n/a (" + compactErr(err.Error(), 80) + ")"
	}
	if len(trades) == 0 {
		return "none"
	}
	if len(trades) > n {
		trades = trades[:n]
	}
	lines := make([]string, 0, len(trades))
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
	for _, tr := range trades {
		side := "SELL"
		if tr.IsBuyer {
			side = "BUY"
		}
		qv, _ := strconv.ParseFloat(strings.TrimSpace(tr.QuoteQty), 64)
		lines = append(lines, fmt.Sprintf("%s %s %s", side, tr.Symbol, formatQuoteByDisplay(qv, cfg, displayCurrency, spot)))
	}
	return strings.Join(lines, " | ")
}

func resolveDailyTimezone(ctx context.Context, cfg Config, alerts *alertManager) (*time.Location, string) {
	loc, tzName, err := services.ResolveDailyTimezone(ctx, cfg.DailyReportTimezone, worldtime.FetchTimezoneByIP)
	if err != nil {
		if alerts != nil {
			alerts.recordError("timezone.resolve", err)
			alerts.sendDedup("timezone.resolve.error", time.Hour, fmt.Sprintf("Daily timezone resolve failed; using UTC (%v)", err))
		}
		log.Printf("daily report timezone resolve error (%s): %v; fallback UTC", strings.TrimSpace(cfg.DailyReportTimezone), err)
		return time.UTC, "UTC"
	}
	return loc, tzName
}

type pnlWindowSnapshot struct {
	dayPnl  float64
	dayPct  float64
	dayOK   bool
	weekPnl float64
	weekPct float64
	weekOK  bool
	monPnl  float64
	monPct  float64
	monOK   bool
}

func resolvePnlWindowSnapshot(ctx context.Context, cfg Config, state *MonitorState) (pnlWindowSnapshot, error) {
	if !cfg.FreqtradeHistoryMode {
		dayPnl, dayPct, dayOK := state.pnlSince(24 * time.Hour)
		weekPnl, weekPct, weekOK := state.pnlSince(7 * 24 * time.Hour)
		monPnl, monPct, monOK := state.pnlSince(30 * 24 * time.Hour)
		return pnlWindowSnapshot{
			dayPnl:  dayPnl,
			dayPct:  dayPct,
			dayOK:   dayOK,
			weekPnl: weekPnl,
			weekPct: weekPct,
			weekOK:  weekOK,
			monPnl:  monPnl,
			monPct:  monPct,
			monOK:   monOK,
		}, nil
	}

	trades, err := getFreqtradeTrades30dCached(ctx, cfg)
	if err != nil {
		return pnlWindowSnapshot{}, err
	}
	dayPnl, dayPct, dayOK := freqtradePnlSince(trades, time.Now().UTC().Add(-24*time.Hour))
	weekPnl, weekPct, weekOK := freqtradePnlSince(trades, time.Now().UTC().Add(-7*24*time.Hour))
	monPnl, monPct, monOK := freqtradePnlSince(trades, time.Now().UTC().Add(-30*24*time.Hour))
	return pnlWindowSnapshot{
		dayPnl:  dayPnl,
		dayPct:  dayPct,
		dayOK:   dayOK,
		weekPnl: weekPnl,
		weekPct: weekPct,
		weekOK:  weekOK,
		monPnl:  monPnl,
		monPct:  monPct,
		monOK:   monOK,
	}, nil
}

func buildDailyReport(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState) (string, error) {
	started := time.Now()
	defer logTiming("build_daily_report", started)
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}
	pnlSnap, err := resolvePnlWindowSnapshot(ctx, cfg, state)
	if err != nil {
		return "", err
	}

	fees, err := getFeeSummaryCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset)
	if err != nil {
		return "", err
	}

	refillD := state.refillStatsSince(24 * time.Hour)
	refillW := state.refillStatsSince(7 * 24 * time.Hour)
	refillM := state.refillStatsSince(30 * 24 * time.Hour)

	pnlLine := func(label string, ok bool, pnl, pct float64) string {
		if !ok {
			return fmt.Sprintf("%s: n/a", label)
		}
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
		return fmt.Sprintf("%s: %s (%.2f%%)", label, formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct)
	}
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
	dayFeeText := formatFeeByMainCurrency(fees.Day, cfg, displayCurrency, spot)
	weekFeeText := formatFeeByMainCurrency(fees.Week, cfg, displayCurrency, spot)
	monthFeeText := formatFeeByMainCurrency(fees.Month, cfg, displayCurrency, spot)
	quoteFreeText := formatQuoteByDisplay(quoteFree, cfg, displayCurrency, spot)
	portfolioText := formatQuoteByDisplay(portfolioQuote, cfg, displayCurrency, spot)
	refillDText := formatQuoteByDisplay(refillD.QuoteSpent, cfg, displayCurrency, spot)
	refillWText := formatQuoteByDisplay(refillW.QuoteSpent, cfg, displayCurrency, spot)
	refillMText := formatQuoteByDisplay(refillM.QuoteSpent, cfg, displayCurrency, spot)

	return fmt.Sprintf(
		"Daily Trading Report (%s UTC)\n\nAccount\nBNB: %s\n%s: %s\n%s: %.4f\nPortfolio: %s\n\nFees (%s)\nDay: %s\nWeek: %s\nMonth: %s\n\nRefills\nDay: %d orders, spent %s, got %.6f %s\nWeek: %d orders, spent %s, got %.6f %s\nMonth: %d orders, spent %s, got %.6f %s\n\nPnL\n%s\n%s\n%s",
		time.Now().UTC().Format("2006-01-02 15:04:05"),
		formatBNBWithQuote(bnbFree, price, cfg),
		cfg.QuoteAsset,
		quoteFreeText,
		cfg.Symbol,
		price,
		portfolioText,
		cfg.BNBAsset,
		dayFeeText,
		weekFeeText,
		monthFeeText,
		refillD.Count,
		refillDText,
		refillD.BNBReceived,
		cfg.BNBAsset,
		refillW.Count,
		refillWText,
		refillW.BNBReceived,
		cfg.BNBAsset,
		refillM.Count,
		refillMText,
		refillM.BNBReceived,
		cfg.BNBAsset,
		pnlLine("Day", pnlSnap.dayOK, pnlSnap.dayPnl, pnlSnap.dayPct),
		pnlLine("Week", pnlSnap.weekOK, pnlSnap.weekPnl, pnlSnap.weekPct),
		pnlLine("Month", pnlSnap.monOK, pnlSnap.monPnl, pnlSnap.monPct),
	), nil
}

func buildPeriodReport(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState, d time.Duration, label string) (string, error) {
	started := time.Now()
	defer logTiming("build_period_report_"+label, started)
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}

	fees, err := totalFeesBNBCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, d)
	if err != nil {
		return "", err
	}
	pnl, pct, ok := state.pnlSince(d)
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return "", err
		}
		pnl, pct, ok = freqtradePnlSince(trades, time.Now().UTC().Add(-d))
	}
	refills := state.refillStatsSince(d)

	pnlText := "n/a"
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, d)
	if ok {
		pnlText = fmt.Sprintf("%s (%.2f%%)", formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct)
	}
	feeText := formatFeeByMainCurrency(fees, cfg, displayCurrency, spot)
	quoteFreeText := formatQuoteByDisplay(quoteFree, cfg, displayCurrency, spot)
	portfolioText := formatQuoteByDisplay(portfolioQuote, cfg, displayCurrency, spot)
	refillText := formatQuoteByDisplay(refills.QuoteSpent, cfg, displayCurrency, spot)

	return fmt.Sprintf(
		"%s Report (%s UTC)\n\nAccount\nBNB: %s\n%s: %s\n%s: %.4f\nPortfolio: %s\n\nWindow Stats\nFees: %s\nRefills: %d orders\nRefill spent: %s\nRefill got: %.6f %s\nPnL: %s",
		strings.Title(label),
		time.Now().UTC().Format("2006-01-02 15:04:05"),
		formatBNBWithQuote(bnbFree, price, cfg),
		cfg.QuoteAsset,
		quoteFreeText,
		cfg.Symbol,
		price,
		portfolioText,
		feeText,
		refills.Count,
		refillText,
		refills.BNBReceived,
		cfg.BNBAsset,
		pnlText,
	), nil
}

func nextDailyRun(now time.Time, hour, minute int, loc *time.Location) time.Time {
	return services.NextDailyRun(now, hour, minute, loc)
}
