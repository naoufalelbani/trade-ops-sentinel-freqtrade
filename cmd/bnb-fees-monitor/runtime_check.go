package main

import (
	"bnb-fees-monitor/internal/services"
	"context"
	"fmt"
	"log"
	"strconv"
	"time"
)

func runCheck(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState) error {
	started := time.Now()
	defer logTiming("run_check", started)
	checkNo := state.incChecks()

	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return err
	}
	minBNBThreshold, targetBNBThreshold, err := cfg.resolveBNBThresholds(price, portfolioQuote)
	if err != nil {
		return err
	}

	state.addSnapshot(Snapshot{
		TS:             time.Now().UTC().UnixMilli(),
		BNBFree:        bnbFree,
		QuoteFree:      quoteFree,
		PortfolioQuote: portfolioQuote,
	})
	if err := state.save(); err != nil {
		log.Printf("state save warning: %v", err)
	}

	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	statusLine := fmt.Sprintf(
		"Status #%d\nBNB: %s\n%s: %s\nPrice %s: %.4f\nPortfolio: %s",
		checkNo,
		formatBNBWithQuote(bnbFree, price, cfg),
		cfg.QuoteAsset,
		formatQuoteByDisplay(quoteFree, cfg, displayCurrency, price),
		cfg.Symbol,
		price,
		formatQuoteByDisplay(portfolioQuote, cfg, displayCurrency, price),
	)

	if cfg.NotifyOnEveryCheck {
		safeSend(notifier, statusLine, nil)
	} else if cfg.SummaryEveryChecks > 0 && checkNo%cfg.SummaryEveryChecks == 0 {
		safeSend(notifier, statusLine, nil)
	}
	checkAbnormalMoveAlerts(cfg, state, runtimeAlerts)

	if bnbFree >= minBNBThreshold {
		log.Printf("check=%d bnb=%.6f >= threshold=%.6f", checkNo, bnbFree, minBNBThreshold)
		return nil
	}

	lastBuyAt := state.getLastBuyAt()
	if !lastBuyAt.IsZero() && time.Since(lastBuyAt) < cfg.BuyCooldown {
		left := cfg.BuyCooldown - time.Since(lastBuyAt)
		msg := fmt.Sprintf("BNB low (%.6f < %.6f), cooldown active: %s", bnbFree, minBNBThreshold, left.Round(time.Second))
		log.Print(msg)
		safeSend(notifier, msg, defaultKeyboard())
		return nil
	}

	needBNB := targetBNBThreshold - bnbFree
	if needBNB <= 0 {
		needBNB = minBNBThreshold - bnbFree
	}
	if needBNB <= 0 {
		return nil
	}

	toSpend := needBNB * price
	if cfg.MaxBuyQuote > 0 && toSpend > cfg.MaxBuyQuote {
		toSpend = cfg.MaxBuyQuote
	}

	minNotional, err := binance.GetMinNotional(ctx, cfg.Symbol)
	if err != nil {
		return fmt.Errorf("get min notional: %w", err)
	}
	if minNotional > 0 && toSpend < minNotional {
		toSpend = minNotional
	}
	if toSpend < cfg.MinBuyQuote {
		toSpend = cfg.MinBuyQuote
	}

	available := quoteFree * cfg.AccountReserveRatio
	if available <= 0 {
		msg := fmt.Sprintf("BNB low (%.6f) but no %s available", bnbFree, cfg.QuoteAsset)
		safeSend(notifier, msg, defaultKeyboard())
		return nil
	}
	if toSpend > available {
		toSpend = available
	}
	if minNotional > 0 && toSpend < minNotional {
		msg := fmt.Sprintf("BNB low, available %.4f %s is below minNotional %.4f", toSpend, cfg.QuoteAsset, minNotional)
		safeSend(notifier, msg, defaultKeyboard())
		return nil
	}

	order, err := binance.MarketBuyByQuote(ctx, cfg.Symbol, toSpend)
	if err != nil {
		return fmt.Errorf("market buy failed: %w", err)
	}
	executedQty, _ := strconv.ParseFloat(order.ExecutedQty, 64)
	quoteQty, _ := strconv.ParseFloat(order.CummulativeQuoteQty, 64)
	state.setLastBuyAt(time.Now().UTC())
	state.addRefillEvent(RefillEvent{
		TS:            time.Now().UTC().UnixMilli(),
		OrderID:       order.OrderID,
		QuoteSpent:    quoteQty,
		BNBReceived:   executedQty,
		OrderStatus:   order.Status,
		TradingSymbol: cfg.Symbol,
	})
	_ = state.save()

	msg := fmt.Sprintf(
		"Bought BNB for fees\nOrderID: %d\nStatus: %s\nSpent: %.4f %s\nReceived: %.6f %s",
		order.OrderID,
		order.Status,
		quoteQty,
		cfg.QuoteAsset,
		executedQty,
		cfg.BNBAsset,
	)
	safeSend(notifier, msg, defaultKeyboard())
	log.Printf("buy executed orderId=%d spent=%f received=%f", order.OrderID, quoteQty, executedQty)

	return nil
}

func executeManualBNBBuy(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState, force bool) (string, error) {
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}
	minBNBThreshold, targetBNBThreshold, err := cfg.resolveBNBThresholds(price, portfolioQuote)
	if err != nil {
		return "", err
	}

	lastBuyAt := state.getLastBuyAt()
	if !force && !lastBuyAt.IsZero() && time.Since(lastBuyAt) < cfg.BuyCooldown {
		left := cfg.BuyCooldown - time.Since(lastBuyAt)
		return fmt.Sprintf("Cooldown active: %s", left.Round(time.Second)), nil
	}
	if !force && bnbFree >= minBNBThreshold {
		return fmt.Sprintf("BNB is already above threshold (%.6f >= %.6f).", bnbFree, minBNBThreshold), nil
	}

	needBNB := targetBNBThreshold - bnbFree
	if needBNB <= 0 {
		if force {
			needBNB = cfg.MinBuyQuote / price
		} else {
			needBNB = minBNBThreshold - bnbFree
		}
	}
	if needBNB <= 0 {
		return "Nothing to buy right now.", nil
	}

	toSpend := needBNB * price
	if cfg.MaxBuyQuote > 0 && toSpend > cfg.MaxBuyQuote {
		toSpend = cfg.MaxBuyQuote
	}
	minNotional, err := binance.GetMinNotional(ctx, cfg.Symbol)
	if err != nil {
		return "", fmt.Errorf("get min notional: %w", err)
	}
	if minNotional > 0 && toSpend < minNotional {
		toSpend = minNotional
	}
	if toSpend < cfg.MinBuyQuote {
		toSpend = cfg.MinBuyQuote
	}

	available := quoteFree * cfg.AccountReserveRatio
	if available <= 0 {
		return fmt.Sprintf("No %s available for buy", cfg.QuoteAsset), nil
	}
	if toSpend > available {
		toSpend = available
	}
	if minNotional > 0 && toSpend < minNotional {
		return fmt.Sprintf("Available %.4f %s is below minNotional %.4f", toSpend, cfg.QuoteAsset, minNotional), nil
	}

	order, err := binance.MarketBuyByQuote(ctx, cfg.Symbol, toSpend)
	if err != nil {
		return "", fmt.Errorf("market buy failed: %w", err)
	}
	executedQty, _ := strconv.ParseFloat(order.ExecutedQty, 64)
	quoteQty, _ := strconv.ParseFloat(order.CummulativeQuoteQty, 64)
	state.setLastBuyAt(time.Now().UTC())
	state.addRefillEvent(RefillEvent{
		TS:            time.Now().UTC().UnixMilli(),
		OrderID:       order.OrderID,
		QuoteSpent:    quoteQty,
		BNBReceived:   executedQty,
		OrderStatus:   order.Status,
		TradingSymbol: cfg.Symbol,
	})
	_ = state.save()

	mode := "Refill"
	if force {
		mode = "Force Buy"
	}
	return fmt.Sprintf(
		"%s executed\nOrderID: %d\nStatus: %s\nSpent: %.4f %s\nReceived: %.6f %s",
		mode,
		order.OrderID,
		order.Status,
		quoteQty,
		cfg.QuoteAsset,
		executedQty,
		cfg.BNBAsset,
	), nil
}

func checkAbnormalMoveAlerts(cfg Config, state *MonitorState, alerts *alertManager) {
	if alerts == nil {
		return
	}
	services.CheckAbnormalMoveAlerts(
		cfg.AbnormalMoveAlertEnabled,
		cfg.QuoteAsset,
		cfg.AbnormalMoveDrop1hPct,
		cfg.AbnormalMoveDrop24hPct,
		cfg.AbnormalMoveAlertCooldown,
		state.pnlSince,
		alerts.sendDedup,
	)
}
