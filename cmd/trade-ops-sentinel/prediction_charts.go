package main

import (
	"context"
	"math"
	"sort"
	"time"
)

func predictPnLSeries(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, horizonDays int) ([]string, []float64, []float64, string) {
	model, ok := predictModel(ctx, cfg, state, horizonDays)
	if !ok {
		return nil, nil, nil, cfg.QuoteAsset
	}
	return predictionChartSeries(ctx, cfg, state, binance, model, false)
}

func predictPnLCumulativeSeries(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, horizonDays int) ([]string, []float64, []float64, string) {
	model, ok := predictModel(ctx, cfg, state, horizonDays)
	if !ok {
		return nil, nil, nil, cfg.QuoteAsset
	}
	return predictionChartSeries(ctx, cfg, state, binance, model, true)
}

func forecastCumulativePnL(ctx context.Context, cfg Config, state *MonitorState, horizonDays int) (float64, bool) {
	model, ok := predictModel(ctx, cfg, state, horizonDays)
	if !ok {
		return 0, false
	}
	total := 0.0
	for _, v := range model.predRaw {
		total += v
	}
	return total, true
}

type predictionModel struct {
	horizonDays int
	lookback    int
	timestamps  []time.Time
	rawValues   []float64
	predRaw     []float64
}

func predictModel(ctx context.Context, cfg Config, state *MonitorState, horizonDays int) (predictionModel, bool) {
	if horizonDays <= 0 {
		horizonDays = 7
	}
	if horizonDays > maxPredictionDays {
		horizonDays = maxPredictionDays
	}
	lookbackDays := horizonDays * 4
	if lookbackDays < 30 {
		lookbackDays = 30
	}
	if lookbackDays > 365 {
		lookbackDays = 365
	}

	series := predictionDailySeries(ctx, cfg, state, lookbackDays)
	if len(series) < 14 {
		return predictionModel{}, false
	}

	timestamps := make([]time.Time, 0, len(series))
	rawValues := make([]float64, 0, len(series))
	for _, p := range series {
		timestamps = append(timestamps, p.Day)
		rawValues = append(rawValues, p.PnLQuote)
	}

	alpha, beta := weightedLinearTrend(rawValues)
	seasonal := weeklySeasonality(timestamps, rawValues, alpha, beta)
	predRaw := make([]float64, 0, horizonDays)
	lastDay := timestamps[len(timestamps)-1]
	for i := 1; i <= horizonDays; i++ {
		x := float64(len(rawValues) - 1 + i)
		day := lastDay.AddDate(0, 0, i)
		season := seasonal[day.Weekday()]
		// Slightly damp seasonality in the forecast horizon to reduce overfit noise.
		season *= 0.7
		predRaw = append(predRaw, alpha+beta*x+season)
	}
	return predictionModel{
		horizonDays: horizonDays,
		lookback:    lookbackDays,
		timestamps:  timestamps,
		rawValues:   rawValues,
		predRaw:     predRaw,
	}, true
}

func predictionChartSeries(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, model predictionModel, cumulative bool) ([]string, []float64, []float64, string) {
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, time.Duration(model.lookback)*24*time.Hour)
	unit := cfg.QuoteAsset
	if displayCurrency == "BNB" && spot > 0 {
		unit = cfg.BNBAsset
	}

	historyVals := make([]float64, 0, len(model.rawValues))
	for _, v := range model.rawValues {
		dv, _, ok := quoteToDisplay(v, cfg, displayCurrency, spot)
		if !ok {
			dv = v
		}
		historyVals = append(historyVals, dv)
	}
	predVals := make([]float64, 0, len(model.predRaw))
	for _, v := range model.predRaw {
		dv, _, ok := quoteToDisplay(v, cfg, displayCurrency, spot)
		if !ok {
			dv = v
		}
		predVals = append(predVals, dv)
	}
	if cumulative {
		for i := 1; i < len(historyVals); i++ {
			historyVals[i] += historyVals[i-1]
		}
		base := historyVals[len(historyVals)-1]
		for i := 0; i < len(predVals); i++ {
			base += predVals[i]
			predVals[i] = base
		}
	}

	labels := make([]string, 0, len(model.timestamps)+len(predVals))
	for _, d := range model.timestamps {
		labels = append(labels, d.Format("01-02"))
	}
	lastDay := model.timestamps[len(model.timestamps)-1]
	for i := 1; i <= model.horizonDays; i++ {
		labels = append(labels, lastDay.AddDate(0, 0, i).Format("01-02"))
	}

	historySeries := make([]float64, 0, len(labels))
	for _, v := range historyVals {
		historySeries = append(historySeries, v)
	}
	for i := 0; i < len(predVals); i++ {
		historySeries = append(historySeries, math.NaN())
	}

	predSeries := make([]float64, 0, len(labels))
	for i := 0; i < len(historyVals)-1; i++ {
		predSeries = append(predSeries, math.NaN())
	}
	predSeries = append(predSeries, historyVals[len(historyVals)-1])
	predSeries = append(predSeries, predVals...)
	idx := trimForecastLeadingZerosIndex(historySeries, predSeries)
	if idx > 0 && idx < len(labels) {
		labels = labels[idx:]
		historySeries = historySeries[idx:]
		predSeries = predSeries[idx:]
	}
	return labels, historySeries, predSeries, unit
}

func trimForecastLeadingZerosIndex(history, forecast []float64) int {
	n := len(history)
	if len(forecast) < n {
		n = len(forecast)
	}
	idx := 0
	for idx < n-2 {
		if math.Abs(history[idx]) > 1e-9 || !math.IsNaN(forecast[idx]) {
			break
		}
		idx++
	}
	return idx
}

type predictionPoint struct {
	Day      time.Time
	PnLQuote float64
}

func predictionDailySeries(ctx context.Context, cfg Config, state *MonitorState, lookbackDays int) []predictionPoint {
	now := time.Now().UTC()
	start := now.Add(-time.Duration(lookbackDays-1) * 24 * time.Hour).Truncate(24 * time.Hour)
	byDay := map[string]float64{}

	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return nil
		}
		for _, tr := range trades {
			if tr.CloseTimestamp <= 0 {
				continue
			}
			t := time.UnixMilli(tr.CloseTimestamp).UTC()
			if t.Before(start) {
				continue
			}
			day := t.Format("2006-01-02")
			byDay[day] += tr.ProfitAbs
		}
	} else {
		rows := state.dailyPnlRows(lookbackDays)
		for _, r := range rows {
			byDay[r.Day] = r.PnL
		}
	}

	out := make([]predictionPoint, 0, lookbackDays)
	for i := lookbackDays - 1; i >= 0; i-- {
		d := now.Add(-time.Duration(i) * 24 * time.Hour).Truncate(24 * time.Hour)
		k := d.Format("2006-01-02")
		out = append(out, predictionPoint{
			Day:      d,
			PnLQuote: byDay[k],
		})
	}
	return out
}

func weightedLinearTrend(y []float64) (alpha, beta float64) {
	n := len(y)
	if n == 0 {
		return 0, 0
	}
	if n == 1 {
		return y[0], 0
	}
	var sw, sx, sy, sxx, sxy float64
	for i := 0; i < n; i++ {
		x := float64(i)
		age := x / float64(n-1)
		// Recency-weighted least squares.
		w := 0.35 + 0.65*age*age
		sw += w
		sx += w * x
		sy += w * y[i]
		sxx += w * x * x
		sxy += w * x * y[i]
	}
	den := sw*sxx - sx*sx
	if math.Abs(den) < 1e-9 {
		return sy / sw, 0
	}
	beta = (sw*sxy - sx*sy) / den
	alpha = (sy - beta*sx) / sw
	return alpha, beta
}

func weeklySeasonality(days []time.Time, values []float64, alpha, beta float64) map[time.Weekday]float64 {
	type agg struct {
		sum   float64
		count int
	}
	out := map[time.Weekday]float64{}
	acc := map[time.Weekday]agg{}
	for i := 0; i < len(values) && i < len(days); i++ {
		base := alpha + beta*float64(i)
		r := values[i] - base
		wd := days[i].Weekday()
		a := acc[wd]
		a.sum += r
		a.count++
		acc[wd] = a
	}
	for wd, a := range acc {
		if a.count == 0 {
			continue
		}
		out[wd] = a.sum / float64(a.count)
	}
	// Fill missing weekdays with nearest known weekday residual.
	if len(out) < 7 {
		type kv struct {
			wd time.Weekday
			v  float64
		}
		known := make([]kv, 0, len(out))
		for wd, v := range out {
			known = append(known, kv{wd: wd, v: v})
		}
		sort.Slice(known, func(i, j int) bool { return known[i].wd < known[j].wd })
		for wd := time.Sunday; wd <= time.Saturday; wd++ {
			if _, ok := out[wd]; ok || len(known) == 0 {
				continue
			}
			out[wd] = known[len(known)-1].v
			for _, k := range known {
				if k.wd >= wd {
					out[wd] = k.v
					break
				}
			}
		}
	}
	return out
}
