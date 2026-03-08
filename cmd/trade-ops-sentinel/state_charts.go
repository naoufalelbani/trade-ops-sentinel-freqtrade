package main

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
	"trade-ops-sentinel/internal/services/charts"
)

func buildLineChartURL(title string, labels []string, values []float64, unit, theme, size string, showLabels, showGrid bool) string {
	return charts.BuildLineChartURL(title, labels, values, unit, theme, size, showLabels, showGrid)
}

func buildCumulativeProfitChartURL(title string, labels []string, values []float64, unit, theme, size string, showLabels, showGrid bool) string {
	return charts.BuildCumulativeProfitChartURL(title, labels, values, unit, theme, size, showLabels, showGrid)
}

func buildForecastChartURL(title string, labels []string, history, forecast []float64, unit, theme, size string, showGrid bool) string {
	return charts.BuildForecastChartURL(title, labels, history, forecast, unit, theme, size, showGrid)
}

func cumulativeProfitSeriesWindow(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, d time.Duration) ([]string, []float64, string) {
	return cumulativeProfitSeriesWindowMode(ctx, cfg, state, binance, d, "auto")
}

func cumulativeProfitSeriesWindowMode(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, d time.Duration, mode string) ([]string, []float64, string) {
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, d)
	unit := cfg.QuoteAsset
	if strings.EqualFold(displayCurrency, "BNB") && spot > 0 {
		unit = cfg.BNBAsset
	}
	hours := int(d / time.Hour)
	if hours <= 0 {
		hours = 24
	}
	modeNorm := strings.ToLower(strings.TrimSpace(mode))
	minutesMode := modeNorm == "minutes"
	tradesMode := modeNorm == "trades"
	hoursMode := d <= 7*24*time.Hour
	if modeNorm == "hours" {
		hoursMode = true
	} else if modeNorm == "days" {
		hoursMode = false
	}
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return nil, nil, unit
		}
		var labels []string
		var series []float64
		if tradesMode {
			labels, series = freqtradePnlSeriesByTradeWindow(trades, d)
		} else if minutesMode {
			minutes := int(d / time.Minute)
			if minutes <= 0 {
				minutes = 60
			}
			labels, series = freqtradePnlSeriesByMinuteActive(trades, minutes)
		} else if hoursMode {
			labels, series = freqtradePnlSeriesByHourActive(trades, hours)
		} else {
			days := int(d / (24 * time.Hour))
			labels, series = freqtradePnlSeriesByDay(trades, days)
		}
		return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
	}

	if tradesMode {
		return nil, nil, unit
	}
	if minutesMode {
		minutes := int(d / time.Minute)
		if minutes <= 0 {
			minutes = 60
		}
		labels, series := state.pnlSeriesLastNMinutes(minutes)
		return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
	}
	if hoursMode {
		labels, series := state.pnlSeriesLastNHours(hours)
		return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
	}
	days := int(d / (24 * time.Hour))
	rows := state.dailyPnlRows(days)
	if len(rows) == 0 {
		return nil, nil, unit
	}
	activeDays := state.snapshotDaySet(days)
	labels := make([]string, 0, len(rows))
	series := make([]float64, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if !activeDays[r.Day] {
			continue
		}
		labels = append(labels, r.Day)
		series = append(series, r.PnL)
	}
	return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
}

func cumulativeProfitSeriesRangeMode(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, fromAgo, toAgo time.Duration, mode string) ([]string, []float64, string) {
	if fromAgo <= toAgo {
		return nil, nil, cfg.QuoteAsset
	}
	now := time.Now().UTC()
	start := now.Add(-fromAgo)
	end := now.Add(-toAgo)
	return cumulativeProfitSeriesBetweenMode(ctx, cfg, state, binance, start, end, mode)
}

func cumulativeProfitSeriesBetweenMode(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, start, end time.Time, mode string) ([]string, []float64, string) {
	if !start.Before(end) {
		return nil, nil, cfg.QuoteAsset
	}
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spotWindow := end.Sub(start)
	if spotWindow <= 0 {
		spotWindow = 24 * time.Hour
	}
	spot := spotForDisplay(ctx, cfg, binance, spotWindow)
	unit := cfg.QuoteAsset
	if strings.EqualFold(displayCurrency, "BNB") && spot > 0 {
		unit = cfg.BNBAsset
	}
	modeNorm := strings.ToLower(strings.TrimSpace(mode))
	minutesMode := modeNorm == "minutes"
	tradesMode := modeNorm == "trades"
	hoursMode := end.Sub(start) <= 7*24*time.Hour
	if modeNorm == "hours" {
		hoursMode = true
	} else if modeNorm == "days" {
		hoursMode = false
	}

	var labels []string
	var series []float64
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return nil, nil, unit
		}
		if tradesMode {
			labels, series = freqtradePnlSeriesByTradeRange(trades, start, end)
		} else if minutesMode {
			labels, series = freqtradePnlSeriesByMinuteRangeActive(trades, start, end)
		} else if hoursMode {
			labels, series = freqtradePnlSeriesByHourRangeActive(trades, start, end)
		} else {
			labels, series = freqtradePnlSeriesByDayRangeActive(trades, start, end)
		}
	} else {
		if tradesMode {
			return nil, nil, unit
		}
		if minutesMode {
			labels, series = state.pnlSeriesByMinuteRangeActive(start, end)
		} else if hoursMode {
			labels, series = state.pnlSeriesByHourRangeActive(start, end)
		} else {
			labels, series = state.pnlSeriesByDayRangeActive(start, end)
		}
	}
	return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
}

func cumulativeFeesSeriesWindow(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, d time.Duration) ([]string, []float64, string, error) {
	hoursMode := d <= 24*time.Hour
	var (
		labels  []string
		feesBNB []float64
		err     error
	)
	if cfg.FreqtradeHistoryMode && hoursMode {
		trades, tErr := getFreqtradeTrades30dCached(ctx, cfg)
		if tErr != nil {
			return nil, nil, "", tErr
		}
		labels, feesBNB = freqtradeFeeSeriesByHourActive(trades, cfg.BNBAsset, 24)
	} else if hoursMode {
		labels, feesBNB, err = feeSeriesLastNHours(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, 24)
	} else {
		days := int(d / (24 * time.Hour))
		labels, feesBNB, err = feeSeriesLastNDaysCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, days)
	}
	if err != nil {
		return nil, nil, "", err
	}
	if len(labels) == 0 {
		return nil, nil, "", nil
	}
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, d)
	unit := cfg.BNBAsset
	if strings.EqualFold(displayCurrency, "USDT") {
		unit = cfg.QuoteAsset
	}
	return cumulativeDisplaySeries(labels, feesBNB), cumulativeDisplayValues(feesBNB, cfg, displayCurrency, spot), unit, nil
}

func cumulativeDisplaySeries(labels []string, series []float64) []string {
	if len(labels) == 0 || len(series) == 0 {
		return nil
	}
	n := len(labels)
	if len(series) < n {
		n = len(series)
	}
	return append([]string(nil), labels[:n]...)
}

func cumulativeDisplayValues(series []float64, cfg Config, displayCurrency string, spot float64) []float64 {
	if len(series) == 0 {
		return nil
	}
	out := make([]float64, 0, len(series))
	cum := 0.0
	for _, raw := range series {
		v, _, ok := quoteToDisplay(raw, cfg, displayCurrency, spot)
		if !ok {
			v = raw
		}
		cum += v
		out = append(out, cum)
	}
	return out
}

func freqtradePnlSeriesByHourActive(trades []freqtradeTrade, hours int) ([]string, []float64) {
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	buckets := map[string]float64{}
	active := map[string]bool{}
	for _, tr := range trades {
		if tr.CloseTimestamp < start.UnixMilli() {
			continue
		}
		k := time.UnixMilli(tr.CloseTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
		active[k] = true
		buckets[k] += tr.ProfitAbs
	}
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		if !active[k] {
			continue
		}
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradePnlSeriesByHourRangeActive(trades []freqtradeTrade, start, end time.Time) ([]string, []float64) {
	startMS := start.UnixMilli()
	endMS := end.UnixMilli()
	buckets := map[time.Time]float64{}
	for _, tr := range trades {
		ts := tr.CloseTimestamp
		if ts < startMS || ts > endMS {
			continue
		}
		k := time.UnixMilli(ts).UTC().Truncate(time.Hour)
		buckets[k] += tr.ProfitAbs
	}
	if len(buckets) == 0 {
		return nil, nil
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, k.Format("01-02 15:00"))
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradePnlSeriesByMinuteActive(trades []freqtradeTrade, minutes int) ([]string, []float64) {
	now := time.Now().UTC().Truncate(time.Minute)
	start := now.Add(-time.Duration(minutes-1) * time.Minute)
	buckets := map[string]float64{}
	active := map[string]bool{}
	for _, tr := range trades {
		if tr.CloseTimestamp < start.UnixMilli() {
			continue
		}
		k := time.UnixMilli(tr.CloseTimestamp).UTC().Truncate(time.Minute).Format("01-02 15:04")
		active[k] = true
		buckets[k] += tr.ProfitAbs
	}
	labels := make([]string, 0, minutes)
	values := make([]float64, 0, minutes)
	for i := minutes - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Minute).Format("01-02 15:04")
		if !active[k] {
			continue
		}
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradePnlSeriesByMinuteRangeActive(trades []freqtradeTrade, start, end time.Time) ([]string, []float64) {
	startMS := start.UnixMilli()
	endMS := end.UnixMilli()
	buckets := map[time.Time]float64{}
	for _, tr := range trades {
		ts := tr.CloseTimestamp
		if ts < startMS || ts > endMS {
			continue
		}
		k := time.UnixMilli(ts).UTC().Truncate(time.Minute)
		buckets[k] += tr.ProfitAbs
	}
	if len(buckets) == 0 {
		return nil, nil
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, k.Format("01-02 15:04"))
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradePnlSeriesByTradeWindow(trades []freqtradeTrade, d time.Duration) ([]string, []float64) {
	start := time.Now().UTC().Add(-d)
	end := time.Now().UTC()
	return freqtradePnlSeriesByTradeRange(trades, start, end)
}

func freqtradePnlSeriesByTradeRange(trades []freqtradeTrade, start, end time.Time) ([]string, []float64) {
	type tradePoint struct {
		ts  int64
		id  int64
		pnl float64
	}
	startMS := start.UnixMilli()
	endMS := end.UnixMilli()
	points := make([]tradePoint, 0, len(trades))
	for _, tr := range trades {
		ts := tr.CloseTimestamp
		if ts <= 0 || ts < startMS || ts > endMS {
			continue
		}
		points = append(points, tradePoint{ts: ts, id: tr.TradeID, pnl: tr.ProfitAbs})
	}
	if len(points) == 0 {
		return nil, nil
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].ts == points[j].ts {
			return points[i].id < points[j].id
		}
		return points[i].ts < points[j].ts
	})
	const maxPoints = 500
	if len(points) > maxPoints {
		points = points[len(points)-maxPoints:]
	}
	labels := make([]string, 0, len(points))
	values := make([]float64, 0, len(points))
	for i, p := range points {
		labels = append(labels, "T"+strconv.Itoa(i+1))
		values = append(values, p.pnl)
	}
	return labels, values
}

func freqtradePnlSeriesByDayRangeActive(trades []freqtradeTrade, start, end time.Time) ([]string, []float64) {
	startMS := start.UnixMilli()
	endMS := end.UnixMilli()
	buckets := map[time.Time]float64{}
	for _, tr := range trades {
		ts := tr.CloseTimestamp
		if ts < startMS || ts > endMS {
			continue
		}
		k := time.UnixMilli(ts).UTC().Truncate(24 * time.Hour)
		buckets[k] += tr.ProfitAbs
	}
	if len(buckets) == 0 {
		return nil, nil
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, k.Format("2006-01-02"))
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradeFeeSeriesByHourActive(trades []freqtradeTrade, feeAsset string, hours int) ([]string, []float64) {
	asset := strings.ToUpper(strings.TrimSpace(feeAsset))
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	buckets := map[string]float64{}
	active := map[string]bool{}
	for _, tr := range trades {
		openFee, closeFee := freqtradeTradeFeeInAsset(tr, asset)
		if tr.OpenTimestamp >= start.UnixMilli() {
			k := time.UnixMilli(tr.OpenTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
			active[k] = true
			buckets[k] += openFee
		}
		if tr.CloseTimestamp >= start.UnixMilli() {
			k := time.UnixMilli(tr.CloseTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
			active[k] = true
			buckets[k] += closeFee
		}
	}
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		if !active[k] {
			continue
		}
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values
}

func feeSeriesLastNHours(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string, hours int) ([]string, []float64, error) {
	start := time.Now().UTC().Add(-time.Duration(hours-1) * time.Hour).UnixMilli()
	end := time.Now().UTC().UnixMilli()
	buckets := map[string]float64{}
	active := map[string]bool{}
	type result struct {
		byHour map[string]float64
		active map[string]bool
		err    error
	}
	ch := make(chan result, len(symbols))
	sem := make(chan struct{}, 20)
	for _, sym := range symbols {
		symbol := sym
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			trades, err := binance.GetMyTrades(ctx, symbol, start, end)
			if err != nil {
				ch <- result{err: err}
				return
			}
			local := map[string]float64{}
			localActive := map[string]bool{}
			for _, tr := range trades {
				if !strings.EqualFold(strings.TrimSpace(tr.CommissionAsset), strings.TrimSpace(bnbAsset)) {
					continue
				}
				fee, err := strconv.ParseFloat(strings.TrimSpace(tr.Commission), 64)
				if err != nil {
					continue
				}
				k := time.UnixMilli(tr.Time).UTC().Truncate(time.Hour).Format("01-02 15:00")
				localActive[k] = true
				local[k] += fee
			}
			ch <- result{byHour: local, active: localActive}
		}()
	}
	for i := 0; i < len(symbols); i++ {
		r := <-ch
		if r.err != nil {
			return nil, nil, r.err
		}
		for k, v := range r.byHour {
			buckets[k] += v
		}
		for k := range r.active {
			active[k] = true
		}
	}
	now := time.Now().UTC().Truncate(time.Hour)
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		if !active[k] {
			continue
		}
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values, nil
}
