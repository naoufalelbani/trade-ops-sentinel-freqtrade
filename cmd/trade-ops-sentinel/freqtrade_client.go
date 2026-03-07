package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
    "time"
)

type freqtradeHistoryCache struct {
	mu       sync.Mutex
	fetched  time.Time
	trades30 []freqtradeTrade
}

var ftCache = &freqtradeHistoryCache{}

type freqtradeTradesResponse struct {
	Trades      []freqtradeTrade `json:"trades"`
	TradesCount int              `json:"trades_count"`
	Offset      int              `json:"offset"`
	TotalTrades int              `json:"total_trades"`
}

type freqtradeTrade struct {
	TradeID          int64   `json:"trade_id"`
	Pair             string  `json:"pair"`
	Amount           float64 `json:"amount"`
	StakeAmount      float64 `json:"stake_amount"`
	OpenTimestamp    int64   `json:"open_timestamp"`
	CloseTimestamp   int64   `json:"close_timestamp"`
	OpenRate         float64 `json:"open_rate"`
	CloseRate        float64 `json:"close_rate"`
	FeeOpen          float64 `json:"fee_open"`
	FeeOpenCost      float64 `json:"fee_open_cost"`
	FeeOpenCurrency  string  `json:"fee_open_currency"`
	FeeClose         float64 `json:"fee_close"`
	FeeCloseCost     float64 `json:"fee_close_cost"`
	FeeCloseCurrency string  `json:"fee_close_currency"`
	ProfitAbs        float64 `json:"profit_abs"`
}

func resolveTrackedSymbolsFromFreqtrade(ctx context.Context, cfg Config) ([]string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.FreqtradeAPIURL), "/")
	if baseURL == "" {
		return nil, errors.New("FREQTRADE_API_URL is required when TRACKED_SYMBOLS=FREQTRADE")
	}
	if strings.TrimSpace(cfg.FreqtradeUsername) == "" || strings.TrimSpace(cfg.FreqtradePassword) == "" {
		return nil, errors.New("FREQTRADE_USERNAME and FREQTRADE_PASSWORD are required when TRACKED_SYMBOLS=FREQTRADE")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	pairs := map[string]struct{}{}
	if err := fetchFreqtradePairs(ctx, client, baseURL, cfg.FreqtradeUsername, cfg.FreqtradePassword, "/api/v1/status", pairs); err != nil {
		return nil, fmt.Errorf("freqtrade status fetch failed: %w", err)
	}
	tradesPath := fmt.Sprintf("/api/v1/trades?limit=%d", cfg.FreqtradeTradesLimit)
	if err := fetchFreqtradePairs(ctx, client, baseURL, cfg.FreqtradeUsername, cfg.FreqtradePassword, tradesPath, pairs); err != nil {
		return nil, fmt.Errorf("freqtrade trades fetch failed: %w", err)
	}
	out := make([]string, 0, len(pairs))
	for s := range pairs {
		out = append(out, s)
	}
	sort.Strings(out)
	log.Printf("freqtrade tracked symbols resolved count=%d", len(out))
	return out, nil
}

func fetchFreqtradeTrades(ctx context.Context, cfg Config) ([]freqtradeTrade, error) {
	return fetchFreqtradeTradesSince(ctx, cfg, time.Now().UTC().Add(-30*24*time.Hour))
}

func shouldRetryFreqtradeHTTP(statusCode int) bool {
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests {
		return true
	}
	return statusCode >= 500
}

func freqtradeRequestWithRetry(ctx context.Context, client *http.Client, cfg Config, source, endpoint string) ([]byte, int, error) {
	maxAttempts := 3
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		started := time.Now()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, 0, err
		}
		req.SetBasicAuth(cfg.FreqtradeUsername, cfg.FreqtradePassword)
		res, err := client.Do(req)
		if err != nil {
			if runtimeAlerts != nil {
				runtimeAlerts.observeAPICall(source, time.Since(started), err)
			}
			lastErr = err
		} else {
			body, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if res.StatusCode >= 400 {
				lastErr = fmt.Errorf("%s", summarizeHTTPStatus(res.StatusCode))
				if runtimeAlerts != nil {
					runtimeAlerts.observeAPICall(source, time.Since(started), lastErr)
				}
				if !shouldRetryFreqtradeHTTP(res.StatusCode) || attempt == maxAttempts {
					return nil, res.StatusCode, lastErr
				}
			} else {
				if runtimeAlerts != nil {
					runtimeAlerts.observeAPICall(source, time.Since(started), nil)
				}
				return body, res.StatusCode, nil
			}
		}

		if attempt >= maxAttempts {
			break
		}
		wait := time.Duration(attempt*attempt) * 500 * time.Millisecond
		if runtimeAlerts != nil {
			runtimeAlerts.observeRetry(source, attempt+1, wait, lastErr)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, 0, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		lastErr = errors.New("freqtrade request failed")
	}
	return nil, 0, lastErr
}

func fetchFreqtradeTradesSince(ctx context.Context, cfg Config, since time.Time) ([]freqtradeTrade, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.FreqtradeAPIURL), "/")
	if baseURL == "" {
		return nil, errors.New("FREQTRADE_API_URL is required")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	limit := cfg.FreqtradeTradesLimit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	maxPages := cfg.FreqtradeMaxPages
	if maxPages <= 0 {
		maxPages = 20
	}

	out := make([]freqtradeTrade, 0, limit*maxPages)
	offset := 0
	sinceMS := since.UnixMilli()
	for page := 0; page < maxPages; page++ {
		endpoint := fmt.Sprintf("%s/api/v1/trades?limit=%d&offset=%d", baseURL, limit, offset)
		body, _, err := freqtradeRequestWithRetry(ctx, client, cfg, "freqtrade.trades", endpoint)
		if err != nil {
			return nil, err
		}
		var payload freqtradeTradesResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode trades response: %w", err)
		}
		if len(payload.Trades) == 0 {
			break
		}
		reachedOlder := false
		for _, tr := range payload.Trades {
			if freqtradeTradeLatestTS(tr) < sinceMS {
				reachedOlder = true
				continue
			}
			out = append(out, tr)
		}
		offset += len(payload.Trades)
		if offset >= payload.TotalTrades {
			break
		}
		if reachedOlder {
			break
		}
	}
	return out, nil
}

func getFreqtradeTrades30dCached(ctx context.Context, cfg Config) ([]freqtradeTrade, error) {
	ftCache.mu.Lock()
	if time.Since(ftCache.fetched) < 60*time.Second && len(ftCache.trades30) > 0 {
		cp := append([]freqtradeTrade(nil), ftCache.trades30...)
		ftCache.mu.Unlock()
		return cp, nil
	}
	ftCache.mu.Unlock()

	trades, err := fetchFreqtradeTradesSince(ctx, cfg, time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil {
		return nil, err
	}
	ftCache.mu.Lock()
	ftCache.trades30 = append(ftCache.trades30[:0], trades...)
	ftCache.fetched = time.Now().UTC()
	cp := append([]freqtradeTrade(nil), ftCache.trades30...)
	ftCache.mu.Unlock()
	return cp, nil
}

func freqtradeTradeLatestTS(tr freqtradeTrade) int64 {
	if tr.CloseTimestamp > tr.OpenTimestamp {
		return tr.CloseTimestamp
	}
	return tr.OpenTimestamp
}

func freqtradeCommissionOpen(tr freqtradeTrade) float64 {
	if tr.FeeOpenCost > 0 {
		return tr.FeeOpenCost
	}
	return tr.FeeOpen
}

func freqtradeCommissionClose(tr freqtradeTrade) float64 {
	if tr.FeeCloseCost > 0 {
		return tr.FeeCloseCost
	}
	return tr.FeeClose
}

func freqtradeTradeFeeInAsset(tr freqtradeTrade, asset string) (float64, float64) {
	want := strings.ToUpper(strings.TrimSpace(asset))
	if want == "" {
		return 0, 0
	}
	openCur := strings.ToUpper(strings.TrimSpace(tr.FeeOpenCurrency))
	closeCur := strings.ToUpper(strings.TrimSpace(tr.FeeCloseCurrency))

	openCost := freqtradeCommissionOpen(tr)
	closeCost := freqtradeCommissionClose(tr)
	if openCur == want && closeCur == want {
		openNotional := tr.StakeAmount
		closeNotional := tr.Amount * tr.CloseRate
		inferredPrice := inferFreqtradeAssetPrice(openNotional, tr.FeeOpen, openCost)
		if inferredPrice <= 0 {
			inferredPrice = inferFreqtradeAssetPrice(closeNotional, tr.FeeClose, closeCost)
		}
		open := normalizeFreqtradeFeeSide(openNotional, tr.FeeOpen, openCost, inferredPrice)
		close := normalizeFreqtradeFeeSide(closeNotional, tr.FeeClose, closeCost, inferredPrice)
		return open, close
	}
	open := 0.0
	close := 0.0
	if openCur == want {
		open = openCost
	}
	if closeCur == want {
		close = closeCost
	}
	return open, close
}

func inferFreqtradeAssetPrice(notional, feeRate, feeCost float64) float64 {
	if notional <= 0 || feeRate <= 0 || feeCost <= 0 {
		return 0
	}
	quoteFee := notional * feeRate
	if quoteFee <= 0 {
		return 0
	}
	implied := quoteFee / feeCost
	if implied >= 50 && implied <= 5000 {
		return implied
	}
	return 0
}

func estimateFreqtradeFeeAssetPrice(trades []freqtradeTrade, feeAsset string) float64 {
	asset := strings.ToUpper(strings.TrimSpace(feeAsset))
	if asset == "" {
		return 0
	}
	values := make([]float64, 0, len(trades)*2)
	for _, tr := range trades {
		if strings.EqualFold(strings.TrimSpace(tr.FeeOpenCurrency), asset) {
			if px := inferFreqtradeAssetPrice(tr.StakeAmount, tr.FeeOpen, freqtradeCommissionOpen(tr)); px > 0 {
				values = append(values, px)
			}
		}
		if strings.EqualFold(strings.TrimSpace(tr.FeeCloseCurrency), asset) {
			closeNotional := tr.Amount * tr.CloseRate
			if px := inferFreqtradeAssetPrice(closeNotional, tr.FeeClose, freqtradeCommissionClose(tr)); px > 0 {
				values = append(values, px)
			}
		}
	}
	if len(values) == 0 {
		return 0
	}
	sort.Float64s(values)
	return values[len(values)/2]
}

func normalizeFreqtradeFeeSide(notional, feeRate, feeCost, fallbackPrice float64) float64 {
	if feeCost <= 0 && feeRate <= 0 {
		return 0
	}
	quoteFee := 0.0
	if notional > 0 && feeRate > 0 {
		quoteFee = notional * feeRate
	}
	if feeCost > 0 && quoteFee > 0 {
		implied := quoteFee / feeCost
		if implied >= 50 && implied <= 5000 {
			return feeCost
		}
		if fallbackPrice > 0 {
			return quoteFee / fallbackPrice
		}
		// If implied price is not plausible, feeCost is likely quote-denominated in this payload.
		// In that case we cannot convert exactly without external price; return 0 to avoid inflation.
		return 0
	}
	if feeCost > 0 {
		return feeCost
	}
	if quoteFee > 0 && fallbackPrice > 0 {
		return quoteFee / fallbackPrice
	}
	return 0
}

func fetchFreqtradePairs(ctx context.Context, client *http.Client, baseURL, username, password, path string, pairs map[string]struct{}) error {
	tmpCfg := cfgWithFreqtradeAuth(username, password)
	body, _, err := freqtradeRequestWithRetry(ctx, client, tmpCfg, "freqtrade"+path, baseURL+path)
	if err != nil {
		return err
	}
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	before := len(pairs)
	collectFreqtradePairs(payload, pairs)
	log.Printf("freqtrade endpoint=%s pairs_added=%d pairs_total=%d", path, len(pairs)-before, len(pairs))
	return nil
}

func cfgWithFreqtradeAuth(username, password string) Config {
	return Config{FreqtradeUsername: username, FreqtradePassword: password}
}

func collectFreqtradePairs(v any, out map[string]struct{}) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if strings.EqualFold(k, "pair") {
				if s, ok := val.(string); ok {
					p := normalizePairToSymbol(s)
					if p != "" {
						out[p] = struct{}{}
					}
				}
			}
			collectFreqtradePairs(val, out)
		}
	case []any:
		for _, it := range x {
			collectFreqtradePairs(it, out)
		}
	}
}

func normalizePairToSymbol(pair string) string {
	p := strings.ToUpper(strings.TrimSpace(pair))
	if p == "" {
		return ""
	}
	p = strings.ReplaceAll(p, "/", "")
	p = strings.ReplaceAll(p, "-", "")
	p = strings.ReplaceAll(p, "_", "")
	return p
}

func buildFreqtradeHealthReport(ctx context.Context, cfg Config) string {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.FreqtradeAPIURL), "/")
	if baseURL == "" {
		return "Freqtrade health: FREQTRADE_API_URL is empty"
	}
	client := &http.Client{Timeout: 10 * time.Second}

	pingLine := "Ping: n/a"
	pingReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/ping", nil)
	if err != nil {
		pingLine = fmt.Sprintf("Ping: request build error: %v", err)
	} else {
		res, reqErr := client.Do(pingReq)
		if reqErr != nil {
			pingLine = fmt.Sprintf("Ping: request error: %v", reqErr)
		} else {
			_, _ = io.ReadAll(io.LimitReader(res.Body, 256))
			_ = res.Body.Close()
			pingLine = fmt.Sprintf("Ping: %s", summarizeHTTPStatus(res.StatusCode))
		}
	}

	authUserSet := strings.TrimSpace(cfg.FreqtradeUsername) != ""
	authPassSet := strings.TrimSpace(cfg.FreqtradePassword) != ""
	statusLine := "Status: n/a"
	tradesLine := "Trades: n/a"
	if !authUserSet || !authPassSet {
		statusLine = "Status: skipped (missing FREQTRADE_USERNAME/FREQTRADE_PASSWORD)"
		tradesLine = "Trades: skipped (missing auth)"
	} else {
		statusLine = freqtradeAuthEndpointCheck(ctx, client, baseURL, cfg.FreqtradeUsername, cfg.FreqtradePassword, "/api/v1/status")
		tradesLine = freqtradeAuthEndpointCheck(ctx, client, baseURL, cfg.FreqtradeUsername, cfg.FreqtradePassword, "/api/v1/trades?limit=1")
	}
	dashboard := "API dashboard: n/a"
	watchdog := "Watchdog: n/a"
	if runtimeAlerts != nil {
		dashboard = runtimeAlerts.buildFreqtradeAPIDashboard()
		watchdog = runtimeAlerts.buildWatchdogSummary()
	}
	return fmt.Sprintf(
		"Freqtrade API Health\nURL: %s\nAuth user set: %t\nAuth pass set: %t\n\n%s\n%s\n%s\n\n%s\n%s",
		baseURL,
		authUserSet,
		authPassSet,
		pingLine,
		statusLine,
		tradesLine,
		dashboard,
		watchdog,
	)
}

func freqtradeAuthEndpointCheck(ctx context.Context, client *http.Client, baseURL, username, password, path string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return fmt.Sprintf("%s: request build error: %v", path, err)
	}
	req.SetBasicAuth(username, password)
	res, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("%s: request error: %v", path, err)
	}
	_, _ = io.ReadAll(io.LimitReader(res.Body, 256))
	_ = res.Body.Close()
	return fmt.Sprintf("%s: %s", path, summarizeHTTPStatus(res.StatusCode))
}

func summarizeHTTPStatus(code int) string {
	if code >= 200 && code < 300 {
		return fmt.Sprintf("http=%d ok", code)
	}
	return fmt.Sprintf("http=%d error", code)
}
