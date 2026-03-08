package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

func formatTradesTable(label string, trades []myTrade, cfg Config, bnbPrice float64, displayCurrency string) string {
	type group struct {
		Symbol   string
		Trades   int
		BuyVal   float64
		SellVal  float64
		FeeQuote float64
		PnLQuote float64
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Trades Grouped (%s)\n", strings.Title(label)))
	b.WriteString("sym      trd buy      sell     fee      pnl\n")
	b.WriteString("------------------------------------------------\n")
	if len(trades) == 0 {
		b.WriteString("No trades found.\n")
		return b.String()
	}

	groups := map[string]*group{}
	for _, tr := range trades {
		symbol := shortenSymbol(tr.Symbol)
		g := groups[symbol]
		if g == nil {
			g = &group{Symbol: symbol}
			groups[symbol] = g
		}
		g.Trades++
		qv, _ := strconv.ParseFloat(strings.TrimSpace(tr.QuoteQty), 64)
		if tr.IsBuyer {
			g.BuyVal += qv
		} else {
			g.SellVal += qv
		}
		g.FeeQuote += tradeFeeUSDTValue(tr, cfg, bnbPrice)
	}

	rows := make([]group, 0, len(groups))
	totalTrades := 0
	totalBuy := 0.0
	totalSell := 0.0
	totalFee := 0.0
	totalPnL := 0.0
	for _, g := range groups {
		g.PnLQuote = g.SellVal - g.BuyVal - g.FeeQuote
		rows = append(rows, *g)
		totalTrades += g.Trades
		totalBuy += g.BuyVal
		totalSell += g.SellVal
		totalFee += g.FeeQuote
		totalPnL += g.PnLQuote
	}
	sort.Slice(rows, func(i, j int) bool {
		return math.Abs(rows[i].PnLQuote) > math.Abs(rows[j].PnLQuote)
	})
	for _, r := range rows {
		buyVal, _, _ := quoteToDisplay(r.BuyVal, cfg, displayCurrency, bnbPrice)
		sellVal, _, _ := quoteToDisplay(r.SellVal, cfg, displayCurrency, bnbPrice)
		feeVal, _, _ := quoteToDisplay(r.FeeQuote, cfg, displayCurrency, bnbPrice)
		pnlVal, unit, _ := quoteToDisplay(r.PnLQuote, cfg, displayCurrency, bnbPrice)
		b.WriteString(fmt.Sprintf(
			"%-8s %-3d %-8s %-8s %-8s %s\n",
			r.Symbol,
			r.Trades,
			fmtCompactNum(formatFloat(buyVal, 8), 2, 2),
			fmtCompactNum(formatFloat(sellVal, 8), 2, 2),
			fmtCompactNum(formatFloat(feeVal, 8), 2, 4),
			formatSignedNoPlus(pnlVal, 4)+" "+unit,
		))
	}
	totalBuyDisp, unit, _ := quoteToDisplay(totalBuy, cfg, displayCurrency, bnbPrice)
	totalSellDisp, _, _ := quoteToDisplay(totalSell, cfg, displayCurrency, bnbPrice)
	totalFeeDisp, _, _ := quoteToDisplay(totalFee, cfg, displayCurrency, bnbPrice)
	totalPnLDisp, _, _ := quoteToDisplay(totalPnL, cfg, displayCurrency, bnbPrice)
	b.WriteString("------------------------------------------------\n")
	b.WriteString(fmt.Sprintf(
		"TOTAL    %-3d %-8s %-8s %-8s %s %s\n",
		totalTrades,
		fmtCompactNum(formatFloat(totalBuyDisp, 8), 2, 2),
		fmtCompactNum(formatFloat(totalSellDisp, 8), 2, 2),
		fmtCompactNum(formatFloat(totalFeeDisp, 8), 2, 4),
		formatSignedNoPlus(totalPnLDisp, 4),
		unit,
	))
	b.WriteString(fmt.Sprintf("pnl = sell - buy - fee (%s)\n", unit))
	return b.String()
}

func formatFreqtradeTradesGroupedTable(label string, trades []freqtradeTrade, since time.Time, cfg Config, displayCurrency string, bnbPrice float64) string {
	type group struct {
		Symbol   string
		Trades   int
		BuyVal   float64
		SellVal  float64
		FeeQuote float64
		PnLQuote float64
	}
	sinceMS := since.UnixMilli()
	groups := map[string]*group{}

	for _, tr := range trades {
		symbol := shortenSymbol(normalizePairToSymbol(tr.Pair))
		if symbol == "" {
			continue
		}
		include := (tr.OpenTimestamp >= sinceMS) || (tr.CloseTimestamp > 0 && tr.CloseTimestamp >= sinceMS)
		if !include {
			continue
		}
		g := groups[symbol]
		if g == nil {
			g = &group{Symbol: symbol}
			groups[symbol] = g
		}
		if tr.OpenTimestamp >= sinceMS {
			g.Trades++
			g.BuyVal += tr.StakeAmount
			g.FeeQuote += tr.StakeAmount * tr.FeeOpen
		}
		if tr.CloseTimestamp > 0 && tr.CloseTimestamp >= sinceMS {
			g.Trades++
			g.SellVal += tr.Amount * tr.CloseRate
			g.FeeQuote += (tr.Amount * tr.CloseRate) * tr.FeeClose
			g.PnLQuote += tr.ProfitAbs
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Trades Grouped (%s)\n", strings.Title(label)))
	b.WriteString("sym      trd buy      sell     fee      pnl\n")
	b.WriteString("------------------------------------------------\n")
	if len(groups) == 0 {
		b.WriteString("No trades found.\n")
		return b.String()
	}

	rows := make([]group, 0, len(groups))
	totalTrades := 0
	totalBuy := 0.0
	totalSell := 0.0
	totalFee := 0.0
	totalPnL := 0.0
	for _, g := range groups {
		rows = append(rows, *g)
		totalTrades += g.Trades
		totalBuy += g.BuyVal
		totalSell += g.SellVal
		totalFee += g.FeeQuote
		totalPnL += g.PnLQuote
	}
	sort.Slice(rows, func(i, j int) bool { return math.Abs(rows[i].PnLQuote) > math.Abs(rows[j].PnLQuote) })
	for _, r := range rows {
		buyVal, _, _ := quoteToDisplay(r.BuyVal, cfg, displayCurrency, bnbPrice)
		sellVal, _, _ := quoteToDisplay(r.SellVal, cfg, displayCurrency, bnbPrice)
		feeVal, _, _ := quoteToDisplay(r.FeeQuote, cfg, displayCurrency, bnbPrice)
		pnlVal, unit, _ := quoteToDisplay(r.PnLQuote, cfg, displayCurrency, bnbPrice)
		b.WriteString(fmt.Sprintf(
			"%-8s %-3d %-8s %-8s %-8s %s\n",
			r.Symbol,
			r.Trades,
			fmtCompactNum(formatFloat(buyVal, 8), 2, 2),
			fmtCompactNum(formatFloat(sellVal, 8), 2, 2),
			fmtCompactNum(formatFloat(feeVal, 8), 2, 4),
			formatSignedNoPlus(pnlVal, 4)+" "+unit,
		))
	}
	totalBuyDisp, unit, _ := quoteToDisplay(totalBuy, cfg, displayCurrency, bnbPrice)
	totalSellDisp, _, _ := quoteToDisplay(totalSell, cfg, displayCurrency, bnbPrice)
	totalFeeDisp, _, _ := quoteToDisplay(totalFee, cfg, displayCurrency, bnbPrice)
	totalPnLDisp, _, _ := quoteToDisplay(totalPnL, cfg, displayCurrency, bnbPrice)
	b.WriteString("------------------------------------------------\n")
	b.WriteString(fmt.Sprintf(
		"TOTAL    %-3d %-8s %-8s %-8s %s %s\n",
		totalTrades,
		fmtCompactNum(formatFloat(totalBuyDisp, 8), 2, 2),
		fmtCompactNum(formatFloat(totalSellDisp, 8), 2, 2),
		fmtCompactNum(formatFloat(totalFeeDisp, 8), 2, 4),
		formatSignedNoPlus(totalPnLDisp, 4),
		unit,
	))
	b.WriteString(fmt.Sprintf("pnl = realized closed profit_abs (%s)\n", unit))
	return b.String()
}

func tradeFeeUSDT(tr myTrade, cfg Config, bnbPrice float64) string {
	v := tradeFeeUSDTValue(tr, cfg, bnbPrice)
	return fmtCompactNum(formatFloat(v, 8), 2, 4)
}

func tradeFeeUSDTValue(tr myTrade, cfg Config, bnbPrice float64) float64 {
	fee, err := strconv.ParseFloat(strings.TrimSpace(tr.Commission), 64)
	if err != nil || fee <= 0 {
		return 0
	}
	asset := strings.ToUpper(strings.TrimSpace(tr.CommissionAsset))
	switch asset {
	case strings.ToUpper(strings.TrimSpace(cfg.QuoteAsset)):
		return fee
	case strings.ToUpper(strings.TrimSpace(cfg.BNBAsset)):
		return convertFeeAssetToQuoteAtSpot(fee, cfg.BNBAsset, cfg.QuoteAsset, bnbPrice)
	}
	return 0
}

func shortenSymbol(symbol string) string {
	up := strings.ToUpper(strings.TrimSpace(symbol))
	for _, suf := range []string{"USDT", "BUSD", "USDC", "BTC", "ETH", "BNB"} {
		if strings.HasSuffix(up, suf) && len(up) > len(suf)+1 {
			return up[:len(up)-len(suf)]
		}
	}
	return up
}

func fmtCompactNum(raw string, wholeDigits int, fracDigits int) string {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return trimNum(raw)
	}
	s := strconv.FormatFloat(v, 'f', fracDigits, 64)
	s = trimNum(s)
	parts := strings.SplitN(s, ".", 2)
	if len(parts[0]) > wholeDigits && len(parts) == 2 {
		// Keep width small on mobile: cut fractional precision for larger values.
		return parts[0]
	}
	return s
}
