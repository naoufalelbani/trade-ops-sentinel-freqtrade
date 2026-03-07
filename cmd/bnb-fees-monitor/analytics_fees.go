package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func totalFeesBNB(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string, d time.Duration) (float64, error) {
	started := time.Now()
	defer logTiming("fees_total_calc", started)
	if appCfg.FreqtradeHistoryMode {
		since := time.Now().UTC().Add(-d)
		trades, err := fetchFreqtradeTradesSince(ctx, appCfg, since)
		if err != nil {
			return 0, err
		}
		return freqtradeFeesSince(trades, since, bnbAsset), nil
	}
	if tradeStore != nil {
		err := tradeStore.SyncSymbolsForce(ctx, binance, symbols)
		if err != nil {
			logIfErr("sqlite.sync_symbols_fees_total", err)
		}
		return tradeStore.SumFeesSince(symbols, bnbAsset, time.Now().UTC().Add(-d).UnixMilli())
	}
	start := time.Now().UTC().Add(-d).UnixMilli()
	end := time.Now().UTC().UnixMilli()

	type result struct {
		fee float64
		err error
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
				ch <- result{err: fmt.Errorf("symbol %s trades: %w", symbol, err)}
				return
			}
			fee := 0.0
			for _, tr := range trades {
				if !strings.EqualFold(strings.TrimSpace(tr.CommissionAsset), strings.TrimSpace(bnbAsset)) {
					continue
				}
				v, err := strconv.ParseFloat(tr.Commission, 64)
				if err == nil {
					fee += v
				}
			}
			ch <- result{fee: fee}
		}()
	}

	total := 0.0
	for i := 0; i < len(symbols); i++ {
		r := <-ch
		if r.err != nil {
			return 0, r.err
		}
		total += r.fee
	}
	return total, nil
}

func totalFeesBNBCached(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string, d time.Duration) (float64, error) {
	key := fmt.Sprintf("fees:total:%s:%d:%s", bnbAsset, int64(d.Seconds()), symbolsCacheKey(symbols))
	var cached float64
	if ok, err := binance.cache.getJSON(ctx, key, &cached); err == nil && ok {
		return cached, nil
	}
	v, err := totalFeesBNB(ctx, binance, symbols, bnbAsset, d)
	if err != nil {
		return 0, err
	}
	_ = binance.cache.setJSON(ctx, key, v, 5*time.Minute)
	return v, nil
}

func feeSeriesLastNDays(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string, days int) ([]string, []float64, error) {
	started := time.Now()
	defer logTiming("fees_series_calc", started)
	if appCfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, appCfg)
		if err != nil {
			return nil, nil, err
		}
		labels, values := freqtradeFeeSeriesByDay(trades, bnbAsset, days)
		return labels, values, nil
	}
	if tradeStore != nil {
		err := tradeStore.SyncSymbolsForce(ctx, binance, symbols)
		if err != nil {
			logIfErr("sqlite.sync_symbols_fees_series", err)
		}
		return tradeStore.FeeSeriesLastNDays(symbols, bnbAsset, days)
	}
	start := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	startMS := start.UnixMilli()
	endMS := time.Now().UTC().UnixMilli()

	buckets := map[string]float64{}
	dayHasData := map[string]bool{}
	type result struct {
		byDay map[string]float64
		err   error
	}
	ch := make(chan result, len(symbols))
	sem := make(chan struct{}, 20)
	for _, sym := range symbols {
		symbol := sym
		go func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			trades, err := binance.GetMyTrades(ctx, symbol, startMS, endMS)
			if err != nil {
				ch <- result{err: fmt.Errorf("symbol %s trades: %w", symbol, err)}
				return
			}
			local := map[string]float64{}
			for _, tr := range trades {
				if !strings.EqualFold(strings.TrimSpace(tr.CommissionAsset), strings.TrimSpace(bnbAsset)) {
					continue
				}
				fee, err := strconv.ParseFloat(tr.Commission, 64)
				if err != nil {
					continue
				}
				day := time.UnixMilli(tr.Time).UTC().Format("2006-01-02")
				dayHasData[day] = true
				local[day] += fee
			}
			ch <- result{byDay: local}
		}()
	}
	for i := 0; i < len(symbols); i++ {
		r := <-ch
		if r.err != nil {
			return nil, nil, r.err
		}
		for day, fee := range r.byDay {
			buckets[day] += fee
		}
	}

	labels := make([]string, 0, days)
	values := make([]float64, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := time.Now().UTC().Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		if !dayHasData[day] {
			continue
		}
		labels = append(labels, day)
		values = append(values, buckets[day])
	}
	return labels, values, nil
}

func feeSeriesLastNDaysCached(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string, days int) ([]string, []float64, error) {
	type payload struct {
		Labels []string  `json:"labels"`
		Values []float64 `json:"values"`
	}
	key := fmt.Sprintf("fees:series:%s:%d:%s", bnbAsset, days, symbolsCacheKey(symbols))
	var cached payload
	if ok, err := binance.cache.getJSON(ctx, key, &cached); err == nil && ok {
		return cached.Labels, cached.Values, nil
	}
	labels, values, err := feeSeriesLastNDays(ctx, binance, symbols, bnbAsset, days)
	if err != nil {
		return nil, nil, err
	}
	_ = binance.cache.setJSON(ctx, key, payload{Labels: labels, Values: values}, 5*time.Minute)
	return labels, values, nil
}

func getFeeSummaryCached(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string) (feeSummary, error) {
	key := fmt.Sprintf("fees:summary:%s:%s", bnbAsset, symbolsCacheKey(symbols))
	var cached feeSummary
	if ok, err := binance.cache.getJSON(ctx, key, &cached); err == nil && ok {
		return cached, nil
	}

	type result struct {
		name string
		val  float64
		err  error
	}
	ch := make(chan result, 3)
	go func() {
		v, err := totalFeesBNBCached(ctx, binance, symbols, bnbAsset, 24*time.Hour)
		ch <- result{name: "d", val: v, err: err}
	}()
	go func() {
		v, err := totalFeesBNBCached(ctx, binance, symbols, bnbAsset, 7*24*time.Hour)
		ch <- result{name: "w", val: v, err: err}
	}()
	go func() {
		v, err := totalFeesBNBCached(ctx, binance, symbols, bnbAsset, 30*24*time.Hour)
		ch <- result{name: "m", val: v, err: err}
	}()

	out := feeSummary{}
	for i := 0; i < 3; i++ {
		r := <-ch
		if r.err != nil {
			return feeSummary{}, r.err
		}
		switch r.name {
		case "d":
			out.Day = r.val
		case "w":
			out.Week = r.val
		case "m":
			out.Month = r.val
		}
	}
	_ = binance.cache.setJSON(ctx, key, out, 5*time.Minute)
	return out, nil
}

func getFeeSummaryCacheOnly(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string) (feeSummary, bool, error) {
	key := fmt.Sprintf("fees:summary:%s:%s", bnbAsset, symbolsCacheKey(symbols))
	var cached feeSummary
	ok, err := binance.cache.getJSON(ctx, key, &cached)
	return cached, ok, err
}

func warmFeeSummaryCacheAsync(binance *BinanceClient, symbols []string, bnbAsset string) {
	go func() {
		if _, err := getFeeSummaryCached(context.Background(), binance, symbols, bnbAsset); err != nil {
			logIfErr("warm_fee_summary_cache", err)
		}
	}()
}

func collectTradesByDuration(ctx context.Context, binance *BinanceClient, symbols []string, d time.Duration) ([]myTrade, error) {
	started := time.Now()
	defer logTiming("collect_trades_duration", started)
	if appCfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, appCfg)
		if err != nil {
			return nil, err
		}
		return freqtradeTradesByDuration(trades, time.Now().UTC().Add(-d)), nil
	}
	if tradeStore != nil {
		var err error
		if appCfg.FreqtradeHistoryMode {
			err = tradeStore.SyncFromFreqtrade(ctx, appCfg)
		} else {
			err = tradeStore.SyncSymbols(ctx, binance, symbols)
		}
		if err != nil {
			logIfErr("sqlite.sync_symbols_collect_trades", err)
		}
		return tradeStore.ListTradesSince(symbols, time.Now().UTC().Add(-d).UnixMilli())
	}

	start := time.Now().UTC().Add(-d).UnixMilli()
	end := time.Now().UTC().UnixMilli()

	type result struct {
		trades []myTrade
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
				ch <- result{err: fmt.Errorf("symbol %s trades: %w", symbol, err)}
				return
			}
			ch <- result{trades: trades}
		}()
	}

	all := make([]myTrade, 0, 256)
	for i := 0; i < len(symbols); i++ {
		r := <-ch
		if r.err != nil {
			return nil, r.err
		}
		all = append(all, r.trades...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Time > all[j].Time })
	return all, nil
}

func freqtradeFeesSince(trades []freqtradeTrade, since time.Time, feeAsset string) float64 {
	asset := strings.ToUpper(strings.TrimSpace(feeAsset))
	sinceMS := since.UnixMilli()
	total := 0.0
	for _, tr := range trades {
		openBNB, closeBNB := freqtradeTradeFeeInAsset(tr, asset)
		if tr.OpenTimestamp >= sinceMS {
			total += openBNB
		}
		if tr.CloseTimestamp >= sinceMS {
			total += closeBNB
		}
	}
	return total
}

func freqtradeFeeSeriesByDay(trades []freqtradeTrade, feeAsset string, days int) ([]string, []float64) {
	asset := strings.ToUpper(strings.TrimSpace(feeAsset))
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days) * 24 * time.Hour)
	buckets := map[string]float64{}
	dayHasData := map[string]bool{}
	for _, tr := range trades {
		openFee, closeFee := freqtradeTradeFeeInAsset(tr, asset)
		if tr.OpenTimestamp >= start.UnixMilli() {
			day := time.UnixMilli(tr.OpenTimestamp).UTC().Format("2006-01-02")
			dayHasData[day] = true
			buckets[day] += openFee
		}
		if tr.CloseTimestamp >= start.UnixMilli() {
			day := time.UnixMilli(tr.CloseTimestamp).UTC().Format("2006-01-02")
			dayHasData[day] = true
			buckets[day] += closeFee
		}
	}
	labels := make([]string, 0, days)
	values := make([]float64, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := now.Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		if !dayHasData[day] {
			continue
		}
		labels = append(labels, day)
		values = append(values, buckets[day])
	}
	return labels, values
}

func freqtradeFeeSeriesByHour(trades []freqtradeTrade, feeAsset string, hours int) ([]string, []float64) {
	asset := strings.ToUpper(strings.TrimSpace(feeAsset))
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	buckets := map[string]float64{}
	for _, tr := range trades {
		openFee, closeFee := freqtradeTradeFeeInAsset(tr, asset)
		if tr.OpenTimestamp >= start.UnixMilli() {
			k := time.UnixMilli(tr.OpenTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
			buckets[k] += openFee
		}
		if tr.CloseTimestamp >= start.UnixMilli() {
			k := time.UnixMilli(tr.CloseTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
			buckets[k] += closeFee
		}
	}
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradeTradesByDuration(trades []freqtradeTrade, since time.Time) []myTrade {
	sinceMS := since.UnixMilli()
	out := make([]myTrade, 0, len(trades)*2)
	for _, tr := range trades {
		symbol := normalizePairToSymbol(tr.Pair)
		if symbol == "" {
			continue
		}
		if tr.OpenTimestamp >= sinceMS && tr.OpenTimestamp > 0 {
			out = append(out, myTrade{
				ID:              tr.TradeID*10 + 1,
				OrderID:         tr.TradeID*10 + 1,
				Price:           formatFloat(tr.OpenRate, 8),
				Qty:             formatFloat(tr.Amount, 8),
				QuoteQty:        formatFloat(tr.StakeAmount, 8),
				IsBuyer:         true,
				Commission:      formatFloat(freqtradeCommissionOpen(tr), 8),
				CommissionAsset: strings.ToUpper(strings.TrimSpace(tr.FeeOpenCurrency)),
				Time:            tr.OpenTimestamp,
				Symbol:          symbol,
			})
		}
		if tr.CloseTimestamp >= sinceMS && tr.CloseTimestamp > 0 {
			out = append(out, myTrade{
				ID:              tr.TradeID*10 + 2,
				OrderID:         tr.TradeID*10 + 2,
				Price:           formatFloat(tr.CloseRate, 8),
				Qty:             formatFloat(tr.Amount, 8),
				QuoteQty:        formatFloat(tr.Amount*tr.CloseRate, 8),
				IsBuyer:         false,
				Commission:      formatFloat(freqtradeCommissionClose(tr), 8),
				CommissionAsset: strings.ToUpper(strings.TrimSpace(tr.FeeCloseCurrency)),
				Time:            tr.CloseTimestamp,
				Symbol:          symbol,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time > out[j].Time })
	return out
}

func freqtradeRealizedPnlSince(trades []freqtradeTrade, since time.Time) float64 {
	sinceMS := since.UnixMilli()
	total := 0.0
	for _, tr := range trades {
		if tr.CloseTimestamp >= sinceMS {
			total += tr.ProfitAbs
		}
	}
	return total
}

func freqtradePnlSeriesByDay(trades []freqtradeTrade, days int) ([]string, []float64) {
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days) * 24 * time.Hour)
	buckets := map[string]float64{}
	dayHasData := map[string]bool{}
	for _, tr := range trades {
		if tr.CloseTimestamp >= start.UnixMilli() {
			day := time.UnixMilli(tr.CloseTimestamp).UTC().Format("2006-01-02")
			dayHasData[day] = true
			buckets[day] += tr.ProfitAbs
		}
	}
	labels := make([]string, 0, days)
	values := make([]float64, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := now.Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		if !dayHasData[day] {
			continue
		}
		labels = append(labels, day)
		values = append(values, buckets[day])
	}
	return labels, values
}

func freqtradePnlSeriesByHour(trades []freqtradeTrade, hours int) ([]string, []float64) {
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	buckets := map[string]float64{}
	for _, tr := range trades {
		if tr.CloseTimestamp >= start.UnixMilli() {
			k := time.UnixMilli(tr.CloseTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
			buckets[k] += tr.ProfitAbs
		}
	}
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradePnlSince(trades []freqtradeTrade, since time.Time) (float64, float64, bool) {
	sinceMS := since.UnixMilli()
	pnl := 0.0
	stake := 0.0
	for _, tr := range trades {
		if tr.CloseTimestamp >= sinceMS {
			pnl += tr.ProfitAbs
			stake += tr.StakeAmount
		}
	}
	if stake <= 0 {
		return pnl, 0, false
	}
	return pnl, (pnl / stake) * 100, true
}

