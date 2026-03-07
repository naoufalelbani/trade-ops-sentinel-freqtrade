package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseHHMM(raw string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid format, expected HH:MM")
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return 0, 0, fmt.Errorf("invalid hour")
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return 0, 0, fmt.Errorf("invalid minute")
	}
	return h, m, nil
}

func compactErr(s string, max int) string {
	out := strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if max > 0 && len(out) > max {
		return out[:max-3] + "..."
	}
	return out
}

func classifyAPIError(err error) string {
	if err == nil {
		return "unknown"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline exceeded"):
		return "timeout"
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"), strings.Contains(msg, "unauthorized"), strings.Contains(msg, "forbidden"), strings.Contains(msg, "invalid api-key"), strings.Contains(msg, "signature"):
		return "auth"
	default:
		return "request"
	}
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func convertFeeAssetToQuoteAtSpot(amount float64, feeAsset, quoteAsset string, spot float64) float64 {
	asset := strings.ToUpper(strings.TrimSpace(feeAsset))
	quote := strings.ToUpper(strings.TrimSpace(quoteAsset))
	if amount == 0 {
		return 0
	}
	if asset == quote {
		return amount
	}
	if quote == "USDT" && isUSDStable(asset) {
		return amount
	}
	if spot <= 0 {
		return 0
	}
	return amount * spot
}

func formatFeeByMainCurrency(feeBNB float64, cfg Config, mainCurrency string, spot float64) string {
	quoteVal := convertFeeAssetToQuoteAtSpot(feeBNB, cfg.BNBAsset, cfg.QuoteAsset, spot)
	if strings.ToUpper(strings.TrimSpace(mainCurrency)) == "USDT" {
		if quoteVal > 0 {
			return fmt.Sprintf("%.4f %s (~%.8f %s)", quoteVal, cfg.QuoteAsset, feeBNB, cfg.BNBAsset)
		}
		return fmt.Sprintf("%.8f %s", feeBNB, cfg.BNBAsset)
	}
	if quoteVal > 0 {
		return fmt.Sprintf("%.8f %s (~%.4f %s)", feeBNB, cfg.BNBAsset, quoteVal, cfg.QuoteAsset)
	}
	return fmt.Sprintf("%.8f %s", feeBNB, cfg.BNBAsset)
}

func quoteToDisplay(amountQuote float64, cfg Config, displayCurrency string, spot float64) (float64, string, bool) {
	d := strings.ToUpper(strings.TrimSpace(displayCurrency))
	if d == "USDT" {
		return amountQuote, cfg.QuoteAsset, true
	}
	if d == "BNB" {
		if spot <= 0 {
			return amountQuote, cfg.QuoteAsset, false
		}
		return amountQuote / spot, cfg.BNBAsset, true
	}
	return amountQuote, cfg.QuoteAsset, true
}

func formatQuoteByDisplay(amountQuote float64, cfg Config, displayCurrency string, spot float64) string {
	v, unit, ok := quoteToDisplay(amountQuote, cfg, displayCurrency, spot)
	if !ok {
		return fmt.Sprintf("%.4f %s", amountQuote, cfg.QuoteAsset)
	}
	if strings.EqualFold(unit, cfg.BNBAsset) {
		return fmt.Sprintf("%.8f %s", v, unit)
	}
	return fmt.Sprintf("%.4f %s", v, unit)
}

func formatBNBWithQuote(bnb, price float64, cfg Config) string {
	if price <= 0 {
		return fmt.Sprintf("%.6f", bnb)
	}
	return fmt.Sprintf("%.6f (~%.4f %s)", bnb, bnb*price, cfg.QuoteAsset)
}

func spotForDisplay(ctx context.Context, cfg Config, binance *BinanceClient, d time.Duration) float64 {
	if strings.ToUpper(strings.TrimSpace(cfg.QuoteAsset)) == strings.ToUpper(strings.TrimSpace(cfg.BNBAsset)) {
		return 1
	}
	key := fmt.Sprintf("ft=%t;dur=%d", cfg.FreqtradeHistoryMode, int64(d/time.Hour))
	displaySpotCache.mu.Lock()
	if entry, ok := displaySpotCache.m[key]; ok && time.Since(entry.fetched) < 60*time.Second {
		displaySpotCache.mu.Unlock()
		return entry.value
	}
	displaySpotCache.mu.Unlock()

	spot := 0.0
	if cfg.FreqtradeHistoryMode {
		since := time.Now().UTC().Add(-d)
		trades, err := fetchFreqtradeTradesSince(ctx, cfg, since)
		if err != nil {
			return 0
		}
		spot = estimateFreqtradeFeeAssetPrice(trades, cfg.BNBAsset)
	} else {
		price, err := binance.GetPrice(ctx, cfg.Symbol)
		if err != nil {
			return 0
		}
		spot = price
	}
	displaySpotCache.mu.Lock()
	displaySpotCache.m[key] = spotCacheEntry{fetched: time.Now().UTC(), value: spot}
	displaySpotCache.mu.Unlock()
	return spot
}
