package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type compoundForecast struct {
	HorizonDays       int
	MaxOpenTrades     int
	OpenTrades        int
	TradableBalance   float64
	WalletBalance     float64
	PerTradeStake     float64
	TradesPerDay      float64
	LogReturnMean     float64
	LogReturnStd      float64
	PredictedTradePct float64
	PossibleTradePnL  float64
	ExpectedPct       float64
	ExpectedPnL       float64
	P20PnL            float64
	P80PnL            float64
	ExpectedFinal     float64
	HistoryLabels     []string
	HistorySeries     []float64
	ForecastSeries    []float64
	DisplayUnit       string
}

func predictCompoundSeries(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, horizonDays int) (compoundForecast, bool) {
	if !cfg.FreqtradeHistoryMode {
		return compoundForecast{}, false
	}
	if horizonDays < 3 {
		horizonDays = 7
	}
	if horizonDays > maxPredictionDays {
		horizonDays = maxPredictionDays
	}

	inputs, err := readFreqtradeCompoundInputs(ctx, cfg)
	if err != nil {
		return compoundForecast{}, false
	}
	if inputs.TradableBalance <= 0 {
		return compoundForecast{}, false
	}

	trades, err := fetchFreqtradeTradesSince(ctx, cfg, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		return compoundForecast{}, false
	}
	mu, sigma, holdHours, tradesPerDay, ok := compoundTradeDistribution(trades)
	if !ok {
		return compoundForecast{}, false
	}

	slots := inputs.MaxOpenTrades - inputs.OpenTrades
	if slots <= 0 {
		slots = 1
	}
	capPerDay := math.Max(0.2, float64(slots))
	if holdHours > 0 {
		capPerDay = math.Max(0.2, float64(slots)*(24.0/holdHours))
	}
	// Slightly adaptive rate: primarily data-driven, capped by available slot throughput.
	effectiveTradesPerDay := math.Min(capPerDay, math.Max(0.2, tradesPerDay*1.05))

	spot := spotForDisplay(ctx, cfg, binance, time.Duration(horizonDays)*24*time.Hour)
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	displayUnit := cfg.QuoteAsset
	if strings.EqualFold(displayCurrency, "BNB") && spot > 0 {
		displayUnit = cfg.BNBAsset
	}
	baseDisplay, _, okBase := quoteToDisplay(inputs.TradableBalance, cfg, displayCurrency, spot)
	if !okBase {
		baseDisplay = inputs.TradableBalance
	}

	historyLabels, historySeries := compoundHistorySeries(ctx, cfg, state, binance, 45)
	if len(historySeries) == 0 {
		return compoundForecast{}, false
	}
	base := historySeries[len(historySeries)-1]
	forecastSeries := make([]float64, 0, horizonDays+1)
	forecastSeries = append(forecastSeries, base)
	expect := 0.0
	for day := 1; day <= horizonDays; day++ {
		n := effectiveTradesPerDay * float64(day)
		expectedM := math.Exp(n*mu + 0.5*n*sigma*sigma)
		expect = baseDisplay * (expectedM - 1)
		forecastSeries = append(forecastSeries, base+expect)
	}
	labels := append([]string(nil), historyLabels...)
	last := parseChartDateLabel(labels[len(labels)-1])
	for i := 1; i <= horizonDays; i++ {
		labels = append(labels, last.AddDate(0, 0, i).Format("01-02"))
	}
	forecastPlot := make([]float64, 0, len(labels))
	for i := 0; i < len(historySeries)-1; i++ {
		forecastPlot = append(forecastPlot, math.NaN())
	}
	forecastPlot = append(forecastPlot, forecastSeries...)
	historyPlot := append([]float64(nil), historySeries...)
	for len(historyPlot) < len(labels) {
		historyPlot = append(historyPlot, math.NaN())
	}
	idx := trimForecastLeadingZerosIndex(historyPlot, forecastPlot)
	if idx > 0 && idx < len(labels) {
		labels = labels[idx:]
		historyPlot = historyPlot[idx:]
		forecastPlot = forecastPlot[idx:]
	}

	expectedFinal := inputs.TradableBalance + (inputs.TradableBalance * (math.Exp(effectiveTradesPerDay*float64(horizonDays)*mu+0.5*effectiveTradesPerDay*float64(horizonDays)*sigma*sigma) - 1))
	perTradeStake := 0.0
	if inputs.MaxOpenTrades > 0 {
		perTradeStake = inputs.WalletBalance / float64(inputs.MaxOpenTrades)
	}
	predTradePct := (math.Exp(mu+0.5*sigma*sigma) - 1) * 100
	possibleTradePnL := perTradeStake * (predTradePct / 100.0)
	expectedPnL := inputs.TradableBalance * (math.Exp(effectiveTradesPerDay*float64(horizonDays)*mu+0.5*effectiveTradesPerDay*float64(horizonDays)*sigma*sigma) - 1)
	expectedPct := 0.0
	if inputs.TradableBalance > 0 {
		expectedPct = (expectedPnL / inputs.TradableBalance) * 100
	}
	return compoundForecast{
		HorizonDays:     horizonDays,
		MaxOpenTrades:   inputs.MaxOpenTrades,
		OpenTrades:      inputs.OpenTrades,
		TradableBalance: inputs.TradableBalance,
		WalletBalance:   inputs.WalletBalance,
		PerTradeStake:   perTradeStake,
		TradesPerDay:    effectiveTradesPerDay,
		LogReturnMean:   mu,
		LogReturnStd:    sigma,
		PredictedTradePct: predTradePct,
		PossibleTradePnL:  possibleTradePnL,
		ExpectedPct:       expectedPct,
		ExpectedPnL:       expectedPnL,
		P20PnL:          inputs.TradableBalance * (math.Exp(effectiveTradesPerDay*float64(horizonDays)*mu-0.841621*sigma*math.Sqrt(effectiveTradesPerDay*float64(horizonDays))) - 1),
		P80PnL:          inputs.TradableBalance * (math.Exp(effectiveTradesPerDay*float64(horizonDays)*mu+0.841621*sigma*math.Sqrt(effectiveTradesPerDay*float64(horizonDays))) - 1),
		ExpectedFinal:   expectedFinal,
		HistoryLabels:   labels,
		HistorySeries:   historyPlot,
		ForecastSeries:  forecastPlot,
		DisplayUnit:     displayUnit,
	}, true
}

func compoundHistorySeries(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, lookbackDays int) ([]string, []float64) {
	model, ok := predictModel(ctx, cfg, state, 7)
	if !ok || len(model.rawValues) == 0 {
		return nil, nil
	}
	limit := len(model.rawValues)
	if limit > lookbackDays {
		limit = lookbackDays
	}
	raw := model.rawValues[len(model.rawValues)-limit:]
	ts := model.timestamps[len(model.timestamps)-limit:]
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, time.Duration(lookbackDays)*24*time.Hour)
	labels := make([]string, 0, len(raw))
	series := make([]float64, 0, len(raw))
	cum := 0.0
	for i, v := range raw {
		dv, _, ok := quoteToDisplay(v, cfg, displayCurrency, spot)
		if !ok {
			dv = v
		}
		cum += dv
		labels = append(labels, ts[i].Format("01-02"))
		series = append(series, cum)
	}
	return labels, series
}

func parseChartDateLabel(label string) time.Time {
	now := time.Now().UTC()
	t, err := time.ParseInLocation("01-02", label, time.UTC)
	if err != nil {
		return now
	}
	return time.Date(now.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func forecastCompoundEarnings(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, horizonDays int) (compoundForecast, bool) {
	fc, ok := predictCompoundSeries(ctx, cfg, state, binance, horizonDays)
	if !ok {
		return compoundForecast{}, false
	}
	return fc, true
}

type freqtradeCompoundInputs struct {
	MaxOpenTrades   int
	OpenTrades      int
	TradableBalance float64
	WalletBalance   float64
}

func readFreqtradeCompoundInputs(ctx context.Context, cfg Config) (freqtradeCompoundInputs, error) {
	if !cfg.FreqtradeHistoryMode {
		return freqtradeCompoundInputs{}, errors.New("compound mode requires freqtrade")
	}
	showCfg, err := fetchFreqtradeJSON(ctx, cfg, "/api/v1/show_config")
	if err != nil {
		return freqtradeCompoundInputs{}, err
	}
	balanceDoc, err := fetchFreqtradeJSON(ctx, cfg, "/api/v1/balance")
	if err != nil {
		return freqtradeCompoundInputs{}, err
	}
	countDoc, err := fetchFreqtradeJSON(ctx, cfg, "/api/v1/count")
	if err != nil {
		return freqtradeCompoundInputs{}, err
	}

	maxOpen := int(readJSONNumber(showCfg, "max_open_trades"))
	if maxOpen <= 0 {
		maxOpen = int(readJSONNumber(countDoc, "max_open_trades"))
	}
	if maxOpen <= 0 {
		maxOpen = 1
	}
	open := int(readJSONNumber(countDoc, "current"))
	if open < 0 {
		open = 0
	}
	wallet := readQuoteBalance(balanceDoc, cfg.QuoteAsset)
	if wallet <= 0 {
		wallet = readJSONNumber(balanceDoc, "total")
	}
	if wallet <= 0 {
		return freqtradeCompoundInputs{}, errors.New("freqtrade balance not available")
	}

	ratio := readJSONNumber(showCfg, "tradable_balance_ratio")
	if ratio <= 0 || ratio > 1 {
		ratio = 1
	}
	availableCap := readJSONNumber(showCfg, "available_capital")
	tradable := wallet * ratio
	if availableCap > 0 {
		tradable = math.Min(tradable, availableCap)
	}
	if tradable <= 0 {
		tradable = wallet
	}
	return freqtradeCompoundInputs{
		MaxOpenTrades:   maxOpen,
		OpenTrades:      open,
		TradableBalance: tradable,
		WalletBalance:   wallet,
	}, nil
}

func fetchFreqtradeJSON(ctx context.Context, cfg Config, path string) (any, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.FreqtradeAPIURL), "/")
	if baseURL == "" {
		return nil, errors.New("freqtrade api url is empty")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	body, _, err := freqtradeRequestWithRetry(ctx, client, cfg, "freqtrade"+path, baseURL+path)
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func readQuoteBalance(v any, quoteAsset string) float64 {
	asset := strings.ToUpper(strings.TrimSpace(quoteAsset))
	switch x := v.(type) {
	case map[string]any:
		cur := strings.ToUpper(strings.TrimSpace(readJSONString(x, "currency", "coin", "asset")))
		if cur == asset {
			if n := readMapNumber(x, "total", "balance", "free"); n > 0 {
				return n
			}
		}
		if b, ok := x[asset]; ok {
			if m, ok := b.(map[string]any); ok {
				if n := readMapNumber(m, "total", "balance", "free"); n > 0 {
					return n
				}
			}
		}
		for _, vv := range x {
			if n := readQuoteBalance(vv, asset); n > 0 {
				return n
			}
		}
	case []any:
		for _, vv := range x {
			if n := readQuoteBalance(vv, asset); n > 0 {
				return n
			}
		}
	}
	return 0
}

func readJSONNumber(v any, key string) float64 {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			if strings.EqualFold(strings.TrimSpace(k), key) {
				if n, ok := anyToFloat(vv); ok {
					return n
				}
			}
		}
		for _, vv := range x {
			if n := readJSONNumber(vv, key); n != 0 {
				return n
			}
		}
	case []any:
		for _, vv := range x {
			if n := readJSONNumber(vv, key); n != 0 {
				return n
			}
		}
	}
	return 0
}

func readMapNumber(m map[string]any, keys ...string) float64 {
	for _, k := range keys {
		for mk, mv := range m {
			if strings.EqualFold(strings.TrimSpace(mk), strings.TrimSpace(k)) {
				if n, ok := anyToFloat(mv); ok {
					return n
				}
			}
		}
	}
	return 0
}

func readJSONString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		for mk, mv := range m {
			if !strings.EqualFold(strings.TrimSpace(mk), strings.TrimSpace(k)) {
				continue
			}
			if s, ok := mv.(string); ok {
				return s
			}
		}
	}
	return ""
}

func anyToFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case json.Number:
		n, err := x.Float64()
		return n, err == nil
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		if s == "" || s == "unlimited" {
			return 0, false
		}
		n, err := strconv.ParseFloat(s, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func compoundTradeDistribution(trades []freqtradeTrade) (float64, float64, float64, float64, bool) {
	filtered := make([]freqtradeTrade, 0, len(trades))
	now := time.Now().UTC()
	cutoff := now.Add(-90 * 24 * time.Hour).UnixMilli()
	for _, tr := range trades {
		if tr.CloseTimestamp <= cutoff || tr.CloseTimestamp <= 0 || tr.StakeAmount <= 0 {
			continue
		}
		filtered = append(filtered, tr)
	}
	if len(filtered) < 10 {
		return 0, 0, 0, 0, false
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].CloseTimestamp < filtered[j].CloseTimestamp })

	rets := make([]float64, 0, len(filtered))
	holds := make([]float64, 0, len(filtered))
	for _, tr := range filtered {
		r := tr.ProfitAbs / tr.StakeAmount
		if math.IsNaN(r) || math.IsInf(r, 0) {
			continue
		}
		rets = append(rets, r)
		if tr.OpenTimestamp > 0 && tr.CloseTimestamp > tr.OpenTimestamp {
			holds = append(holds, float64(tr.CloseTimestamp-tr.OpenTimestamp)/(1000.0*3600.0))
		}
	}
	if len(rets) < 10 {
		return 0, 0, 0, 0, false
	}
	lo, hi := percentileBounds(rets, 0.05, 0.95)
	logReturns := make([]float64, 0, len(rets))
	for _, r := range rets {
		if r < lo {
			r = lo
		}
		if r > hi {
			r = hi
		}
		if r <= -0.95 {
			continue
		}
		logReturns = append(logReturns, math.Log1p(r))
	}
	if len(logReturns) < 8 {
		return 0, 0, 0, 0, false
	}
	mu, sigma := meanStd(logReturns)
	if sigma < 1e-4 {
		sigma = 1e-4
	}
	avgHold := 24.0
	if len(holds) > 0 {
		sum := 0.0
		for _, h := range holds {
			sum += h
		}
		avgHold = sum / float64(len(holds))
		if avgHold <= 0 {
			avgHold = 24
		}
	}
	spanDays := float64(filtered[len(filtered)-1].CloseTimestamp-filtered[0].CloseTimestamp) / (1000.0 * 3600.0 * 24.0)
	if spanDays < 1 {
		spanDays = 1
	}
	tradesPerDay := float64(len(filtered)) / spanDays
	return mu, sigma, avgHold, tradesPerDay, true
}

func percentileBounds(values []float64, low, high float64) (float64, float64) {
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	if len(cp) == 0 {
		return 0, 0
	}
	loIdx := int(math.Round(low * float64(len(cp)-1)))
	hiIdx := int(math.Round(high * float64(len(cp)-1)))
	if loIdx < 0 {
		loIdx = 0
	}
	if hiIdx < loIdx {
		hiIdx = loIdx
	}
	if hiIdx >= len(cp) {
		hiIdx = len(cp) - 1
	}
	return cp[loIdx], cp[hiIdx]
}

func meanStd(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	if len(values) == 1 {
		return mean, 0
	}
	variance := 0.0
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(values) - 1)
	return mean, math.Sqrt(math.Max(variance, 0))
}

func sendCompoundPredictionChart(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, notifier *TelegramNotifier, chatID int64, horizonDays int, refreshAction, chartTheme, chartSize string, chartGrid bool) error {
	fc, ok := predictCompoundSeries(ctx, cfg, state, binance, horizonDays)
	if !ok || len(fc.HistoryLabels) == 0 {
		safeSendToChat(notifier, chatID, "Not enough Freqtrade data for compound prediction yet.", chartsKeyboard())
		return nil
	}
	title := fmt.Sprintf("Compound PnL Forecast (next %dd)", horizonDays)
	chartURL := buildForecastChartURL(title, fc.HistoryLabels, fc.HistorySeries, fc.ForecastSeries, fc.DisplayUnit, chartTheme, chartSize, chartGrid)
	caption := fmt.Sprintf(
		"%s\nmax trades=%d (open=%d), tradable=%s %s\nper trade=%s %s, predicted trade=%s%%, possible trade pnl=%s %s\nE[%dd]=%s %s (%s%%), p20/p80=[%s, %s] %s",
		title,
		fc.MaxOpenTrades,
		fc.OpenTrades,
		formatSignedNoPlus(fc.TradableBalance, 3),
		strings.ToUpper(cfg.QuoteAsset),
		formatSignedNoPlus(fc.PerTradeStake, 3),
		strings.ToUpper(cfg.QuoteAsset),
		formatSignedNoPlus(fc.PredictedTradePct, 2),
		formatSignedNoPlus(fc.PossibleTradePnL, 3),
		strings.ToUpper(cfg.QuoteAsset),
		fc.HorizonDays,
		formatSignedNoPlus(fc.ExpectedPnL, 3),
		strings.ToUpper(cfg.QuoteAsset),
		formatSignedNoPlus(fc.ExpectedPct, 2),
		formatSignedNoPlus(fc.P20PnL, 3),
		formatSignedNoPlus(fc.P80PnL, 3),
		strings.ToUpper(cfg.QuoteAsset),
	)
	safeSendPhotoToChatWithMarkup(notifier, chatID, chartURL, caption, chartRefreshKeyboard(refreshAction))
	return nil
}
