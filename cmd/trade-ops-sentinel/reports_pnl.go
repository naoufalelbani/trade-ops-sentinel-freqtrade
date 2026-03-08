package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func pnlSignalEmoji(v float64) string {
	if v > 0 {
		return "🟢"
	}
	if v < 0 {
		return "🔴"
	}
	return "⚪"
}

func buildDailyPnlTable(ctx context.Context, cfg Config, state *MonitorState, days int) (string, error) {
	rows := state.dailyPnlRows(days)
	counts := map[string]int{}
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := 0.0
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return "", err
		}
		rows, counts = freqtradeDailyPnlRows(trades, days)
		spot = estimateFreqtradeFeeAssetPrice(trades, cfg.BNBAsset)
	} else if tradeStore != nil {
		c, err := tradeStore.DailyTradeCounts(cfg.TrackedSymbols, days)
		if err != nil {
			logIfErr("sqlite.daily_trade_counts", err)
		} else {
			counts = c
		}
	}

	var b strings.Builder
	showPnLEmojis := state.getPnLEmojisEnabled(true)
	b.WriteString(fmt.Sprintf("Daily Profit over the last %d days:\n", days))
	b.WriteString(fmt.Sprintf("%-16s %-18s %-9s\n", "Day (count)", "PnL", "Profit %"))
	b.WriteString(fmt.Sprintf("%-16s %-18s %-9s\n", "------------", "------------------", "--------"))

	for _, r := range rows {
		count := counts[r.Day]
		dayLabel := fmt.Sprintf("%s (%d)", r.Day, count)
		pnlVal, unit, ok := quoteToDisplay(r.PnL, cfg, displayCurrency, spot)
		if !ok {
			pnlVal = r.PnL
			unit = strings.ToUpper(cfg.QuoteAsset)
		}
		quoteCell := fmt.Sprintf("%s %s", formatSignedNoPlus(pnlVal, 3), unit)
		if showPnLEmojis {
			quoteCell = fmt.Sprintf("%s %s", quoteCell, pnlSignalEmoji(r.PnL))
		}
		pctCell := fmt.Sprintf("%s%%", formatSignedNoPlus(r.Pct, 2))
		b.WriteString(fmt.Sprintf("%-16s %-18s %-9s\n", dayLabel, quoteCell, pctCell))
	}
	predPrinted := false
	if pred7, ok := forecastCumulativePnL(ctx, cfg, state, 7); ok {
		p7, u7, ok7 := quoteToDisplay(pred7, cfg, displayCurrency, spot)
		if !ok7 {
			p7 = pred7
			u7 = strings.ToUpper(cfg.QuoteAsset)
		}
		b.WriteString(fmt.Sprintf("forecast cumulative 7d: %s %s\n", formatSignedNoPlus(p7, 3), u7))
		predPrinted = true
	}
	if pred30, ok := forecastCumulativePnL(ctx, cfg, state, 30); ok {
		p30, u30, ok30 := quoteToDisplay(pred30, cfg, displayCurrency, spot)
		if !ok30 {
			p30 = pred30
			u30 = strings.ToUpper(cfg.QuoteAsset)
		}
		b.WriteString(fmt.Sprintf("forecast cumulative 30d: %s %s\n", formatSignedNoPlus(p30, 3), u30))
		predPrinted = true
	}
	if predPrinted {
		b.WriteString("forecast model: weighted trend + weekly seasonality\n")
	}
	if cfg.FreqtradeHistoryMode {
		if comp7, ok := forecastCompoundEarnings(ctx, cfg, state, nil, 7); ok {
			b.WriteString(fmt.Sprintf(
				"compound 7d: exp %s %s (%s%%) (p20/p80 %s/%s %s)\n",
				formatSignedNoPlus(comp7.ExpectedPnL, 3),
				strings.ToUpper(cfg.QuoteAsset),
				formatSignedNoPlus(comp7.ExpectedPct, 2),
				formatSignedNoPlus(comp7.P20PnL, 3),
				formatSignedNoPlus(comp7.P80PnL, 3),
				strings.ToUpper(cfg.QuoteAsset),
			))
			b.WriteString(fmt.Sprintf(
				"compound inputs: max_open_trades=%d open=%d balance=%s %s tradable_balance=%s %s modeled_trades/day=%.2f\n",
				comp7.MaxOpenTrades,
				comp7.OpenTrades,
				formatSignedNoPlus(comp7.WalletBalance, 3),
				strings.ToUpper(cfg.QuoteAsset),
				formatSignedNoPlus(comp7.TradableBalance, 3),
				strings.ToUpper(cfg.QuoteAsset),
				comp7.TradesPerDay,
			))
			b.WriteString(fmt.Sprintf(
				"formula: amount_to_trade = balance/max_trades = %s %s, predicted_earning_pct=%s%%, possible_trade_earning=%s %s\n",
				formatSignedNoPlus(comp7.PerTradeStake, 3),
				strings.ToUpper(cfg.QuoteAsset),
				formatSignedNoPlus(comp7.PredictedTradePct, 2),
				formatSignedNoPlus(comp7.PossibleTradePnL, 3),
				strings.ToUpper(cfg.QuoteAsset),
			))
		}
		if comp30, ok := forecastCompoundEarnings(ctx, cfg, state, nil, 30); ok {
			b.WriteString(fmt.Sprintf(
				"compound 30d: exp %s %s (%s%%) (p20/p80 %s/%s %s)\n",
				formatSignedNoPlus(comp30.ExpectedPnL, 3),
				strings.ToUpper(cfg.QuoteAsset),
				formatSignedNoPlus(comp30.ExpectedPct, 2),
				formatSignedNoPlus(comp30.P20PnL, 3),
				formatSignedNoPlus(comp30.P80PnL, 3),
				strings.ToUpper(cfg.QuoteAsset),
			))
		}
	}
	if cfg.FreqtradeHistoryMode {
		b.WriteString("profit % = (sell - buy - fee) / buy (closed trades)\n")
		b.WriteString("note: Freqtrade UI may show a different % when using wallet/equity as denominator.\n")
		b.WriteString("compound model: log-return compounding with winsorized trade returns and max-open-trades capacity cap.\n")
	}
	if showPnLEmojis {
		b.WriteString("legend: 🟢 gain | 🔴 loss | ⚪ flat\n")
	}
	return b.String(), nil
}

func freqtradeDailyPnlRows(trades []freqtradeTrade, days int) ([]dailyPnlRow, map[string]int) {
	type agg struct {
		pnl   float64
		buy   float64
		sell  float64
		fee   float64
		count int
	}
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days-1) * 24 * time.Hour).Truncate(24 * time.Hour)
	byDay := map[string]agg{}

	for _, tr := range trades {
		if tr.CloseTimestamp <= 0 {
			continue
		}
		t := time.UnixMilli(tr.CloseTimestamp).UTC()
		if t.Before(start) {
			continue
		}
		day := t.Format("2006-01-02")
		a := byDay[day]
		buy := tr.StakeAmount
		sell := tr.Amount * tr.CloseRate
		fee := (buy * tr.FeeOpen) + (sell * tr.FeeClose)
		a.buy += buy
		a.sell += sell
		a.fee += fee
		a.pnl += sell - buy - fee
		a.count++
		byDay[day] = a
	}

	rows := make([]dailyPnlRow, 0, days)
	counts := make(map[string]int, days)
	for i := 0; i < days; i++ {
		day := now.Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		a := byDay[day]
		pct := 0.0
		if a.buy > 0 {
			pct = (a.pnl / a.buy) * 100
		}
		rows = append(rows, dailyPnlRow{Day: day, PnL: a.pnl, Pct: pct})
		counts[day] = a.count
	}
	return rows, counts
}

type pairPnlAgg struct {
	Pair  string
	PnL   float64
	Count int
}

func buildPairLeaderboard(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, d time.Duration, label string) (string, error) {
	if !cfg.FreqtradeHistoryMode {
		return "", errors.New("leaderboard requires TRACKED_SYMBOLS=FREQTRADE")
	}
	trades, err := getFreqtradeTrades30dCached(ctx, cfg)
	if err != nil {
		return "", err
	}
	sinceMS := time.Now().UTC().Add(-d).UnixMilli()
	agg := map[string]*pairPnlAgg{}
	for _, tr := range trades {
		if tr.CloseTimestamp <= 0 || tr.CloseTimestamp < sinceMS {
			continue
		}
		pair := normalizePairToSymbol(tr.Pair)
		if pair == "" {
			continue
		}
		a := agg[pair]
		if a == nil {
			a = &pairPnlAgg{Pair: pair}
			agg[pair] = a
		}
		a.PnL += tr.ProfitAbs
		a.Count++
	}

	winners := make([]pairPnlAgg, 0, len(agg))
	losers := make([]pairPnlAgg, 0, len(agg))
	for _, a := range agg {
		if a.PnL > 0 {
			winners = append(winners, *a)
		} else if a.PnL < 0 {
			losers = append(losers, *a)
		}
	}

	sort.Slice(winners, func(i, j int) bool { return winners[i].PnL > winners[j].PnL })
	sort.Slice(losers, func(i, j int) bool { return losers[i].PnL < losers[j].PnL })
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, d)
	displayUnit := cfg.QuoteAsset
	if strings.EqualFold(displayCurrency, "BNB") && spot > 0 {
		displayUnit = cfg.BNBAsset
	}
	if len(winners) > 5 {
		winners = winners[:5]
	}
	if len(losers) > 5 {
		losers = losers[:5]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Pair Leaderboard (%s)\n", strings.Title(label)))
	b.WriteString(fmt.Sprintf("pair      pnl_%s   trades\n", strings.ToLower(displayUnit)))
	b.WriteString("---------------------------\n")
	b.WriteString("Top Winners\n")
	if len(winners) == 0 {
		b.WriteString("none\n")
	} else {
		for _, row := range winners {
			pnlVal, _, ok := quoteToDisplay(row.PnL, cfg, displayCurrency, spot)
			if !ok {
				pnlVal = row.PnL
			}
			b.WriteString(fmt.Sprintf("%-9s %-10s %d\n", shortenSymbol(row.Pair), formatSignedNoPlus(pnlVal, 4), row.Count))
		}
	}
	b.WriteString("\nTop Losers\n")
	if len(losers) == 0 {
		b.WriteString("none\n")
	} else {
		for _, row := range losers {
			pnlVal, _, ok := quoteToDisplay(row.PnL, cfg, displayCurrency, spot)
			if !ok {
				pnlVal = row.PnL
			}
			b.WriteString(fmt.Sprintf("%-9s %-10s %d\n", shortenSymbol(row.Pair), formatSignedNoPlus(pnlVal, 4), row.Count))
		}
	}
	return b.String(), nil
}
