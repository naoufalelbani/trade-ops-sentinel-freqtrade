package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

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
		pctCell := fmt.Sprintf("%s%%", formatSignedNoPlus(r.Pct, 2))
		b.WriteString(fmt.Sprintf("%-16s %-18s %-9s\n", dayLabel, quoteCell, pctCell))
	}
	return b.String(), nil
}

func freqtradeDailyPnlRows(trades []freqtradeTrade, days int) ([]dailyPnlRow, map[string]int) {
	type agg struct {
		pnl   float64
		stake float64
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
		a.pnl += tr.ProfitAbs
		a.stake += tr.StakeAmount
		a.count++
		byDay[day] = a
	}

	rows := make([]dailyPnlRow, 0, days)
	counts := make(map[string]int, days)
	for i := 0; i < days; i++ {
		day := now.Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		a := byDay[day]
		pct := 0.0
		if a.stake > 0 {
			pct = (a.pnl / a.stake) * 100
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
