package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func (s *TradeStore) lastTradeTime(symbol string) (int64, bool, error) {
	var ts sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(trade_time) FROM trades WHERE symbol=?`, symbol).Scan(&ts)
	if err != nil {
		return 0, false, err
	}
	if !ts.Valid {
		return 0, false, nil
	}
	return ts.Int64, true, nil
}

func (s *TradeStore) lastSyncedTime(symbol string) (int64, bool, error) {
	var ts sql.NullInt64
	err := s.db.QueryRow(`SELECT last_synced_time FROM trade_sync_state WHERE symbol=?`, symbol).Scan(&ts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if !ts.Valid {
		return 0, false, nil
	}
	return ts.Int64, true, nil
}

func (s *TradeStore) setLastSyncedTime(ctx context.Context, symbol string, ts int64) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO trade_sync_state(symbol, last_synced_time) VALUES(?, ?)
ON CONFLICT(symbol) DO UPDATE SET last_synced_time=excluded.last_synced_time
`, symbol, ts)
	return err
}

func (s *TradeStore) SyncSymbols(ctx context.Context, binance *BinanceClient, symbols []string) error {
	return s.syncSymbols(ctx, binance, symbols, false)
}

func (s *TradeStore) SyncSymbolsForce(ctx context.Context, binance *BinanceClient, symbols []string) error {
	return s.syncSymbols(ctx, binance, symbols, true)
}

func (s *TradeStore) syncSymbols(ctx context.Context, binance *BinanceClient, symbols []string, force bool) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	for _, symbol := range symbols {
		if err := s.syncSymbol(ctx, binance, symbol, force); err != nil {
			return err
		}
	}
	return nil
}

func (s *TradeStore) SyncFromFreqtrade(ctx context.Context, cfg Config) error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	now := time.Now().UTC().UnixMilli()
	lastSyncTS, hasSync, err := s.lastSyncedTime("__FREQTRADE__")
	if err != nil {
		return err
	}
	if hasSync && s.syncInterval > 0 {
		elapsed := now - lastSyncTS
		if elapsed >= 0 && elapsed < s.syncInterval.Milliseconds() {
			return nil
		}
	}

	trades, err := fetchFreqtradeTrades(ctx, cfg)
	if err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	symbolSet := map[string]struct{}{}
	for _, tr := range trades {
		symbol := normalizePairToSymbol(tr.Pair)
		if symbol != "" {
			symbolSet[symbol] = struct{}{}
		}
	}
	if len(symbolSet) > 0 {
		symbols := make([]string, 0, len(symbolSet))
		for s := range symbolSet {
			symbols = append(symbols, s)
		}
		delQ, delArgs := inClause(`DELETE FROM trades WHERE symbol IN (%s)`, symbols)
		if _, err := tx.ExecContext(ctx, delQ, delArgs...); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT OR REPLACE INTO trades(symbol, trade_id, order_id, side, price, qty, quote_qty, commission, commission_asset, trade_time)
VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, tr := range trades {
		symbol := normalizePairToSymbol(tr.Pair)
		if symbol == "" || tr.TradeID <= 0 || tr.OpenTimestamp <= 0 {
			continue
		}
		openTradeID := tr.TradeID*10 + 1
		if _, err := stmt.ExecContext(
			ctx,
			symbol,
			openTradeID,
			openTradeID,
			"BUY",
			tr.OpenRate,
			tr.Amount,
			tr.StakeAmount,
			freqtradeCommissionOpen(tr),
			strings.ToUpper(strings.TrimSpace(tr.FeeOpenCurrency)),
			tr.OpenTimestamp,
		); err != nil {
			_ = tx.Rollback()
			return err
		}
		if tr.CloseTimestamp > 0 && tr.CloseRate > 0 {
			closeTradeID := tr.TradeID*10 + 2
			if _, err := stmt.ExecContext(
				ctx,
				symbol,
				closeTradeID,
				closeTradeID,
				"SELL",
				tr.CloseRate,
				tr.Amount,
				tr.Amount*tr.CloseRate,
				freqtradeCommissionClose(tr),
				strings.ToUpper(strings.TrimSpace(tr.FeeCloseCurrency)),
				tr.CloseTimestamp,
			); err != nil {
				_ = tx.Rollback()
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.setLastSyncedTime(ctx, "__FREQTRADE__", now)
}

func (s *TradeStore) SyncSymbol(ctx context.Context, binance *BinanceClient, symbol string) error {
	return s.syncSymbol(ctx, binance, symbol, false)
}

func (s *TradeStore) syncSymbol(ctx context.Context, binance *BinanceClient, symbol string, force bool) error {
	lastSyncTS, hasSync, err := s.lastSyncedTime(symbol)
	if err != nil {
		return err
	}
	lastTradeTS, hasTrade, err := s.lastTradeTime(symbol)
	if err != nil {
		return err
	}

	nowTime := time.Now().UTC()
	now := nowTime.UnixMilli()
	if !force && hasSync && s.syncInterval > 0 {
		elapsed := now - lastSyncTS
		if elapsed >= 0 && elapsed < s.syncInterval.Milliseconds() {
			return nil
		}
	}
	maxWindowStart := nowTime.Add(-time.Duration(s.maxLookbackDays) * 24 * time.Hour).UnixMilli()
	var start int64
	if hasTrade {
		start = lastTradeTS + 1
		if hasSync && lastSyncTS+1 > start {
			start = lastSyncTS + 1
		}
	} else if hasSync {
		start = lastSyncTS + 1
	} else {
		// No local data: bootstrap last max lookback window (30 days max by config clamp).
		start = maxWindowStart
	}
	if start < maxWindowStart {
		start = maxWindowStart
	}
	if start > now {
		_ = s.setLastSyncedTime(ctx, symbol, now)
		return nil
	}
	trades, err := binance.GetMyTrades(ctx, symbol, start, now)
	if err != nil {
		return err
	}
	if len(trades) == 0 {
		return s.setLastSyncedTime(ctx, symbol, now)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT OR REPLACE INTO trades(symbol, trade_id, order_id, side, price, qty, quote_qty, commission, commission_asset, trade_time)
VALUES(?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, tr := range trades {
		side := "SELL"
		if tr.IsBuyer {
			side = "BUY"
		}
		price, _ := strconv.ParseFloat(tr.Price, 64)
		qty, _ := strconv.ParseFloat(tr.Qty, 64)
		quoteQty, _ := strconv.ParseFloat(tr.QuoteQty, 64)
		fee, _ := strconv.ParseFloat(tr.Commission, 64)
		if _, err := stmt.ExecContext(ctx, tr.Symbol, tr.ID, tr.OrderID, side, price, qty, quoteQty, fee, tr.CommissionAsset, tr.Time); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.setLastSyncedTime(ctx, symbol, now)
}

func (s *TradeStore) SumFeesSince(symbols []string, feeAsset string, sinceMS int64) (float64, error) {
	if len(symbols) == 0 {
		return 0, nil
	}
	q, args := inClause(`SELECT COALESCE(SUM(commission),0) FROM trades WHERE UPPER(commission_asset)=UPPER(?) AND trade_time>=? AND symbol IN (%s)`, symbols)
	args = append([]any{strings.TrimSpace(feeAsset), sinceMS}, args...)
	var sum float64
	if err := s.db.QueryRow(q, args...).Scan(&sum); err != nil {
		return 0, err
	}
	return sum, nil
}

func (s *TradeStore) FeeSeriesLastNDays(symbols []string, feeAsset string, days int) ([]string, []float64, error) {
	if len(symbols) == 0 {
		return nil, nil, nil
	}
	start := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	q, args := inClause(`SELECT trade_time, commission FROM trades WHERE UPPER(commission_asset)=UPPER(?) AND trade_time>=? AND symbol IN (%s)`, symbols)
	args = append([]any{strings.TrimSpace(feeAsset), start}, args...)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	buckets := map[string]float64{}
	dayHasData := map[string]bool{}
	for rows.Next() {
		var ts int64
		var fee float64
		if err := rows.Scan(&ts, &fee); err != nil {
			return nil, nil, err
		}
		day := time.UnixMilli(ts).UTC().Format("2006-01-02")
		dayHasData[day] = true
		buckets[day] += fee
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

func (s *TradeStore) ListTradesSince(symbols []string, sinceMS int64) ([]myTrade, error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	q, args := inClause(`SELECT symbol, trade_id, order_id, side, price, qty, quote_qty, commission, commission_asset, trade_time FROM trades WHERE trade_time>=? AND symbol IN (%s) ORDER BY trade_time DESC`, symbols)
	args = append([]any{sinceMS}, args...)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]myTrade, 0, 256)
	for rows.Next() {
		var tr myTrade
		var side string
		var price, qty, quoteQty, fee float64
		if err := rows.Scan(&tr.Symbol, &tr.ID, &tr.OrderID, &side, &price, &qty, &quoteQty, &fee, &tr.CommissionAsset, &tr.Time); err != nil {
			return nil, err
		}
		tr.IsBuyer = strings.EqualFold(side, "BUY")
		tr.Price = formatFloat(price, 8)
		tr.Qty = formatFloat(qty, 8)
		tr.QuoteQty = formatFloat(quoteQty, 8)
		tr.Commission = formatFloat(fee, 8)
		out = append(out, tr)
	}
	return out, nil
}

func (s *TradeStore) DailyTradeCounts(symbols []string, days int) (map[string]int, error) {
	if len(symbols) == 0 {
		return map[string]int{}, nil
	}
	start := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	q, args := inClause(`SELECT trade_time FROM trades WHERE trade_time>=? AND symbol IN (%s)`, symbols)
	args = append([]any{start}, args...)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var ts int64
		if err := rows.Scan(&ts); err != nil {
			return nil, err
		}
		day := time.UnixMilli(ts).UTC().Format("2006-01-02")
		out[day]++
	}
	return out, nil
}

func inClause(format string, symbols []string) (string, []any) {
	ph := make([]string, len(symbols))
	args := make([]any, len(symbols))
	for i, s := range symbols {
		ph[i] = "?"
		args[i] = s
	}
	return fmt.Sprintf(format, strings.Join(ph, ",")), args
}
