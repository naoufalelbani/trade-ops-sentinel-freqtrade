package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite"
)

type feeSummary struct {
	Day   float64
	Week  float64
	Month float64
}

func logTiming(name string, started time.Time) {
	log.Printf("timing op=%s duration_ms=%d", name, time.Since(started).Milliseconds())
}

func logIfErr(op string, err error) {
	if err != nil {
		log.Printf("error op=%s err=%v", op, err)
	}
}

type Config struct {
	BinanceAPIKey         string
	BinanceAPISecret      string
	Symbol                string
	TrackedSymbols        []string
	MaxAutoTrackedSymbols int
	BNBAsset              string
	QuoteAsset            string
	MinBNB                float64
	TargetBNB             float64
	MinBNBUSDT            float64
	TargetBNBUSDT         float64
	BNBRatioMode          bool
	BNBRatioMin           float64
	BNBRatioTarget        float64
	MaxBuyQuote           float64
	MinBuyQuote           float64
	CheckInterval         time.Duration
	BuyCooldown           time.Duration
	RecvWindowMs          int64

	TelegramToken      string
	TelegramChatID     string
	SummaryEveryChecks int
	NotifyOnEveryCheck bool
	BinanceBaseURL     string
	TelegramBaseURL    string

	AccountReserveRatio float64
	StateFile           string
	MaxSnapshots        int
	DailyReportEnabled  bool
	DailyReportTimeUTC  string
	DailyReportTimezone string
	DailyReportMode     string
	DailyDigestTrades   int
	FeeMainCurrency     string

	HeartbeatEnabled       bool
	HeartbeatStaleAfter    time.Duration
	HeartbeatCheckInterval time.Duration
	HeartbeatPingURL       string

	APIFailureAlertEnabled   bool
	APIFailureThreshold      int
	APIFailureAlertCooldown  time.Duration
	APILatencyThreshold      time.Duration
	APILatencySpikeThreshold int

	AbnormalMoveAlertEnabled  bool
	AbnormalMoveDrop1hPct     float64
	AbnormalMoveDrop24hPct    float64
	AbnormalMoveAlertCooldown time.Duration

	RedisEnabled              bool
	RedisAddr                 string
	RedisPassword             string
	RedisDB                   int
	RedisTradeTTL             time.Duration
	RedisPricesTTL            time.Duration
	RedisKeyPrefix            string
	MyTradesMaxConcurrency    int
	MyTradesMinInterval       time.Duration
	SQLiteEnabled             bool
	SQLitePath                string
	SQLiteInitialLookbackDays int
	SQLiteSyncInterval        time.Duration
	SQLiteMaxLookbackDays     int
	FreqtradeAPIURL           string
	FreqtradeUsername         string
	FreqtradePassword         string
	FreqtradeTradesLimit      int
	FreqtradeHistoryMode      bool
	FreqtradeMaxPages         int
}

type Snapshot struct {
	TS             int64   `json:"ts"`
	BNBFree        float64 `json:"bnb_free"`
	QuoteFree      float64 `json:"quote_free"`
	PortfolioQuote float64 `json:"portfolio_quote"`
}

type RefillEvent struct {
	TS            int64   `json:"ts"`
	OrderID       int64   `json:"order_id"`
	QuoteSpent    float64 `json:"quote_spent"`
	BNBReceived   float64 `json:"bnb_received"`
	OrderStatus   string  `json:"order_status"`
	TradingSymbol string  `json:"trading_symbol"`
}

type persistState struct {
	Checks       int           `json:"checks"`
	StartCount   int           `json:"start_count"`
	LastBuyAt    int64         `json:"last_buy_at"`
	Snapshots    []Snapshot    `json:"snapshots"`
	RefillEvents []RefillEvent `json:"refill_events"`
	FeeCurrency  string        `json:"fee_currency"`
	CustomCumWin []string      `json:"custom_cum_windows,omitempty"`
	CustomRanges []rangeRecord `json:"custom_ranges,omitempty"`
	LastUpdated  int64         `json:"last_updated"`
}

type rangeRecord struct {
	FromTS int64 `json:"from_ts"`
	ToTS   int64 `json:"to_ts"`
}

type MonitorState struct {
	mu           sync.Mutex
	checks       int
	startCount   int
	lastBuyAt    time.Time
	snapshots    []Snapshot
	refillEvents []RefillEvent
	feeCurrency  string
	customCumWin []string
	customRanges []rangeRecord
	stateFile    string
	maxSnapshots int
}

type BinanceClient struct {
	apiKey     string
	secret     string
	baseURL    string
	recvWindow int64
	httpClient *http.Client
	tradeTTL   time.Duration
	pricesTTL  time.Duration

	mu                  sync.Mutex
	minNotional         float64
	loadedMin           bool
	lastMyTradesRequest time.Time
	myTradesMinInterval time.Duration
	banUntil            time.Time

	cache       *RedisCache
	myTradesSem chan struct{}
}

type RedisCache struct {
	enabled bool
	client  *redis.Client
	prefix  string
}

type TradeStore struct {
	db                  *sql.DB
	initialLookbackDays int
	syncInterval        time.Duration
	maxLookbackDays     int
	syncMu              sync.Mutex
}

var tradeStore *TradeStore
var appCfg Config
var runtimeAlerts *alertManager

type freqtradeHistoryCache struct {
	mu       sync.Mutex
	fetched  time.Time
	trades30 []freqtradeTrade
}

var ftCache = &freqtradeHistoryCache{}

var customCumProfitInput = struct {
	mu       sync.Mutex
	awaiting map[int64]bool
}{
	awaiting: map[int64]bool{},
}

var customCumProfitRange = struct {
	mu   sync.Mutex
	from map[int64]string
}{
	from: map[int64]string{},
}

type dateRangeInputState struct {
	AwaitFrom bool
	AwaitTo   bool
	From      time.Time
}

var customCumProfitDateRange = struct {
	mu sync.Mutex
	m  map[int64]dateRangeInputState
}{
	m: map[int64]dateRangeInputState{},
}

type calendarRangeState struct {
	Phase string
	From  time.Time
	To    time.Time
}

var customCumProfitCalendarRange = struct {
	mu sync.Mutex
	m  map[int64]calendarRangeState
}{
	m: map[int64]calendarRangeState{},
}

type spotCacheEntry struct {
	fetched time.Time
	value   float64
}

var displaySpotCache = struct {
	mu sync.Mutex
	m  map[string]spotCacheEntry
}{
	m: map[string]spotCacheEntry{},
}

type alertErrorEvent struct {
	TS     time.Time
	Source string
	Err    string
}

type alertManager struct {
	mu sync.Mutex

	notifier *TelegramNotifier
	cfg      Config

	botContainerName string
	botRestartCount  int

	lastCheckSuccess time.Time
	lastCheckErr     string

	apiFailureCounts map[string]int
	apiFailureLast   map[string]time.Time

	apiLatencySpikes map[string]int
	apiLatencyLast   map[string]time.Time
	apiTotalCalls    map[string]int
	apiSuccessCalls  map[string]int
	apiErrorCalls    map[string]int
	apiLastErr       map[string]string
	apiLastLatency   map[string]time.Duration
	apiRetryCount    map[string]int
	apiBackoffUntil  map[string]time.Time
	apiDegraded      map[string]bool

	dedupLast map[string]time.Time
	errors    []alertErrorEvent
	services  map[string]*serviceHeartbeat
}

type serviceHeartbeat struct {
	LastSuccess      time.Time
	LastError        string
	ConsecutiveFails int
	RecoveryCount    int
}

type TelegramNotifier struct {
	token      string
	chatID     string
	baseURL    string
	httpClient *http.Client
}

type accountResponse struct {
	Balances []struct {
		Asset  string `json:"asset"`
		Free   string `json:"free"`
		Locked string `json:"locked"`
	} `json:"balances"`
}

type priceResponse struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

type exchangeInfoResponse struct {
	Symbols []struct {
		Symbol     string `json:"symbol"`
		Status     string `json:"status"`
		QuoteAsset string `json:"quoteAsset"`
		Filters    []struct {
			FilterType  string `json:"filterType"`
			MinNotional string `json:"minNotional"`
		} `json:"filters"`
	} `json:"symbols"`
}

type orderResponse struct {
	Symbol              string `json:"symbol"`
	OrderID             int64  `json:"orderId"`
	Status              string `json:"status"`
	ExecutedQty         string `json:"executedQty"`
	CummulativeQuoteQty string `json:"cummulativeQuoteQty"`
	TransactTime        int64  `json:"transactTime"`
}

type myTrade struct {
	ID              int64  `json:"id"`
	OrderID         int64  `json:"orderId"`
	Price           string `json:"price"`
	Qty             string `json:"qty"`
	QuoteQty        string `json:"quoteQty"`
	IsBuyer         bool   `json:"isBuyer"`
	Commission      string `json:"commission"`
	CommissionAsset string `json:"commissionAsset"`
	Time            int64  `json:"time"`
	Symbol          string `json:"-"`
}

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

type binanceErrorResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type binanceAPIError struct {
	HTTPStatus int
	Code       int
	Msg        string
	BanUntil   time.Time
}

func (e *binanceAPIError) Error() string {
	if e.BanUntil.IsZero() {
		return fmt.Sprintf("binance http=%d code=%d msg=%s", e.HTTPStatus, e.Code, e.Msg)
	}
	return fmt.Sprintf("binance http=%d code=%d msg=%s", e.HTTPStatus, e.Code, e.Msg)
}

type inlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
}

type inlineKeyboardMarkup struct {
	InlineKeyboard [][]inlineKeyboardButton `json:"inline_keyboard"`
}

type replyKeyboardMarkup struct {
	Keyboard        [][]keyboardButton `json:"keyboard"`
	ResizeKeyboard  bool               `json:"resize_keyboard,omitempty"`
	OneTimeKeyboard bool               `json:"one_time_keyboard,omitempty"`
}

type keyboardButton struct {
	Text string `json:"text"`
}

type tgUpdateResp struct {
	OK     bool       `json:"ok"`
	Result []tgUpdate `json:"result"`
}

type tgUpdate struct {
	UpdateID      int              `json:"update_id"`
	Message       *tgMessage       `json:"message,omitempty"`
	CallbackQuery *tgCallbackQuery `json:"callback_query,omitempty"`
}

type tgMessage struct {
	MessageID int    `json:"message_id"`
	Text      string `json:"text"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

type tgCallbackQuery struct {
	ID      string     `json:"id"`
	Data    string     `json:"data"`
	Message *tgMessage `json:"message,omitempty"`
}

func newAlertManager(cfg Config, notifier *TelegramNotifier) *alertManager {
	host := strings.TrimSpace(os.Getenv("HOSTNAME"))
	if host == "" {
		host = "bnb-fees-monitor"
	}
	return &alertManager{
		botContainerName: host,
		notifier:         notifier,
		cfg:              cfg,
		lastCheckSuccess: time.Now().UTC(),
		apiFailureCounts: make(map[string]int),
		apiFailureLast:   make(map[string]time.Time),
		apiLatencySpikes: make(map[string]int),
		apiLatencyLast:   make(map[string]time.Time),
		apiTotalCalls:    make(map[string]int),
		apiSuccessCalls:  make(map[string]int),
		apiErrorCalls:    make(map[string]int),
		apiLastErr:       make(map[string]string),
		apiLastLatency:   make(map[string]time.Duration),
		apiRetryCount:    make(map[string]int),
		apiBackoffUntil:  make(map[string]time.Time),
		apiDegraded:      make(map[string]bool),
		dedupLast:        make(map[string]time.Time),
		errors:           make([]alertErrorEvent, 0, 64),
		services:         make(map[string]*serviceHeartbeat),
	}
}

func (a *alertManager) markCheckSuccess() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.lastCheckSuccess = time.Now().UTC()
	a.lastCheckErr = ""
	a.serviceSuccessLocked(a.botContainerName)
	a.mu.Unlock()
}

func (a *alertManager) markCheckFailure(err error) {
	if a == nil || err == nil {
		return
	}
	a.recordError("run_check", err)
	a.mu.Lock()
	a.lastCheckErr = err.Error()
	a.serviceFailureLocked(a.botContainerName, err.Error())
	a.mu.Unlock()
}

func (a *alertManager) checkHeartbeatStale() {
	if a == nil || !a.cfg.HeartbeatEnabled || a.cfg.HeartbeatStaleAfter <= 0 {
		return
	}
	a.mu.Lock()
	last := a.lastCheckSuccess
	lastErr := a.lastCheckErr
	services := make(map[string]serviceHeartbeat, len(a.services))
	for k, v := range a.services {
		if v == nil {
			continue
		}
		services[k] = *v
	}
	a.mu.Unlock()

	staleFor := time.Since(last)
	if staleFor < a.cfg.HeartbeatStaleAfter {
		// Legacy single-check heartbeat is healthy; continue with service-specific checks.
	} else {
		msg := fmt.Sprintf(
			"Heartbeat alert: no successful check for %s (last success %s UTC). Last error: %s",
			staleFor.Round(time.Second),
			last.Format("2006-01-02 15:04:05"),
			orDefault(lastErr, "n/a"),
		)
		a.sendDedup("heartbeat.stale", a.cfg.APIFailureAlertCooldown, msg)
	}
	for name, svc := range services {
		if svc.LastSuccess.IsZero() {
			continue
		}
		svcStale := time.Since(svc.LastSuccess)
		if svcStale < a.cfg.HeartbeatStaleAfter {
			continue
		}
		restartText := "n/a"
		if name == a.botContainerName {
			restartText = strconv.Itoa(a.botRestartCount)
		} else {
			restartText = strconv.Itoa(svc.RecoveryCount)
		}
		msg := fmt.Sprintf(
			"Heartbeat watchdog alert [%s]: stale for %s (last success %s UTC). Restarts=%s Recoveries=%d Last error=%s",
			name,
			svcStale.Round(time.Second),
			svc.LastSuccess.Format("2006-01-02 15:04:05"),
			restartText,
			svc.RecoveryCount,
			orDefault(svc.LastError, "n/a"),
		)
		a.sendDedup("heartbeat.service."+name, a.cfg.APIFailureAlertCooldown, msg)
	}
}

func (a *alertManager) observeAPICall(source string, duration time.Duration, err error) {
	if a == nil || !a.cfg.APIFailureAlertEnabled {
		return
	}
	now := time.Now().UTC()
	threshold := a.cfg.APIFailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	cooldown := a.cfg.APIFailureAlertCooldown
	if cooldown <= 0 {
		cooldown = 15 * time.Minute
	}

	a.mu.Lock()
	a.apiTotalCalls[source]++
	a.apiLastLatency[source] = duration
	if strings.HasPrefix(source, "freqtrade") {
		if err == nil {
			a.serviceSuccessLocked("freqtrade")
		} else {
			a.serviceFailureLocked("freqtrade", err.Error())
		}
	}
	if err != nil {
		a.apiFailureCounts[source]++
		a.apiErrorCalls[source]++
		a.apiLastErr[source] = err.Error()
		count := a.apiFailureCounts[source]
		last := a.apiFailureLast[source]
		a.apiDegraded[source] = count >= threshold
		a.mu.Unlock()

		a.recordError(source, err)
		if count >= threshold && now.Sub(last) >= cooldown {
			a.mu.Lock()
			a.apiFailureLast[source] = now
			a.mu.Unlock()
			a.sendRaw(fmt.Sprintf("API alert [%s]: %d consecutive failures (%s). Last error: %v", source, count, classifyAPIError(err), err))
		}
		return
	}

	prevFailures := a.apiFailureCounts[source]
	a.apiFailureCounts[source] = 0
	a.apiSuccessCalls[source]++
	a.apiLastErr[source] = ""
	latencyThreshold := a.cfg.APILatencyThreshold
	latencySpikeThreshold := a.cfg.APILatencySpikeThreshold
	if latencyThreshold <= 0 || latencySpikeThreshold <= 0 {
		a.apiLatencySpikes[source] = 0
		if prevFailures >= threshold && a.apiDegraded[source] {
			a.apiDegraded[source] = false
			a.mu.Unlock()
			a.sendDedup("api.recovered."+source, cooldown, fmt.Sprintf("API recovered [%s]: request flow back to normal", source))
			return
		}
		a.apiDegraded[source] = false
		a.mu.Unlock()
		return
	}
	if duration >= latencyThreshold {
		a.apiLatencySpikes[source]++
		spikes := a.apiLatencySpikes[source]
		last := a.apiLatencyLast[source]
		a.apiDegraded[source] = spikes >= latencySpikeThreshold
		a.mu.Unlock()
		if spikes >= latencySpikeThreshold && now.Sub(last) >= cooldown {
			a.mu.Lock()
			a.apiLatencyLast[source] = now
			a.mu.Unlock()
			a.sendRaw(fmt.Sprintf("API timeout spike [%s]: %d slow calls in a row (latest %s)", source, spikes, duration.Round(time.Millisecond)))
		}
		return
	}
	a.apiLatencySpikes[source] = 0
	if prevFailures >= threshold && a.apiDegraded[source] {
		a.apiDegraded[source] = false
		a.mu.Unlock()
		a.sendDedup("api.recovered."+source, cooldown, fmt.Sprintf("API recovered [%s]: request flow back to normal", source))
		return
	}
	a.apiDegraded[source] = false
	a.mu.Unlock()
}

func (a *alertManager) observeRetry(source string, attempt int, wait time.Duration, err error) {
	if a == nil {
		return
	}
	if attempt < 1 {
		attempt = 1
	}
	a.mu.Lock()
	a.apiRetryCount[source]++
	backoffUntil := time.Now().UTC().Add(wait)
	a.apiBackoffUntil[source] = backoffUntil
	a.apiDegraded[source] = true
	a.mu.Unlock()
	msg := fmt.Sprintf(
		"API degraded [%s]: retry attempt %d scheduled in %s (backoff until %s UTC). Last error: %v",
		source,
		attempt,
		wait.Round(time.Millisecond),
		backoffUntil.Format("15:04:05"),
		err,
	)
	a.sendDedup("api.retry."+source, a.cfg.APIFailureAlertCooldown, msg)
}

func (a *alertManager) setBotRestartCount(v int) {
	if a == nil {
		return
	}
	if v < 0 {
		v = 0
	}
	a.mu.Lock()
	a.botRestartCount = v
	a.mu.Unlock()
}

func (a *alertManager) serviceSuccessLocked(name string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	svc := a.services[name]
	if svc == nil {
		svc = &serviceHeartbeat{}
		a.services[name] = svc
	}
	if svc.ConsecutiveFails > 0 {
		svc.RecoveryCount++
	}
	svc.ConsecutiveFails = 0
	svc.LastSuccess = time.Now().UTC()
}

func (a *alertManager) serviceFailureLocked(name, errMsg string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	svc := a.services[name]
	if svc == nil {
		svc = &serviceHeartbeat{}
		a.services[name] = svc
	}
	svc.ConsecutiveFails++
	svc.LastError = strings.TrimSpace(errMsg)
}

func (a *alertManager) buildFreqtradeAPIDashboard() string {
	if a == nil {
		return "API dashboard: n/a"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	keys := make([]string, 0, len(a.apiTotalCalls))
	for k := range a.apiTotalCalls {
		if strings.HasPrefix(k, "freqtrade") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "API dashboard: no freqtrade samples yet"
	}
	lines := make([]string, 0, len(keys)+1)
	lines = append(lines, "Freqtrade API Dashboard")
	now := time.Now().UTC()
	for _, k := range keys {
		backoff := "none"
		if until := a.apiBackoffUntil[k]; !until.IsZero() && until.After(now) {
			backoff = time.Until(until).Round(time.Second).String()
		}
		state := "ok"
		if a.apiDegraded[k] {
			state = "degraded"
		}
		lines = append(lines, fmt.Sprintf(
			"%s | state=%s ok=%d err=%d consec=%d slowStreak=%d retries=%d backoff=%s last=%s",
			k,
			state,
			a.apiSuccessCalls[k],
			a.apiErrorCalls[k],
			a.apiFailureCounts[k],
			a.apiLatencySpikes[k],
			a.apiRetryCount[k],
			backoff,
			a.apiLastLatency[k].Round(time.Millisecond),
		))
	}
	return strings.Join(lines, "\n")
}

func (a *alertManager) buildWatchdogSummary() string {
	if a == nil {
		return "Watchdog: n/a"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.services) == 0 {
		return "Watchdog: no services tracked yet"
	}
	keys := make([]string, 0, len(a.services))
	for k := range a.services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		svc := a.services[k]
		if svc == nil {
			continue
		}
		restarts := "n/a"
		if k == a.botContainerName {
			restarts = strconv.Itoa(a.botRestartCount)
		} else {
			restarts = strconv.Itoa(svc.RecoveryCount)
		}
		lastOK := "n/a"
		if !svc.LastSuccess.IsZero() {
			lastOK = svc.LastSuccess.Format("15:04:05")
		}
		parts = append(parts, fmt.Sprintf("%s(lastOK=%s restart=%s recov=%d)", k, lastOK, restarts, svc.RecoveryCount))
	}
	return "Watchdog: " + strings.Join(parts, " | ")
}

func (a *alertManager) recentErrorsSince(window time.Duration, limit int) []alertErrorEvent {
	if a == nil || limit <= 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := time.Now().UTC().Add(-window)
	out := make([]alertErrorEvent, 0, limit)
	for i := len(a.errors) - 1; i >= 0; i-- {
		ev := a.errors[i]
		if ev.TS.Before(cut) {
			break
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (a *alertManager) recordError(source string, err error) {
	if a == nil || err == nil {
		return
	}
	a.mu.Lock()
	a.errors = append(a.errors, alertErrorEvent{
		TS:     time.Now().UTC(),
		Source: source,
		Err:    err.Error(),
	})
	if len(a.errors) > 200 {
		a.errors = a.errors[len(a.errors)-200:]
	}
	a.mu.Unlock()
}

func (a *alertManager) sendDedup(key string, cooldown time.Duration, msg string) {
	if a == nil {
		return
	}
	if cooldown <= 0 {
		cooldown = 15 * time.Minute
	}
	now := time.Now().UTC()
	a.mu.Lock()
	last := a.dedupLast[key]
	if now.Sub(last) < cooldown {
		a.mu.Unlock()
		return
	}
	a.dedupLast[key] = now
	a.mu.Unlock()
	a.sendRaw(msg)
}

func (a *alertManager) sendRaw(msg string) {
	if a == nil || strings.TrimSpace(msg) == "" {
		return
	}
	safeSend(a.notifier, msg, defaultKeyboard())
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}
	tradeStore, err = newTradeStore(cfg)
	if err != nil {
		log.Fatalf("trade store init error: %v", err)
	}
	cache, err := newRedisCache(cfg)
	if err != nil {
		log.Fatalf("redis config error: %v", err)
	}

	binance := &BinanceClient{
		apiKey:              cfg.BinanceAPIKey,
		secret:              cfg.BinanceAPISecret,
		baseURL:             strings.TrimRight(cfg.BinanceBaseURL, "/"),
		recvWindow:          cfg.RecvWindowMs,
		httpClient:          &http.Client{Timeout: 25 * time.Second},
		cache:               cache,
		tradeTTL:            cfg.RedisTradeTTL,
		pricesTTL:           cfg.RedisPricesTTL,
		myTradesMinInterval: cfg.MyTradesMinInterval,
		myTradesSem:         make(chan struct{}, cfg.MyTradesMaxConcurrency),
	}
	resolved, err := resolveTrackedSymbols(context.Background(), cfg, binance)
	if err != nil {
		if len(cfg.TrackedSymbols) == 1 && cfg.TrackedSymbols[0] == "FREQTRADE" {
			log.Printf("resolve tracked symbols warning: %v; fallback symbol=%s", err, cfg.Symbol)
			resolved = []string{cfg.Symbol}
		} else {
			log.Fatalf("resolve tracked symbols error: %v", err)
		}
	}
	cfg.TrackedSymbols = resolved
	appCfg = cfg
	log.Printf("tracking symbols count=%d", len(cfg.TrackedSymbols))
	if tradeStore != nil {
		startupSyncCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		log.Printf("sqlite startup sync begin symbols=%d", len(cfg.TrackedSymbols))
		var syncErr error
		if cfg.FreqtradeHistoryMode {
			syncErr = tradeStore.SyncFromFreqtrade(startupSyncCtx, cfg)
		} else {
			syncErr = tradeStore.SyncSymbols(startupSyncCtx, binance, cfg.TrackedSymbols)
		}
		if syncErr != nil {
			logIfErr("sqlite.startup_sync", syncErr)
		} else {
			log.Printf("sqlite startup sync done")
		}
		cancel()
	}
	notifier := &TelegramNotifier{
		token:      cfg.TelegramToken,
		chatID:     cfg.TelegramChatID,
		baseURL:    strings.TrimRight(cfg.TelegramBaseURL, "/"),
		httpClient: &http.Client{Timeout: 40 * time.Second},
	}
	runtimeAlerts = newAlertManager(cfg, notifier)

	state := newMonitorState(cfg.StateFile, cfg.MaxSnapshots)
	if err := state.load(); err != nil {
		log.Printf("state load warning: %v", err)
	}
	if state.getDisplayCurrency(cfg.FeeMainCurrency) == "" {
		state.setDisplayCurrency(cfg.FeeMainCurrency)
	}
	startCount := state.incStartCount()
	if err := state.save(); err != nil {
		log.Printf("state save warning: %v", err)
	}
	if runtimeAlerts != nil {
		restartCount := startCount - 1
		if restartCount < 0 {
			restartCount = 0
		}
		runtimeAlerts.setBotRestartCount(restartCount)
	}

	go telegramLoop(context.Background(), cfg, binance, notifier, state)
	go dailyReportLoop(context.Background(), cfg, binance, notifier, state)
	go heartbeatLoop(context.Background(), cfg, runtimeAlerts)

	restartCount := startCount - 1
	if restartCount < 0 {
		restartCount = 0
	}
	startup := fmt.Sprintf(
		"BNB fee monitor started\nSymbol=%s\n%s\nTracked symbols=%d\nInterval=%s\nContainer=%s Restarts=%d",
		cfg.Symbol,
		cfg.thresholdModeLine(),
		len(cfg.TrackedSymbols),
		cfg.CheckInterval,
		strings.TrimSpace(orDefault(os.Getenv("HOSTNAME"), "bnb-fees-monitor")),
		restartCount,
	)
	if err := notifier.Send(startup, defaultKeyboard()); err != nil {
		log.Printf("telegram startup send failed: %v", err)
	}
	safeSend(notifier, "Keyboard shortcuts enabled. Tap: Status, Daily Report, Menu, Help", defaultReplyKeyboard())

	ticker := time.NewTicker(cfg.CheckInterval)
	defer ticker.Stop()

	if err := runCheck(context.Background(), cfg, binance, notifier, state); err != nil {
		log.Printf("initial check failed: %v", err)
		runtimeAlerts.markCheckFailure(err)
	} else {
		runtimeAlerts.markCheckSuccess()
	}

	for range ticker.C {
		if err := runCheck(context.Background(), cfg, binance, notifier, state); err != nil {
			log.Printf("check failed: %v", err)
			runtimeAlerts.markCheckFailure(err)
			safeSend(notifier, fmt.Sprintf("Check failed: %v", err), defaultKeyboard())
			continue
		}
		runtimeAlerts.markCheckSuccess()
	}
}

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
		"Status #%d\nBNB: %.6f\n%s: %s\nPrice %s: %.4f\nPortfolio: %s",
		checkNo,
		bnbFree,
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

func telegramLoop(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState) {
	if notifier.token == "" || notifier.chatID == "" {
		return
	}

	offset := 0
	for {
		updates, next, err := notifier.GetUpdates(ctx, offset)
		if err != nil {
			log.Printf("telegram poll error: %v", err)
			if runtimeAlerts != nil {
				runtimeAlerts.recordError("telegram.poll", err)
			}
			time.Sleep(3 * time.Second)
			continue
		}
		offset = next

		for _, upd := range updates {
			// Handle updates asynchronously so one heavy report does not block all Telegram interactions.
			go handleTelegramUpdate(ctx, cfg, binance, notifier, state, upd)
		}
	}
}

func handleTelegramUpdate(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState, upd tgUpdate) {
	started := time.Now()
	defer logTiming("telegram_update", started)
	if upd.Message != nil {
		if !notifier.allowedChat(upd.Message.Chat.ID) {
			return
		}
		if strings.TrimSpace(upd.Message.Text) == "" {
			return
		}
		rawText := strings.TrimSpace(upd.Message.Text)
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitDateFrom(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Date range input canceled.", chartsKeyboard())
				return
			}
			from, ok := parseUserDateTime(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid FROM date. Use: `YYYY-MM-DD HH:MM` (UTC), example `2026-03-05 14:30`.", chartsKeyboard())
				return
			}
			if from.After(time.Now().UTC()) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "FROM date cannot be in the future.", chartsKeyboard())
				return
			}
			setAwaitingCustomCumProfitDateTo(upd.Message.Chat.ID, from)
			safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("FROM set to %s UTC. Now type TO date/time (`YYYY-MM-DD HH:MM`).", from.Format("2006-01-02 15:04")), chartsKeyboard())
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitDateTo(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Date range input canceled.", chartsKeyboard())
				return
			}
			to, ok := parseUserDateTime(rawText)
			if !ok {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid TO date. Use: `YYYY-MM-DD HH:MM` (UTC), example `2026-03-06 09:00`.", chartsKeyboard())
				return
			}
			from, okFrom := getCustomCumProfitDateFrom(upd.Message.Chat.ID)
			if !okFrom {
				clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Please start again: choose date range first.", chartsKeyboard())
				return
			}
			if !to.After(from) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "Invalid TO date. TO must be after FROM.", chartsKeyboard())
				return
			}
			if to.After(time.Now().UTC()) {
				safeSendToChat(notifier, upd.Message.Chat.ID, "TO date cannot be in the future.", chartsKeyboard())
				return
			}
			clearCustomCumProfitDateRangeState(upd.Message.Chat.ID)
			safeSendToChat(
				notifier,
				upd.Message.Chat.ID,
				fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")),
				customCumProfitDateRangeGranularityKeyboard(from.Unix(), to.Unix()),
			)
			return
		}
		if !strings.HasPrefix(rawText, "/") && isAwaitingCustomCumProfitWindow(upd.Message.Chat.ID) {
			if strings.EqualFold(rawText, "cancel") || strings.EqualFold(rawText, "back") {
				setAwaitingCustomCumProfitWindow(upd.Message.Chat.ID, false)
				safeSendToChat(notifier, upd.Message.Chat.ID, "Custom cumulative profit input canceled.", chartsKeyboard())
				return
			}
			_, token, label, ok := parseCumProfitWindowInput(rawText)
			if !ok {
				safeSendToChat(
					notifier,
					upd.Message.Chat.ID,
					"Invalid window. Type like `36h`, `72h`, `3d`, `10d` (or `cancel`).",
					customCumProfitWindowKeyboard(),
				)
				return
			}
			setAwaitingCustomCumProfitWindow(upd.Message.Chat.ID, false)
			state.addCustomCumWindow(token)
			_ = state.save()
			safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("Window %s selected. Choose timeline mode:", label), customCumProfitGranularityKeyboard(token))
			return
		}
		text := normalizeCommand(upd.Message.Text)
		switch text {
		case "/menu":
			safeSendToChat(notifier, upd.Message.Chat.ID, "Main keyboard enabled.", defaultReplyKeyboard())
			safeSendToChat(notifier, upd.Message.Chat.ID, "BNB monitor menu:", defaultKeyboard())
		case "/status":
			report, err := buildStatusReport(ctx, cfg, binance, state)
			if err != nil {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("status error: %v", err), defaultKeyboard())
				return
			}
			safeSendToChat(notifier, upd.Message.Chat.ID, report, defaultKeyboard())
		case "/daily":
			if err := sendDailyReport(ctx, cfg, binance, notifier, state); err != nil {
				safeSendToChat(notifier, upd.Message.Chat.ID, fmt.Sprintf("daily report error: %v", err), defaultKeyboard())
			}
		case "/help":
			safeSendToChat(notifier, upd.Message.Chat.ID, helpText(), defaultReplyKeyboard())
		default:
			safeSendToChat(notifier, upd.Message.Chat.ID, "Unknown command.\n\n"+helpText(), defaultReplyKeyboard())
		}
		return
	}

	if upd.CallbackQuery == nil || upd.CallbackQuery.Message == nil {
		return
	}
	chatID := upd.CallbackQuery.Message.Chat.ID
	if !notifier.allowedChat(chatID) {
		return
	}

	safeAnswerCallback(notifier, upd.CallbackQuery.ID, "Processing...")
	data := upd.CallbackQuery.Data

	switch data {
	case "menu", "menu_main":
		safeSendToChat(notifier, chatID, "Main menu", defaultKeyboard())
	case "menu_actions":
		safeSendToChat(notifier, chatID, "Actions menu", actionsKeyboard())
	case "menu_reports":
		safeSendToChat(notifier, chatID, "Reports menu", reportsKeyboard())
	case "menu_charts":
		safeSendToChat(notifier, chatID, "Charts menu", chartsKeyboard())
	case "menu_settings":
		safeSendToChat(notifier, chatID, "Settings menu", settingsKeyboard())
	case "refill_now":
		msg, err := executeManualBNBBuy(ctx, cfg, binance, state, false)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("refill error: %v", err), defaultKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, msg, defaultKeyboard())
	case "force_buy":
		safeSendToChat(notifier, chatID, "Force buy BNB?\nThis will place a market order now (uses safety caps).", forceBuyConfirmKeyboard())
	case "force_buy_confirm":
		msg, err := executeManualBNBBuy(ctx, cfg, binance, state, true)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("force buy error: %v", err), defaultKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, msg, defaultKeyboard())
	case "force_buy_cancel":
		safeSendToChat(notifier, chatID, "Force buy canceled.", defaultKeyboard())
	case "daily_report_now":
		if err := sendDailyReport(ctx, cfg, binance, notifier, state); err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("daily report error: %v", err), defaultKeyboard())
		}
	case "fee_currency_menu":
		current := state.getDisplayCurrency(cfg.FeeMainCurrency)
		safeSendToChat(notifier, chatID, fmt.Sprintf("Choose display currency (current: %s):", current), feeCurrencyKeyboard())
	case "fee_currency_bnb":
		state.setDisplayCurrency("BNB")
		_ = state.save()
		safeSendToChat(notifier, chatID, "Display currency set to BNB.", settingsKeyboard())
	case "fee_currency_usdt":
		state.setDisplayCurrency("USDT")
		_ = state.save()
		safeSendToChat(notifier, chatID, "Display currency set to USDT.", settingsKeyboard())
	case "report_day", "report_week", "report_month":
		dur := selectDuration(data)
		label := durationLabel(data)
		if err := sendPeriodReport(ctx, cfg, binance, notifier, state, dur, label); err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("%s report error: %v", label, err), defaultKeyboard())
		}
	case "status":
		report, err := buildStatusReport(ctx, cfg, binance, state)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("status error: %v", err), defaultKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, report, defaultKeyboard())
	case "fees_day", "fees_week", "fees_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		v, err := totalFeesBNB(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, dur)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("fee calc error: %v", err), defaultKeyboard())
			return
		}
		spot := spotForDisplay(ctx, cfg, binance, dur)
		mainCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		feeText := formatFeeByMainCurrency(v, cfg, mainCurrency, spot)
		note := ""
		if spot > 0 {
			if cfg.FreqtradeHistoryMode {
				note = ", inferred from Freqtrade"
			} else {
				note = ", spot"
			}
		}
		safeSendToChat(
			notifier,
			chatID,
			fmt.Sprintf("Fees consumed (%s): %s%s", title, feeText, note),
			defaultKeyboard(),
		)
	case "trades_day", "trades_week", "trades_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		bnbPrice := spotForDisplay(ctx, cfg, binance, dur)
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		var table string
		if cfg.FreqtradeHistoryMode {
			ftTrades, err := getFreqtradeTrades30dCached(ctx, cfg)
			if err != nil {
				safeSendToChat(notifier, chatID, fmt.Sprintf("trades error: %v", err), defaultKeyboard())
				return
			}
			table = formatFreqtradeTradesGroupedTable(title, ftTrades, time.Now().UTC().Add(-dur), cfg, displayCurrency, bnbPrice)
		} else {
			trades, err := collectTradesByDuration(ctx, binance, cfg.TrackedSymbols, dur)
			if err != nil {
				safeSendToChat(notifier, chatID, fmt.Sprintf("trades error: %v", err), defaultKeyboard())
				return
			}
			table = formatTradesTable(title, trades, cfg, bnbPrice, displayCurrency)
		}
		safeSendPreLargeToChat(notifier, chatID, table, defaultKeyboard())
	case "leaders_day", "leaders_week", "leaders_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		text, err := buildPairLeaderboard(ctx, cfg, state, binance, dur, title)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("leaderboard error: %v", err), defaultKeyboard())
			return
		}
		safeSendPreToChat(notifier, chatID, text, defaultKeyboard())
	case "pnl_7d_table":
		text, err := buildDailyPnlTable(ctx, cfg, state, 7)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("pnl table error: %v", err), defaultKeyboard())
			return
		}
		safeSendPreToChat(notifier, chatID, text, &inlineKeyboardMarkup{
			InlineKeyboard: [][]inlineKeyboardButton{
				{{Text: "Refresh", CallbackData: "pnl_7d_table"}},
			},
		})
	case "pnl_day", "pnl_week", "pnl_month":
		dur := selectDuration(data)
		title := durationLabel(data)
		if cfg.FreqtradeHistoryMode {
			trades, err := getFreqtradeTrades30dCached(ctx, cfg)
			if err != nil {
				safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s) error: %v", title, err), defaultKeyboard())
				return
			}
			pnl, pct, ok := freqtradePnlSince(trades, time.Now().UTC().Add(-dur))
			if !ok {
				safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): not enough data yet", title), defaultKeyboard())
				return
			}
			displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
			spot := spotForDisplay(ctx, cfg, binance, dur)
			safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): %s (%.2f%%)", title, formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct), defaultKeyboard())
			return
		}
		pnl, pct, ok := state.pnlSince(dur)
		if !ok {
			safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): not enough data yet", title), defaultKeyboard())
			return
		}
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		spot := spotForDisplay(ctx, cfg, binance, dur)
		safeSendToChat(notifier, chatID, fmt.Sprintf("PnL (%s): %s (%.2f%%)", title, formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct), defaultKeyboard())
	case "chart_fees":
		labels, values, err := feeSeriesLastNDaysCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, 30)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("fees chart error: %v", err), defaultKeyboard())
			return
		}
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No fee trade data for chart yet", defaultKeyboard())
			return
		}
		chartURL := buildLineChartURL("BNB Fees (Last 30 Days)", labels, values, "BNB")
		safeSendPhotoToChat(notifier, chatID, chartURL, "Fees chart")
	case "chart_cum_fees_day", "chart_cum_fees_week", "chart_cum_fees_month":
		dur := selectDuration(data)
		labels, values, unit, err := cumulativeFeesSeriesWindow(ctx, cfg, state, binance, dur)
		if err != nil {
			safeSendToChat(notifier, chatID, fmt.Sprintf("cumulative fees chart error: %v", err), defaultKeyboard())
			return
		}
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No cumulative fee data yet", defaultKeyboard())
			return
		}
		chartURL := buildCumulativeProfitChartURL("Cumulative Fees", labels, values, unit)
		safeSendPhotoToChat(notifier, chatID, chartURL, "Cumulative Fees")
	case "chart_pnl":
		labels, values := state.pnlSeriesLastNDays(30)
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No PnL data for chart yet", defaultKeyboard())
			return
		}
		chartURL := buildLineChartURL("PnL Delta (Last 30 Days)", labels, values, cfg.QuoteAsset)
		safeSendPhotoToChat(notifier, chatID, chartURL, "PnL chart")
	case "chart_cum_profit_day", "chart_cum_profit_48h", "chart_cum_profit_72h", "chart_cum_profit_week", "chart_cum_profit_month":
		dur := selectDuration(data)
		labels, values, unit := cumulativeProfitSeriesWindow(ctx, cfg, state, binance, dur)
		if len(labels) == 0 {
			safeSendToChat(notifier, chatID, "No cumulative profit data yet", defaultKeyboard())
			return
		}
		chartURL := buildCumulativeProfitChartURL("Cumulative Profit", labels, values, unit)
		safeSendPhotoToChat(notifier, chatID, chartURL, "Cumulative Profit")
	case "chart_cum_profit_custom":
		setAwaitingCustomCumProfitWindow(chatID, true)
		safeSendToChat(notifier, chatID, "Choose cumulative profit window or type it (example: `36h`, `3d`).", customCumProfitWindowKeyboard())
	case "chart_cum_profit_range":
		clearRangeFromSelection(chatID)
		safeSendToChat(notifier, chatID, "Choose FROM (how long ago to start):", customCumProfitRangeFromKeyboard())
	case "chart_cum_profit_date_range":
		clearCalendarRangeState(chatID)
		clearCustomCumProfitDateRangeState(chatID)
		safeSendToChat(notifier, chatID, "Choose date-range input method:", customCumProfitDateRangeEntryKeyboard())
	case "chart_cum_profit_date_range_manual":
		clearCalendarRangeState(chatID)
		setAwaitingCustomCumProfitDateFrom(chatID)
		safeSendToChat(notifier, chatID, "Type FROM date/time in UTC (`YYYY-MM-DD HH:MM`).\nExample: `2026-03-01 08:00`\nType `cancel` to stop.", chartsKeyboard())
	case "chart_cum_profit_calendar_range":
		clearCustomCumProfitDateRangeState(chatID)
		setCalendarRangePhase(chatID, "from_day")
		now := time.Now().UTC()
		safeSendToChat(notifier, chatID, "Pick FROM date:", customCumProfitCalendarKeyboard("from", now.Year(), now.Month()))
	case "chart_cum_profit_custom_history":
		history := state.customCumWindows()
		if len(history) == 0 {
			safeSendToChat(notifier, chatID, "No custom history yet. Type one first (example: `36h`, `10d`).", customCumProfitWindowKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, "Custom cumulative history:", customCumProfitHistoryKeyboard(history))
	case "chart_cum_profit_range_history":
		history := state.customRangeHistory()
		if len(history) == 0 {
			safeSendToChat(notifier, chatID, "No range history yet.", chartsKeyboard())
			return
		}
		safeSendToChat(notifier, chatID, "Last 5 ranges:", customCumProfitRangeHistoryKeyboard(history))
	case "freqtrade_health":
		report := buildFreqtradeHealthReport(ctx, cfg)
		safeSendToChat(notifier, chatID, report, defaultKeyboard())
	default:
		if strings.HasPrefix(data, "ccpw_") {
			token := strings.TrimPrefix(data, "ccpw_")
			_, _, label, ok := parseCumProfitWindowInput(token)
			if !ok {
				safeSendToChat(notifier, chatID, "Invalid custom window.", chartsKeyboard())
				return
			}
			state.addCustomCumWindow(token)
			_ = state.save()
			setAwaitingCustomCumProfitWindow(chatID, false)
			safeSendToChat(notifier, chatID, fmt.Sprintf("Window %s selected. Choose timeline mode:", label), customCumProfitGranularityKeyboard(token))
			return
		}
		if strings.HasPrefix(data, "ccpg_") {
			rest := strings.TrimPrefix(data, "ccpg_")
			parts := strings.Split(rest, "_")
			if len(parts) != 2 {
				safeSendToChat(notifier, chatID, "Invalid custom chart selection.", chartsKeyboard())
				return
			}
			dur, _, label, ok := parseCumProfitWindowInput(parts[0])
			if !ok {
				safeSendToChat(notifier, chatID, "Invalid custom window.", chartsKeyboard())
				return
			}
			mode, modeLabel := parseCumProfitGranularity(parts[1])
			labels, values, unit := cumulativeProfitSeriesWindowMode(ctx, cfg, state, binance, dur, mode)
			if len(labels) == 0 {
				safeSendToChat(notifier, chatID, "No cumulative profit data yet", defaultKeyboard())
				return
			}
			title := fmt.Sprintf("Cumulative Profit (%s, %s)", label, modeLabel)
			chartURL := buildCumulativeProfitChartURL(title, labels, values, unit)
			safeSendPhotoToChat(notifier, chatID, chartURL, title)
			return
		}
		if data == "ccal_ignore" {
			return
		}
		if strings.HasPrefix(data, "ccal_") {
			rest := strings.TrimPrefix(data, "ccal_")
			parts := strings.Split(rest, "_")
			if len(parts) < 3 {
				safeSendToChat(notifier, chatID, "Invalid calendar action.", chartsKeyboard())
				return
			}
			phase := parts[0]
			action := parts[1]
			payload := parts[2]
			if phase != "from" && phase != "to" {
				safeSendToChat(notifier, chatID, "Invalid calendar phase.", chartsKeyboard())
				return
			}
			switch action {
			case "nav":
				year, month, ok := parseCalendarMonthToken(payload)
				if !ok {
					safeSendToChat(notifier, chatID, "Invalid calendar month.", chartsKeyboard())
					return
				}
				setCalendarRangePhase(chatID, phase+"_day")
				prompt := "Pick FROM date:"
				if phase == "to" {
					prompt = "Pick TO date:"
				}
				safeSendToChat(notifier, chatID, prompt, customCumProfitCalendarKeyboard(phase, year, month))
				return
			case "day":
				dt, ok := parseCalendarDayToken(payload)
				if !ok {
					safeSendToChat(notifier, chatID, "Invalid calendar day.", chartsKeyboard())
					return
				}
				if dt.After(time.Now().UTC()) {
					safeSendToChat(notifier, chatID, "Date cannot be in the future.", chartsKeyboard())
					return
				}
				if phase == "from" {
					setCalendarRangeFromDate(chatID, dt)
					setCalendarRangePhase(chatID, "from_hour")
					safeSendToChat(notifier, chatID, fmt.Sprintf("FROM date %s selected. Pick FROM hour:", dt.Format("2006-01-02")), customCumProfitHourKeyboard("from"))
				} else {
					setCalendarRangeToDate(chatID, dt)
					setCalendarRangePhase(chatID, "to_hour")
					safeSendToChat(notifier, chatID, fmt.Sprintf("TO date %s selected. Pick TO hour:", dt.Format("2006-01-02")), customCumProfitHourKeyboard("to"))
				}
				return
			case "hour":
				hour, err := strconv.Atoi(payload)
				if err != nil || hour < 0 || hour > 23 {
					safeSendToChat(notifier, chatID, "Invalid hour.", chartsKeyboard())
					return
				}
				st, ok := getCalendarRangeState(chatID)
				if !ok {
					safeSendToChat(notifier, chatID, "Calendar session expired. Start again.", chartsKeyboard())
					return
				}
				if phase == "from" {
					if st.From.IsZero() {
						safeSendToChat(notifier, chatID, "Choose FROM date first.", chartsKeyboard())
						return
					}
					from := time.Date(st.From.Year(), st.From.Month(), st.From.Day(), hour, 0, 0, 0, time.UTC)
					if from.After(time.Now().UTC()) {
						safeSendToChat(notifier, chatID, "FROM datetime cannot be in the future.", chartsKeyboard())
						return
					}
					setCalendarRangeFromDate(chatID, from)
					setCalendarRangePhase(chatID, "to_day")
					safeSendToChat(notifier, chatID, fmt.Sprintf("FROM set to %s UTC. Now pick TO date:", from.Format("2006-01-02 15:04")), customCumProfitCalendarKeyboard("to", from.Year(), from.Month()))
				} else {
					if st.To.IsZero() || st.From.IsZero() {
						safeSendToChat(notifier, chatID, "Choose TO date first.", chartsKeyboard())
						return
					}
					to := time.Date(st.To.Year(), st.To.Month(), st.To.Day(), hour, 0, 0, 0, time.UTC)
					if to.After(time.Now().UTC()) {
						safeSendToChat(notifier, chatID, "TO datetime cannot be in the future.", chartsKeyboard())
						return
					}
					if !to.After(st.From) {
						safeSendToChat(notifier, chatID, "Invalid TO range. FROM must be older than TO.", customCumProfitCalendarKeyboard("to", st.From.Year(), st.From.Month()))
						return
					}
					clearCalendarRangeState(chatID)
					safeSendToChat(
						notifier,
						chatID,
						fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", st.From.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")),
						customCumProfitDateRangeGranularityKeyboard(st.From.Unix(), to.Unix()),
					)
				}
				return
			default:
				safeSendToChat(notifier, chatID, "Invalid calendar action.", chartsKeyboard())
				return
			}
		}
		if strings.HasPrefix(data, "cprf_") {
			fromToken := strings.TrimPrefix(data, "cprf_")
			fromAgo, fromLabel, ok := parseRangeAgoToken(fromToken)
			if !ok {
				safeSendToChat(notifier, chatID, "Invalid FROM range.", chartsKeyboard())
				return
			}
			setRangeFromSelection(chatID, fromToken)
			safeSendToChat(notifier, chatID, fmt.Sprintf("From set: %s ago. Choose TO:", fromLabel), customCumProfitRangeToKeyboard(fromToken))
			_ = fromAgo
			return
		}
		if strings.HasPrefix(data, "cprt_") {
			toToken := strings.TrimPrefix(data, "cprt_")
			fromToken, okFrom := getRangeFromSelection(chatID)
			if !okFrom {
				safeSendToChat(notifier, chatID, "Please choose FROM first.", customCumProfitRangeFromKeyboard())
				return
			}
			fromAgo, fromLabel, okA := parseRangeAgoToken(fromToken)
			toAgo, toLabel, okB := parseRangeAgoToken(toToken)
			if !okA || !okB || fromAgo <= toAgo {
				safeSendToChat(notifier, chatID, "Invalid TO range. FROM must be older than TO.", customCumProfitRangeToKeyboard(fromToken))
				return
			}
			safeSendToChat(notifier, chatID, fmt.Sprintf("Range: %s ago -> %s ago. Choose timeline:", fromLabel, toLabel), customCumProfitRangeGranularityKeyboard(fromToken, toToken))
			return
		}
			if strings.HasPrefix(data, "cprg_") {
			rest := strings.TrimPrefix(data, "cprg_")
			parts := strings.Split(rest, "_")
			if len(parts) != 3 {
				safeSendToChat(notifier, chatID, "Invalid range chart selection.", chartsKeyboard())
				return
			}
			fromAgo, fromLabel, okA := parseRangeAgoToken(parts[0])
			toAgo, toLabel, okB := parseRangeAgoToken(parts[1])
			if !okA || !okB || fromAgo <= toAgo {
				safeSendToChat(notifier, chatID, "Invalid range. FROM must be older than TO.", customCumProfitRangeFromKeyboard())
				return
			}
			mode, modeLabel := parseCumProfitGranularity(parts[2])
				labels, values, unit := cumulativeProfitSeriesRangeMode(ctx, cfg, state, binance, fromAgo, toAgo, mode)
				if len(labels) == 0 {
					safeSendToChat(notifier, chatID, "No cumulative profit data in this range yet.", chartsKeyboard())
					return
				}
				now := time.Now().UTC()
				from := now.Add(-fromAgo)
				to := now.Add(-toAgo)
				state.addCustomRange(from, to)
				_ = state.save()
				clearRangeFromSelection(chatID)
				title := fmt.Sprintf("Cumulative Profit (%s ago -> %s ago, %s)", fromLabel, toLabel, modeLabel)
				chartURL := buildCumulativeProfitChartURL(title, labels, values, unit)
				safeSendPhotoToChat(notifier, chatID, chartURL, title)
				return
			}
			if strings.HasPrefix(data, "cprh_") {
				rest := strings.TrimPrefix(data, "cprh_")
				parts := strings.Split(rest, "_")
				if len(parts) != 2 {
					safeSendToChat(notifier, chatID, "Invalid range history item.", chartsKeyboard())
					return
				}
				fromSec, errA := strconv.ParseInt(parts[0], 10, 64)
				toSec, errB := strconv.ParseInt(parts[1], 10, 64)
				if errA != nil || errB != nil {
					safeSendToChat(notifier, chatID, "Invalid range history value.", chartsKeyboard())
					return
				}
				from := time.Unix(fromSec, 0).UTC()
				to := time.Unix(toSec, 0).UTC()
				if !to.After(from) {
					safeSendToChat(notifier, chatID, "Invalid history range.", chartsKeyboard())
					return
				}
				safeSendToChat(
					notifier,
					chatID,
					fmt.Sprintf("Range: %s -> %s UTC. Choose timeline:", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04")),
					customCumProfitDateRangeGranularityKeyboard(from.Unix(), to.Unix()),
				)
				return
			}
			if strings.HasPrefix(data, "cpdtg_") {
			rest := strings.TrimPrefix(data, "cpdtg_")
			parts := strings.Split(rest, "_")
			if len(parts) != 3 {
				safeSendToChat(notifier, chatID, "Invalid date range chart selection.", chartsKeyboard())
				return
			}
			fromSec, errA := strconv.ParseInt(parts[0], 10, 64)
			toSec, errB := strconv.ParseInt(parts[1], 10, 64)
			if errA != nil || errB != nil {
				safeSendToChat(notifier, chatID, "Invalid date range values.", chartsKeyboard())
				return
			}
			from := time.Unix(fromSec, 0).UTC()
			to := time.Unix(toSec, 0).UTC()
			if !to.After(from) {
				safeSendToChat(notifier, chatID, "Invalid range: TO must be after FROM.", chartsKeyboard())
				return
			}
			mode, modeLabel := parseCumProfitGranularity(parts[2])
				labels, values, unit := cumulativeProfitSeriesBetweenMode(ctx, cfg, state, binance, from, to, mode)
				if len(labels) == 0 {
					safeSendToChat(notifier, chatID, "No cumulative profit data in this date range.", chartsKeyboard())
					return
				}
				state.addCustomRange(from, to)
				_ = state.save()
				title := fmt.Sprintf("Cumulative Profit (%s -> %s, %s)", from.Format("2006-01-02 15:04"), to.Format("2006-01-02 15:04"), modeLabel)
			chartURL := buildCumulativeProfitChartURL(title, labels, values, unit)
			safeSendPhotoToChat(notifier, chatID, chartURL, title)
			return
		}
		safeSendToChat(notifier, chatID, "Unknown action", defaultKeyboard())
	}
}

func defaultKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Status", CallbackData: "status"}, {Text: "Actions", CallbackData: "menu_actions"}},
			{{Text: "Reports", CallbackData: "menu_reports"}, {Text: "Charts", CallbackData: "menu_charts"}},
			{{Text: "Settings", CallbackData: "menu_settings"}},
		},
	}
}

func actionsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Refill Now", CallbackData: "refill_now"}, {Text: "Force Buy BNB", CallbackData: "force_buy"}},
			{{Text: "Daily Report Now", CallbackData: "daily_report_now"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func reportsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Daily", CallbackData: "report_day"}, {Text: "Weekly", CallbackData: "report_week"}, {Text: "Monthly", CallbackData: "report_month"}},
			{{Text: "Fees D", CallbackData: "fees_day"}, {Text: "Fees W", CallbackData: "fees_week"}, {Text: "Fees M", CallbackData: "fees_month"}},
			{{Text: "PnL D", CallbackData: "pnl_day"}, {Text: "PnL W", CallbackData: "pnl_week"}, {Text: "PnL M", CallbackData: "pnl_month"}},
			{{Text: "Trades D", CallbackData: "trades_day"}, {Text: "Trades W", CallbackData: "trades_week"}, {Text: "Trades M", CallbackData: "trades_month"}},
			{{Text: "Leaders D", CallbackData: "leaders_day"}, {Text: "Leaders W", CallbackData: "leaders_week"}, {Text: "Leaders M", CallbackData: "leaders_month"}},
			{{Text: "PnL 7d Table", CallbackData: "pnl_7d_table"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func chartsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Fees Chart", CallbackData: "chart_fees"}, {Text: "PnL Chart", CallbackData: "chart_pnl"}},
			{{Text: "Cum Fees 24h", CallbackData: "chart_cum_fees_day"}, {Text: "Cum Fees 7d", CallbackData: "chart_cum_fees_week"}, {Text: "Cum Fees 30d", CallbackData: "chart_cum_fees_month"}},
			{{Text: "Cum Profit 24h", CallbackData: "chart_cum_profit_day"}, {Text: "Cum Profit 48h", CallbackData: "chart_cum_profit_48h"}, {Text: "Cum Profit 72h", CallbackData: "chart_cum_profit_72h"}},
			{{Text: "Cum Profit 7d", CallbackData: "chart_cum_profit_week"}, {Text: "Cum Profit 30d", CallbackData: "chart_cum_profit_month"}},
			{{Text: "Cum Profit Custom", CallbackData: "chart_cum_profit_custom"}, {Text: "Custom History", CallbackData: "chart_cum_profit_custom_history"}},
			{{Text: "Range From->To", CallbackData: "chart_cum_profit_range"}},
			{{Text: "Range Date&Hour", CallbackData: "chart_cum_profit_date_range"}, {Text: "Calendar Range", CallbackData: "chart_cum_profit_calendar_range"}},
			{{Text: "Range History", CallbackData: "chart_cum_profit_range_history"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func customCumProfitWindowKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "24h", CallbackData: "ccpw_24h"}, {Text: "48h", CallbackData: "ccpw_48h"}, {Text: "72h", CallbackData: "ccpw_72h"}},
			{{Text: "3d", CallbackData: "ccpw_3d"}, {Text: "5d", CallbackData: "ccpw_5d"}, {Text: "7d", CallbackData: "ccpw_7d"}},
			{{Text: "14d", CallbackData: "ccpw_14d"}, {Text: "30d", CallbackData: "ccpw_30d"}},
			{{Text: "History", CallbackData: "chart_cum_profit_custom_history"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitGranularityKeyboard(windowToken string) *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Hours", CallbackData: "ccpg_" + windowToken + "_h"}, {Text: "Days", CallbackData: "ccpg_" + windowToken + "_d"}, {Text: "Auto", CallbackData: "ccpg_" + windowToken + "_a"}},
			{{Text: "Window", CallbackData: "chart_cum_profit_custom"}, {Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitHistoryKeyboard(tokens []string) *inlineKeyboardMarkup {
	rows := make([][]inlineKeyboardButton, 0, 8)
	for i := 0; i < len(tokens); i += 3 {
		row := make([]inlineKeyboardButton, 0, 3)
		for j := i; j < i+3 && j < len(tokens); j++ {
			t := strings.ToLower(strings.TrimSpace(tokens[j]))
			if t == "" {
				continue
			}
			row = append(row, inlineKeyboardButton{Text: t, CallbackData: "ccpw_" + t})
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Custom Input", CallbackData: "chart_cum_profit_custom"},
		{Text: "Back", CallbackData: "menu_charts"},
	})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitRangeFromKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "24h ago", CallbackData: "cprf_24h"}, {Text: "48h ago", CallbackData: "cprf_48h"}, {Text: "72h ago", CallbackData: "cprf_72h"}},
			{{Text: "7d ago", CallbackData: "cprf_7d"}, {Text: "14d ago", CallbackData: "cprf_14d"}, {Text: "30d ago", CallbackData: "cprf_30d"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitRangeToKeyboard(fromToken string) *inlineKeyboardMarkup {
	rows := [][]inlineKeyboardButton{
		{{Text: "now", CallbackData: "cprt_now"}, {Text: "12h ago", CallbackData: "cprt_12h"}, {Text: "24h ago", CallbackData: "cprt_24h"}},
		{{Text: "48h ago", CallbackData: "cprt_48h"}, {Text: "72h ago", CallbackData: "cprt_72h"}, {Text: "7d ago", CallbackData: "cprt_7d"}},
		{{Text: "From", CallbackData: "chart_cum_profit_range"}, {Text: "Back", CallbackData: "menu_charts"}},
	}
	_ = fromToken
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitRangeGranularityKeyboard(fromToken, toToken string) *inlineKeyboardMarkup {
	prefix := "cprg_" + fromToken + "_" + toToken + "_"
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Hours", CallbackData: prefix + "h"}, {Text: "Days", CallbackData: prefix + "d"}, {Text: "Auto", CallbackData: prefix + "a"}},
			{{Text: "To", CallbackData: "cprf_" + fromToken}, {Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitDateRangeGranularityKeyboard(fromTS, toTS int64) *inlineKeyboardMarkup {
	prefix := fmt.Sprintf("cpdtg_%d_%d_", fromTS, toTS)
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Hours", CallbackData: prefix + "h"}, {Text: "Days", CallbackData: prefix + "d"}, {Text: "Auto", CallbackData: prefix + "a"}},
			{{Text: "New Date Range", CallbackData: "chart_cum_profit_date_range"}, {Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitDateRangeEntryKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Type Manually", CallbackData: "chart_cum_profit_date_range_manual"}},
			{{Text: "Open Calendar", CallbackData: "chart_cum_profit_calendar_range"}},
			{{Text: "Range History", CallbackData: "chart_cum_profit_range_history"}},
			{{Text: "Back", CallbackData: "menu_charts"}},
		},
	}
}

func customCumProfitRangeHistoryKeyboard(history []rangeRecord) *inlineKeyboardMarkup {
	rows := make([][]inlineKeyboardButton, 0, len(history)+1)
	for _, h := range history {
		from := time.Unix(h.FromTS, 0).UTC()
		to := time.Unix(h.ToTS, 0).UTC()
		if !to.After(from) {
			continue
		}
		label := fmt.Sprintf("%s -> %s", from.Format("01-02 15:04"), to.Format("01-02 15:04"))
		cb := fmt.Sprintf("cprh_%d_%d", h.FromTS, h.ToTS)
		rows = append(rows, []inlineKeyboardButton{{Text: label, CallbackData: cb}})
	}
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Back", CallbackData: "menu_charts"},
	})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitCalendarKeyboard(phase string, year int, month time.Month) *inlineKeyboardMarkup {
	if phase != "from" && phase != "to" {
		phase = "from"
	}
	rows := make([][]inlineKeyboardButton, 0, 10)
	title := fmt.Sprintf("%s %04d", month.String(), year)
	rows = append(rows, []inlineKeyboardButton{{Text: title, CallbackData: "ccal_ignore"}})
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Mo", CallbackData: "ccal_ignore"},
		{Text: "Tu", CallbackData: "ccal_ignore"},
		{Text: "We", CallbackData: "ccal_ignore"},
		{Text: "Th", CallbackData: "ccal_ignore"},
		{Text: "Fr", CallbackData: "ccal_ignore"},
		{Text: "Sa", CallbackData: "ccal_ignore"},
		{Text: "Su", CallbackData: "ccal_ignore"},
	})

	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	daysInMonth := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	weekday := int(first.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	offset := weekday - 1 // monday-based

	day := 1
	for rowIdx := 0; rowIdx < 6 && day <= daysInMonth; rowIdx++ {
		row := make([]inlineKeyboardButton, 0, 7)
		for col := 0; col < 7; col++ {
			if rowIdx == 0 && col < offset {
				row = append(row, inlineKeyboardButton{Text: " ", CallbackData: "ccal_ignore"})
				continue
			}
			if day > daysInMonth {
				row = append(row, inlineKeyboardButton{Text: " ", CallbackData: "ccal_ignore"})
				continue
			}
			dateToken := fmt.Sprintf("%04d%02d%02d", year, int(month), day)
			row = append(row, inlineKeyboardButton{
				Text:         strconv.Itoa(day),
				CallbackData: "ccal_" + phase + "_day_" + dateToken,
			})
			day++
		}
		rows = append(rows, row)
	}

	prev := first.AddDate(0, -1, 0)
	next := first.AddDate(0, 1, 0)
	rows = append(rows, []inlineKeyboardButton{
		{Text: "<", CallbackData: fmt.Sprintf("ccal_%s_nav_%04d%02d", phase, prev.Year(), int(prev.Month()))},
		{Text: ">", CallbackData: fmt.Sprintf("ccal_%s_nav_%04d%02d", phase, next.Year(), int(next.Month()))},
	})
	rows = append(rows, []inlineKeyboardButton{
		{Text: "Back", CallbackData: "menu_charts"},
	})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func customCumProfitHourKeyboard(phase string) *inlineKeyboardMarkup {
	if phase != "from" && phase != "to" {
		phase = "from"
	}
	rows := make([][]inlineKeyboardButton, 0, 8)
	for i := 0; i < 24; i += 6 {
		row := make([]inlineKeyboardButton, 0, 6)
		for j := 0; j < 6; j++ {
			h := i + j
			row = append(row, inlineKeyboardButton{
				Text:         fmt.Sprintf("%02d:00", h),
				CallbackData: fmt.Sprintf("ccal_%s_hour_%02d", phase, h),
			})
		}
		rows = append(rows, row)
	}
	rows = append(rows, []inlineKeyboardButton{{Text: "Back", CallbackData: "chart_cum_profit_calendar_range"}})
	return &inlineKeyboardMarkup{InlineKeyboard: rows}
}

func settingsKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Currency", CallbackData: "fee_currency_menu"}},
			{{Text: "Freqtrade Health", CallbackData: "freqtrade_health"}},
			{{Text: "Back", CallbackData: "menu_main"}},
		},
	}
}

func defaultReplyKeyboard() *replyKeyboardMarkup {
	return &replyKeyboardMarkup{
		Keyboard: [][]keyboardButton{
			{{Text: "Status"}, {Text: "Daily Report"}},
			{{Text: "Menu"}, {Text: "Help"}},
		},
		ResizeKeyboard: true,
	}
}

func forceBuyConfirmKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "Confirm Force Buy", CallbackData: "force_buy_confirm"}, {Text: "Cancel", CallbackData: "force_buy_cancel"}},
			{{Text: "Menu", CallbackData: "menu"}},
		},
	}
}

func feeCurrencyKeyboard() *inlineKeyboardMarkup {
	return &inlineKeyboardMarkup{
		InlineKeyboard: [][]inlineKeyboardButton{
			{{Text: "BNB", CallbackData: "fee_currency_bnb"}, {Text: "USDT", CallbackData: "fee_currency_usdt"}},
			{{Text: "Back", CallbackData: "menu_settings"}},
		},
	}
}

func helpText() string {
	return strings.Join([]string{
		"BNB Fees Monitor - Help",
		"",
		"Commands:",
		"/start or /menu - open menu",
		"/status - snapshot (balance, fees, pnl, system, watchdog)",
		"/daily - full daily report + charts",
		"/help - this help",
		"",
		"Menu sections:",
		"Actions: Refill Now, Force Buy BNB, Daily Report Now",
		"Reports: day/week/month for Report, Fees, PnL, Trades, Leaders, plus PnL 7d table",
		"Charts: fees, pnl, cumulative fees/profit, custom windows, range tools",
		"Settings: display currency (BNB/USDT), Freqtrade Health",
		"",
		"Chart options:",
		"Cum Profit presets: 24h, 48h, 72h, 7d, 30d",
		"Cum Profit Custom: choose preset buttons or type window (examples: 36h, 3d, 10d)",
		"Custom History: re-use previous custom windows",
		"Range From->To: relative range picker (from/to ago) + timeline",
		"Range Date&Hour: exact UTC datetime input for from/to",
		"Calendar Range: pick FROM/TO date via calendar + hour buttons",
		"Range History: re-use last 5 saved ranges",
		"Timeline mode: Hours / Days / Auto",
		"",
		"Date/time format (manual range):",
		"Use UTC. Accepted: YYYY-MM-DD HH:MM, YYYY-MM-DD HH, YYYY-MM-DD",
		"Example: 2026-03-07 14:30",
		"Type 'cancel' or 'back' to stop custom/date input flows",
		"",
		"Reliability / health:",
		"Freqtrade Health shows API checks + dashboard",
		"Watchdog tracks stale heartbeat and recovery/restart info",
		"",
		"Notes:",
		"Display currency affects reports/charts values (BNB or USDT)",
		"Some range/history features need prior usage before history appears",
	}, "\n")
}

func normalizeCommand(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "status", "/status":
		return "/status"
	case "daily report", "/daily":
		return "/daily"
	case "menu", "/menu", "/start":
		return "/menu"
	case "help", "/help":
		return "/help"
	default:
		return s
	}
}

func heartbeatLoop(ctx context.Context, cfg Config, alerts *alertManager) {
	interval := cfg.HeartbeatCheckInterval
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			alerts.checkHeartbeatStale()
			if strings.TrimSpace(cfg.HeartbeatPingURL) == "" {
				continue
			}
			if err := pingHeartbeatURL(ctx, cfg.HeartbeatPingURL); err != nil {
				alerts.observeAPICall("heartbeat.ping", 0, err)
			} else {
				alerts.observeAPICall("heartbeat.ping", 0, nil)
			}
		}
	}
}

func pingHeartbeatURL(ctx context.Context, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(rawURL), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 12 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("heartbeat ping http=%d body=%s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func checkAbnormalMoveAlerts(cfg Config, state *MonitorState, alerts *alertManager) {
	if alerts == nil || !cfg.AbnormalMoveAlertEnabled {
		return
	}
	if cfg.AbnormalMoveDrop1hPct > 0 {
		pnl, pct, ok := state.pnlSince(time.Hour)
		if ok && pct <= -cfg.AbnormalMoveDrop1hPct {
			alerts.sendDedup(
				"move_drop_1h",
				cfg.AbnormalMoveAlertCooldown,
				fmt.Sprintf("Abnormal move alert: 1h portfolio change %.2f%% (%.4f %s)", pct, pnl, cfg.QuoteAsset),
			)
		}
	}
	if cfg.AbnormalMoveDrop24hPct > 0 {
		pnl, pct, ok := state.pnlSince(24 * time.Hour)
		if ok && pct <= -cfg.AbnormalMoveDrop24hPct {
			alerts.sendDedup(
				"move_drop_24h",
				cfg.AbnormalMoveAlertCooldown,
				fmt.Sprintf("Abnormal move alert: 24h portfolio change %.2f%% (%.4f %s)", pct, pnl, cfg.QuoteAsset),
			)
		}
	}
}

func dailyReportLoop(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState) {
	if !cfg.DailyReportEnabled {
		log.Print("daily report loop disabled")
		return
	}
	hour, minute, err := parseHHMM(cfg.DailyReportTimeUTC)
	if err != nil {
		log.Printf("daily report time parse error (%s): %v; fallback 00:05", cfg.DailyReportTimeUTC, err)
		hour, minute = 0, 5
	}

	for {
		loc, tzName := resolveDailyTimezone(ctx, cfg, runtimeAlerts)
		nextRun := nextDailyRun(time.Now().In(loc), hour, minute, loc)
		wait := time.Until(nextRun)
		if wait < 0 {
			wait = 10 * time.Second
		}
		log.Printf("next daily report scheduled at %s (%s)", nextRun.Format(time.RFC3339), tzName)

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := sendDailyReport(ctx, cfg, binance, notifier, state); err != nil {
			log.Printf("daily report error: %v", err)
			if runtimeAlerts != nil {
				runtimeAlerts.recordError("daily.report", err)
			}
			safeSend(notifier, fmt.Sprintf("Daily report failed: %v", err), defaultKeyboard())
		}
	}
}

func sendDailyReport(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState) error {
	started := time.Now()
	defer logTiming("send_daily_report", started)
	mode := strings.ToLower(strings.TrimSpace(cfg.DailyReportMode))
	if mode == "" {
		mode = "full"
	}
	var (
		report string
		err    error
	)
	if mode == "digest" {
		report, err = buildDailyDigest(ctx, cfg, binance, state, runtimeAlerts)
	} else {
		report, err = buildDailyReport(ctx, cfg, binance, state)
	}
	if err != nil {
		return err
	}
	if err := notifier.Send(report, defaultKeyboard()); err != nil {
		return err
	}
	if mode == "digest" {
		return nil
	}

	feeLabels, feeVals, err := feeSeriesLastNDaysCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, 30)
	if err != nil {
		log.Printf("daily fee chart generation error: %v", err)
	} else if len(feeLabels) > 0 {
		chartURL := buildLineChartURL("BNB Fees (Last 30 Days)", feeLabels, feeVals, cfg.BNBAsset)
		safeSendPhoto(notifier, chartURL, "Daily Report: Fees (30d)")
	}

	portLabels, portVals := state.portfolioSeriesLastNDays(30)
	if len(portLabels) > 0 {
		chartURL := buildLineChartURL("Portfolio Value (Last 30 Days)", portLabels, portVals, cfg.QuoteAsset)
		safeSendPhoto(notifier, chartURL, "Daily Report: Portfolio (30d)")
	}

	pnlLabels, pnlVals := state.pnlSeriesLastNDays(30)
	if len(pnlLabels) > 0 {
		chartURL := buildLineChartURL("PnL Delta (Last 30 Days)", pnlLabels, pnlVals, cfg.QuoteAsset)
		safeSendPhoto(notifier, chartURL, "Daily Report: PnL Delta (30d)")
	}
	return nil
}

func sendPeriodReport(ctx context.Context, cfg Config, binance *BinanceClient, notifier *TelegramNotifier, state *MonitorState, d time.Duration, label string) error {
	report, err := buildPeriodReport(ctx, cfg, binance, state, d, label)
	if err != nil {
		return err
	}
	if err := notifier.Send(report, defaultKeyboard()); err != nil {
		return err
	}
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			logIfErr("period.freqtrade.fetch_trades", err)
			return nil
		}
		var feeLabels []string
		var feeVals []float64
		var pnlLabels []string
		var pnlVals []float64
		switch label {
		case "day":
			feeLabels, feeVals = freqtradeFeeSeriesByHour(trades, cfg.BNBAsset, 24)
			pnlLabels, pnlVals = freqtradePnlSeriesByHour(trades, 24)
		case "week":
			feeLabels, feeVals = freqtradeFeeSeriesByDay(trades, cfg.BNBAsset, 7)
			pnlLabels, pnlVals = freqtradePnlSeriesByDay(trades, 7)
		default:
			feeLabels, feeVals = freqtradeFeeSeriesByDay(trades, cfg.BNBAsset, 30)
			pnlLabels, pnlVals = freqtradePnlSeriesByDay(trades, 30)
		}
		if len(feeLabels) > 0 {
			chartURL := buildLineChartURL(fmt.Sprintf("Fees (%s)", strings.Title(label)), feeLabels, feeVals, cfg.BNBAsset)
			safeSendPhoto(notifier, chartURL, fmt.Sprintf("Fees chart (%s)", label))
		}
		if len(pnlLabels) > 0 {
			chartURL := buildLineChartURL(fmt.Sprintf("PnL (%s)", strings.Title(label)), pnlLabels, pnlVals, cfg.QuoteAsset)
			safeSendPhoto(notifier, chartURL, fmt.Sprintf("PnL chart (%s)", label))
		}
	}
	return nil
}

func buildDailyDigest(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState, alerts *alertManager) (string, error) {
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}
	fees, err := getFeeSummaryCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset)
	if err != nil {
		return "", err
	}
	pnlDay, pctDay, okDay := state.pnlSince(24 * time.Hour)
	pnlWeek, pctWeek, okWeek := state.pnlSince(7 * 24 * time.Hour)
	if cfg.FreqtradeHistoryMode {
		trades, ftErr := getFreqtradeTrades30dCached(ctx, cfg)
		if ftErr == nil {
			pnlDay, pctDay, okDay = freqtradePnlSince(trades, time.Now().UTC().Add(-24*time.Hour))
			pnlWeek, pctWeek, okWeek = freqtradePnlSince(trades, time.Now().UTC().Add(-7*24*time.Hour))
		}
	}

	lastTrades := buildLastTradesDigest(ctx, cfg, binance, state)
	errors24h := alerts.recentErrorsSince(24*time.Hour, 3)
	errLines := "none"
	if len(errors24h) > 0 {
		var b strings.Builder
		for i := 0; i < len(errors24h); i++ {
			ev := errors24h[i]
			if i > 0 {
				b.WriteString(" | ")
			}
			b.WriteString(fmt.Sprintf("%s %s: %s", ev.TS.Format("15:04"), ev.Source, compactErr(ev.Err, 90)))
		}
		errLines = b.String()
	}

	pnlDayLine := "n/a"
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
	if okDay {
		pnlDayLine = fmt.Sprintf("%s (%.2f%%)", formatQuoteByDisplay(pnlDay, cfg, displayCurrency, spot), pctDay)
	}
	pnlWeekLine := "n/a"
	if okWeek {
		pnlWeekLine = fmt.Sprintf("%s (%.2f%%)", formatQuoteByDisplay(pnlWeek, cfg, displayCurrency, spot), pctWeek)
	}
	dayFeeText := formatFeeByMainCurrency(fees.Day, cfg, displayCurrency, spot)
	weekFeeText := formatFeeByMainCurrency(fees.Week, cfg, displayCurrency, spot)
	monthFeeText := formatFeeByMainCurrency(fees.Month, cfg, displayCurrency, spot)
	quoteFreeText := formatQuoteByDisplay(quoteFree, cfg, displayCurrency, spot)
	portfolioText := formatQuoteByDisplay(portfolioQuote, cfg, displayCurrency, spot)

	return fmt.Sprintf(
		"Daily Digest (%s UTC)\nBalance: BNB %.6f | %s | Portfolio %s\nPrice %s: %.4f\nFees: D %s | W %s | M %s\nPnL: D %s | W %s\nLast trades: %s\nErrors(24h): %s",
		time.Now().UTC().Format("2006-01-02 15:04"),
		bnbFree,
		quoteFreeText,
		portfolioText,
		cfg.Symbol,
		price,
		dayFeeText,
		weekFeeText,
		monthFeeText,
		pnlDayLine,
		pnlWeekLine,
		lastTrades,
		errLines,
	), nil
}

func buildLastTradesDigest(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState) string {
	n := cfg.DailyDigestTrades
	if n <= 0 {
		n = 3
	}
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return "n/a (" + compactErr(err.Error(), 80) + ")"
		}
		sort.Slice(trades, func(i, j int) bool {
			return freqtradeTradeLatestTS(trades[i]) > freqtradeTradeLatestTS(trades[j])
		})
		lines := make([]string, 0, n)
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
		for _, tr := range trades {
			ts := freqtradeTradeLatestTS(tr)
			if ts <= 0 {
				continue
			}
			lines = append(lines, fmt.Sprintf("%s %s", normalizePairToSymbol(tr.Pair), formatQuoteByDisplay(tr.ProfitAbs, cfg, displayCurrency, spot)))
			if len(lines) >= n {
				break
			}
		}
		if len(lines) == 0 {
			return "none"
		}
		return strings.Join(lines, " | ")
	}

	trades, err := collectTradesByDuration(ctx, binance, cfg.TrackedSymbols, 24*time.Hour)
	if err != nil {
		return "n/a (" + compactErr(err.Error(), 80) + ")"
	}
	if len(trades) == 0 {
		return "none"
	}
	if len(trades) > n {
		trades = trades[:n]
	}
	lines := make([]string, 0, len(trades))
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
	for _, tr := range trades {
		side := "SELL"
		if tr.IsBuyer {
			side = "BUY"
		}
		qv, _ := strconv.ParseFloat(strings.TrimSpace(tr.QuoteQty), 64)
		lines = append(lines, fmt.Sprintf("%s %s %s", side, tr.Symbol, formatQuoteByDisplay(qv, cfg, displayCurrency, spot)))
	}
	return strings.Join(lines, " | ")
}

func resolveDailyTimezone(ctx context.Context, cfg Config, alerts *alertManager) (*time.Location, string) {
	raw := strings.TrimSpace(cfg.DailyReportTimezone)
	if raw == "" {
		return time.UTC, "UTC"
	}
	if strings.EqualFold(raw, "AUTO") || strings.EqualFold(raw, "AUTO_IP") {
		tzName, err := fetchNetworkTimezone(ctx)
		if err != nil {
			if alerts != nil {
				alerts.recordError("timezone.auto", err)
				alerts.sendDedup("timezone.auto.error", time.Hour, fmt.Sprintf("Daily timezone auto-detect failed; using UTC (%v)", err))
			}
			return time.UTC, "UTC"
		}
		loc, err := time.LoadLocation(tzName)
		if err != nil {
			if alerts != nil {
				alerts.recordError("timezone.load", err)
			}
			return time.UTC, "UTC"
		}
		return loc, tzName
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		if alerts != nil {
			alerts.recordError("timezone.load", err)
		}
		log.Printf("daily report timezone error (%s): %v; fallback UTC", raw, err)
		return time.UTC, "UTC"
	}
	return loc, raw
}

func fetchNetworkTimezone(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://worldtimeapi.org/api/ip", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("worldtimeapi http=%d body=%s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Timezone) == "" {
		return "", errors.New("empty timezone from worldtimeapi")
	}
	return payload.Timezone, nil
}

type pnlWindowSnapshot struct {
	dayPnl  float64
	dayPct  float64
	dayOK   bool
	weekPnl float64
	weekPct float64
	weekOK  bool
	monPnl  float64
	monPct  float64
	monOK   bool
}

func resolvePnlWindowSnapshot(ctx context.Context, cfg Config, state *MonitorState) (pnlWindowSnapshot, error) {
	if !cfg.FreqtradeHistoryMode {
		dayPnl, dayPct, dayOK := state.pnlSince(24 * time.Hour)
		weekPnl, weekPct, weekOK := state.pnlSince(7 * 24 * time.Hour)
		monPnl, monPct, monOK := state.pnlSince(30 * 24 * time.Hour)
		return pnlWindowSnapshot{
			dayPnl:  dayPnl,
			dayPct:  dayPct,
			dayOK:   dayOK,
			weekPnl: weekPnl,
			weekPct: weekPct,
			weekOK:  weekOK,
			monPnl:  monPnl,
			monPct:  monPct,
			monOK:   monOK,
		}, nil
	}

	trades, err := getFreqtradeTrades30dCached(ctx, cfg)
	if err != nil {
		return pnlWindowSnapshot{}, err
	}
	dayPnl, dayPct, dayOK := freqtradePnlSince(trades, time.Now().UTC().Add(-24*time.Hour))
	weekPnl, weekPct, weekOK := freqtradePnlSince(trades, time.Now().UTC().Add(-7*24*time.Hour))
	monPnl, monPct, monOK := freqtradePnlSince(trades, time.Now().UTC().Add(-30*24*time.Hour))
	return pnlWindowSnapshot{
		dayPnl:  dayPnl,
		dayPct:  dayPct,
		dayOK:   dayOK,
		weekPnl: weekPnl,
		weekPct: weekPct,
		weekOK:  weekOK,
		monPnl:  monPnl,
		monPct:  monPct,
		monOK:   monOK,
	}, nil
}

func buildDailyReport(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState) (string, error) {
	started := time.Now()
	defer logTiming("build_daily_report", started)
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}
	pnlSnap, err := resolvePnlWindowSnapshot(ctx, cfg, state)
	if err != nil {
		return "", err
	}

	fees, err := getFeeSummaryCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset)
	if err != nil {
		return "", err
	}

	refillD := state.refillStatsSince(24 * time.Hour)
	refillW := state.refillStatsSince(7 * 24 * time.Hour)
	refillM := state.refillStatsSince(30 * 24 * time.Hour)

	pnlLine := func(label string, ok bool, pnl, pct float64) string {
		if !ok {
			return fmt.Sprintf("%s: n/a", label)
		}
		displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
		spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
		return fmt.Sprintf("%s: %s (%.2f%%)", label, formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct)
	}
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)
	dayFeeText := formatFeeByMainCurrency(fees.Day, cfg, displayCurrency, spot)
	weekFeeText := formatFeeByMainCurrency(fees.Week, cfg, displayCurrency, spot)
	monthFeeText := formatFeeByMainCurrency(fees.Month, cfg, displayCurrency, spot)
	quoteFreeText := formatQuoteByDisplay(quoteFree, cfg, displayCurrency, spot)
	portfolioText := formatQuoteByDisplay(portfolioQuote, cfg, displayCurrency, spot)
	refillDText := formatQuoteByDisplay(refillD.QuoteSpent, cfg, displayCurrency, spot)
	refillWText := formatQuoteByDisplay(refillW.QuoteSpent, cfg, displayCurrency, spot)
	refillMText := formatQuoteByDisplay(refillM.QuoteSpent, cfg, displayCurrency, spot)

	return fmt.Sprintf(
		"Daily Trading Report (%s UTC)\n\nAccount\nBNB: %.6f\n%s: %s\n%s: %.4f\nPortfolio: %s\n\nFees (%s)\nDay: %s\nWeek: %s\nMonth: %s\n\nRefills\nDay: %d orders, spent %s, got %.6f %s\nWeek: %d orders, spent %s, got %.6f %s\nMonth: %d orders, spent %s, got %.6f %s\n\nPnL\n%s\n%s\n%s",
		time.Now().UTC().Format("2006-01-02 15:04:05"),
		bnbFree,
		cfg.QuoteAsset,
		quoteFreeText,
		cfg.Symbol,
		price,
		portfolioText,
		cfg.BNBAsset,
		dayFeeText,
		weekFeeText,
		monthFeeText,
		refillD.Count,
		refillDText,
		refillD.BNBReceived,
		cfg.BNBAsset,
		refillW.Count,
		refillWText,
		refillW.BNBReceived,
		cfg.BNBAsset,
		refillM.Count,
		refillMText,
		refillM.BNBReceived,
		cfg.BNBAsset,
		pnlLine("Day", pnlSnap.dayOK, pnlSnap.dayPnl, pnlSnap.dayPct),
		pnlLine("Week", pnlSnap.weekOK, pnlSnap.weekPnl, pnlSnap.weekPct),
		pnlLine("Month", pnlSnap.monOK, pnlSnap.monPnl, pnlSnap.monPct),
	), nil
}

func buildPeriodReport(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState, d time.Duration, label string) (string, error) {
	started := time.Now()
	defer logTiming("build_period_report_"+label, started)
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}

	fees, err := totalFeesBNBCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, d)
	if err != nil {
		return "", err
	}
	pnl, pct, ok := state.pnlSince(d)
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return "", err
		}
		pnl, pct, ok = freqtradePnlSince(trades, time.Now().UTC().Add(-d))
	}
	refills := state.refillStatsSince(d)

	pnlText := "n/a"
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, d)
	if ok {
		pnlText = fmt.Sprintf("%s (%.2f%%)", formatQuoteByDisplay(pnl, cfg, displayCurrency, spot), pct)
	}
	feeText := formatFeeByMainCurrency(fees, cfg, displayCurrency, spot)
	quoteFreeText := formatQuoteByDisplay(quoteFree, cfg, displayCurrency, spot)
	portfolioText := formatQuoteByDisplay(portfolioQuote, cfg, displayCurrency, spot)
	refillText := formatQuoteByDisplay(refills.QuoteSpent, cfg, displayCurrency, spot)

	return fmt.Sprintf(
		"%s Report (%s UTC)\n\nAccount\nBNB: %.6f\n%s: %s\n%s: %.4f\nPortfolio: %s\n\nWindow Stats\nFees: %s\nRefills: %d orders\nRefill spent: %s\nRefill got: %.6f %s\nPnL: %s",
		strings.Title(label),
		time.Now().UTC().Format("2006-01-02 15:04:05"),
		bnbFree,
		cfg.QuoteAsset,
		quoteFreeText,
		cfg.Symbol,
		price,
		portfolioText,
		feeText,
		refills.Count,
		refillText,
		refills.BNBReceived,
		cfg.BNBAsset,
		pnlText,
	), nil
}

func nextDailyRun(now time.Time, hour, minute int, loc *time.Location) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

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

func buildStatusReport(ctx context.Context, cfg Config, binance *BinanceClient, state *MonitorState) (string, error) {
	started := time.Now()
	defer logTiming("build_status_report", started)
	bnbFree, quoteFree, price, portfolioQuote, err := loadAccountSnapshot(ctx, cfg, binance)
	if err != nil {
		return "", err
	}
	pnlSnap, err := resolvePnlWindowSnapshot(ctx, cfg, state)
	if err != nil {
		return "", err
	}

	fees, ok, err := getFeeSummaryCacheOnly(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset)
	if err != nil {
		logIfErr("fees_summary_cache_only", err)
		fees = feeSummary{}
		ok = false
	}
	if !ok {
		warmFeeSummaryCacheAsync(binance, cfg.TrackedSymbols, cfg.BNBAsset)
	}
	refillD := state.refillStatsSince(24 * time.Hour)
	refillW := state.refillStatsSince(7 * 24 * time.Hour)
	refillM := state.refillStatsSince(30 * 24 * time.Hour)
	mainCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, 24*time.Hour)

	pnlLine := func(label string, ok bool, pnl, pct float64) string {
		if !ok {
			return fmt.Sprintf("%s: n/a", label)
		}
		return fmt.Sprintf("%s: %s (%.2f%%)", label, formatQuoteByDisplay(pnl, cfg, mainCurrency, spot), pct)
	}

	feesLine := fmt.Sprintf(
		"Fees: D=%s W=%s M=%s",
		formatFeeByMainCurrency(fees.Day, cfg, mainCurrency, spot),
		formatFeeByMainCurrency(fees.Week, cfg, mainCurrency, spot),
		formatFeeByMainCurrency(fees.Month, cfg, mainCurrency, spot),
	)
	if !ok {
		feesLine += " (warming cache...)"
	}
	systemLine := buildSystemLine()
	watchdogLine := "Watchdog: n/a"
	if runtimeAlerts != nil {
		watchdogLine = runtimeAlerts.buildWatchdogSummary()
	}

	return fmt.Sprintf(
		"Status\nBNB: %.6f\n%s: %s\n%s: %.4f\nPortfolio: %s\n\n%s\nRefills: D=%d W=%d M=%d\n%s\n%s\n\nPnL\n%s\n%s\n%s",
		bnbFree,
		cfg.QuoteAsset,
		formatQuoteByDisplay(quoteFree, cfg, mainCurrency, spot),
		cfg.Symbol,
		price,
		formatQuoteByDisplay(portfolioQuote, cfg, mainCurrency, spot),
		feesLine,
		refillD.Count,
		refillW.Count,
		refillM.Count,
		systemLine,
		watchdogLine,
		pnlLine("Day", pnlSnap.dayOK, pnlSnap.dayPnl, pnlSnap.dayPct),
		pnlLine("Week", pnlSnap.weekOK, pnlSnap.weekPnl, pnlSnap.weekPct),
		pnlLine("Month", pnlSnap.monOK, pnlSnap.monPnl, pnlSnap.monPct),
	), nil
}

func buildSystemLine() string {
	cpu, cpuOK := readCPUUsagePercent()
	memUsed, memTotal, memPct, memOK := readMemUsage()
	diskUsed, diskTotal, diskPct, diskOK := readDiskUsage("/")

	cpuText := "n/a"
	if cpuOK {
		cpuText = fmt.Sprintf("%.1f%%", cpu)
	}
	memText := "n/a"
	if memOK {
		memText = fmt.Sprintf("%s/%s (%.1f%%)", formatBytes(memUsed), formatBytes(memTotal), memPct)
	}
	diskText := "n/a"
	if diskOK {
		diskText = fmt.Sprintf("%s/%s (%.1f%%)", formatBytes(diskUsed), formatBytes(diskTotal), diskPct)
	}
	return fmt.Sprintf("System: CPU %s | MEM %s | DISK / %s", cpuText, memText, diskText)
}

func readCPUUsagePercent() (float64, bool) {
	idle1, total1, ok := readCPUStat()
	if !ok {
		return 0, false
	}
	time.Sleep(220 * time.Millisecond)
	idle2, total2, ok := readCPUStat()
	if !ok {
		return 0, false
	}
	deltaTotal := float64(total2 - total1)
	deltaIdle := float64(idle2 - idle1)
	if deltaTotal <= 0 {
		return 0, false
	}
	used := (deltaTotal - deltaIdle) / deltaTotal * 100.0
	if used < 0 {
		used = 0
	}
	if used > 100 {
		used = 100
	}
	return used, true
}

func readCPUStat() (idle uint64, total uint64, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0, 0, false
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var vals [10]uint64
	n := 0
	for i := 1; i < len(fields) && n < len(vals); i++ {
		v, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		vals[n] = v
		n++
	}
	if n < 4 {
		return 0, 0, false
	}
	idle = vals[3]
	if n > 4 {
		idle += vals[4]
	}
	for i := 0; i < n; i++ {
		total += vals[i]
	}
	return idle, total, true
}

func readMemUsage() (used uint64, total uint64, pct float64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, false
	}
	var memTotalKB, memAvailKB uint64
	for _, ln := range strings.Split(string(data), "\n") {
		fields := strings.Fields(ln)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "MemTotal:":
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				memTotalKB = v
			}
		case "MemAvailable:":
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				memAvailKB = v
			}
		}
	}
	if memTotalKB == 0 || memAvailKB > memTotalKB {
		return 0, 0, 0, false
	}
	total = memTotalKB * 1024
	used = (memTotalKB - memAvailKB) * 1024
	pct = (float64(used) / float64(total)) * 100.0
	return used, total, pct, true
}

func readDiskUsage(path string) (used uint64, total uint64, pct float64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, false
	}
	total = st.Blocks * uint64(st.Bsize)
	avail := st.Bavail * uint64(st.Bsize)
	if total == 0 || avail > total {
		return 0, 0, 0, false
	}
	used = total - avail
	pct = (float64(used) / float64(total)) * 100.0
	return used, total, pct, true
}

func formatBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%dB", v)
	}
	div := float64(unit)
	suffix := "KiB"
	for _, s := range []string{"MiB", "GiB", "TiB", "PiB"} {
		if float64(v) < div*unit {
			break
		}
		div *= unit
		suffix = s
	}
	return fmt.Sprintf("%.1f%s", float64(v)/div, suffix)
}

func (c Config) useUSDTThresholds() bool {
	return c.MinBNBUSDT > 0 || c.TargetBNBUSDT > 0
}

func (c Config) useRatioThresholds() bool {
	return c.BNBRatioMode
}

func (c Config) resolveBNBThresholds(price, portfolioQuote float64) (float64, float64, error) {
	if price <= 0 {
		return 0, 0, errors.New("invalid symbol price for threshold conversion")
	}
	if c.useRatioThresholds() {
		if portfolioQuote <= 0 {
			return 0, 0, errors.New("invalid portfolio value for ratio threshold conversion")
		}
		minBNB := (portfolioQuote * c.BNBRatioMin) / price
		targetBNB := (portfolioQuote * c.BNBRatioTarget) / price
		return minBNB, targetBNB, nil
	}
	if c.useUSDTThresholds() {
		minBNB := c.MinBNBUSDT / price
		targetBNB := c.TargetBNBUSDT / price
		return minBNB, targetBNB, nil
	}
	return c.MinBNB, c.TargetBNB, nil
}

func (c Config) thresholdModeLine() string {
	if c.useRatioThresholds() {
		return fmt.Sprintf("Threshold ratio=%.4f%%, Target ratio=%.4f%% of portfolio", c.BNBRatioMin*100, c.BNBRatioTarget*100)
	}
	if c.useUSDTThresholds() {
		return fmt.Sprintf("Threshold=%s %.4f (~auto BNB), Target=%s %.4f (~auto BNB)", c.QuoteAsset, c.MinBNBUSDT, c.QuoteAsset, c.TargetBNBUSDT)
	}
	return fmt.Sprintf("Threshold=%.6f BNB, Target=%.6f BNB", c.MinBNB, c.TargetBNB)
}

func loadAccountSnapshot(ctx context.Context, cfg Config, binance *BinanceClient) (float64, float64, float64, float64, error) {
	balances, err := binance.GetFreeBalances(ctx)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("get balances: %w", err)
	}
	bnbFree := balances[cfg.BNBAsset]
	quoteFree := balances[cfg.QuoteAsset]

	price, err := binance.GetPrice(ctx, cfg.Symbol)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("get price: %w", err)
	}
	portfolioQuote, err := binance.EstimatePortfolioQuote(ctx, balances, cfg.QuoteAsset)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("estimate portfolio: %w", err)
	}
	return bnbFree, quoteFree, price, portfolioQuote, nil
}

func newTradeStore(cfg Config) (*TradeStore, error) {
	if !cfg.SQLiteEnabled {
		return nil, nil
	}
	db, err := sql.Open("sqlite", cfg.SQLitePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS trades (
  symbol TEXT NOT NULL,
  trade_id INTEGER NOT NULL,
  order_id INTEGER NOT NULL,
  side TEXT NOT NULL,
  price REAL NOT NULL,
  qty REAL NOT NULL,
  quote_qty REAL NOT NULL,
  commission REAL NOT NULL,
  commission_asset TEXT NOT NULL,
  trade_time INTEGER NOT NULL,
  PRIMARY KEY (symbol, trade_id)
);
CREATE INDEX IF NOT EXISTS idx_trades_symbol_time ON trades(symbol, trade_time);
CREATE INDEX IF NOT EXISTS idx_trades_time ON trades(trade_time);
CREATE TABLE IF NOT EXISTS trade_sync_state (
  symbol TEXT PRIMARY KEY,
  last_synced_time INTEGER NOT NULL
);
`); err != nil {
		_ = db.Close()
		return nil, err
	}
	log.Printf("sqlite trade store enabled path=%s", cfg.SQLitePath)
	return &TradeStore{
		db:                  db,
		initialLookbackDays: cfg.SQLiteInitialLookbackDays,
		syncInterval:        cfg.SQLiteSyncInterval,
		maxLookbackDays:     cfg.SQLiteMaxLookbackDays,
	}, nil
}

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

func resolveTrackedSymbols(ctx context.Context, cfg Config, binance *BinanceClient) ([]string, error) {
	if len(cfg.TrackedSymbols) == 1 && cfg.TrackedSymbols[0] == "FREQTRADE" {
		syms, err := resolveTrackedSymbolsFromFreqtrade(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if len(syms) == 0 {
			return nil, errors.New("freqtrade returned no tracked pairs")
		}
		return syms, nil
	}
	if len(cfg.TrackedSymbols) == 1 && cfg.TrackedSymbols[0] == "ALL" {
		syms, err := binance.ListTradingSymbolsByQuote(ctx, cfg.QuoteAsset)
		if err != nil {
			return nil, err
		}
		if cfg.MaxAutoTrackedSymbols > 0 && len(syms) > cfg.MaxAutoTrackedSymbols {
			syms = syms[:cfg.MaxAutoTrackedSymbols]
		}
		return syms, nil
	}
	return cfg.TrackedSymbols, nil
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
				lastErr = fmt.Errorf("http=%d body=%s", res.StatusCode, strings.TrimSpace(string(body)))
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
			body, _ := io.ReadAll(io.LimitReader(res.Body, 256))
			_ = res.Body.Close()
			pingLine = fmt.Sprintf("Ping: http=%d body=%s", res.StatusCode, strings.TrimSpace(string(body)))
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
	body, _ := io.ReadAll(io.LimitReader(res.Body, 256))
	_ = res.Body.Close()
	return fmt.Sprintf("%s: http=%d body=%s", path, res.StatusCode, strings.TrimSpace(string(body)))
}

func newRedisCache(cfg Config) (*RedisCache, error) {
	if !cfg.RedisEnabled {
		return &RedisCache{enabled: false}, nil
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	log.Printf("redis cache enabled addr=%s db=%d", cfg.RedisAddr, cfg.RedisDB)
	return &RedisCache{enabled: true, client: client, prefix: cfg.RedisKeyPrefix}, nil
}

func (c *RedisCache) key(raw string) string {
	return c.prefix + raw
}

func (c *RedisCache) getJSON(ctx context.Context, key string, dst any) (bool, error) {
	if c == nil || !c.enabled {
		return false, nil
	}
	v, err := c.client.Get(ctx, c.key(key)).Result()
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}
	if err := json.Unmarshal([]byte(v), dst); err != nil {
		return false, err
	}
	return true, nil
}

func (c *RedisCache) setJSON(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c == nil || !c.enabled {
		return nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, c.key(key), b, ttl).Err()
}

func selectDuration(key string) time.Duration {
	switch key {
	case "fees_day", "pnl_day", "report_day", "trades_day", "leaders_day", "chart_cum_fees_day", "chart_cum_profit_day":
		return 24 * time.Hour
	case "chart_cum_profit_48h":
		return 48 * time.Hour
	case "chart_cum_profit_72h":
		return 72 * time.Hour
	case "fees_week", "pnl_week", "report_week", "trades_week", "leaders_week", "chart_cum_fees_week", "chart_cum_profit_week":
		return 7 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

func durationLabel(key string) string {
	switch key {
	case "fees_day", "pnl_day", "report_day", "trades_day", "leaders_day":
		return "day"
	case "fees_week", "pnl_week", "report_week", "trades_week", "leaders_week":
		return "week"
	default:
		return "month"
	}
}

func parseCumProfitWindowInput(raw string) (time.Duration, string, string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 2 {
		return 0, "", "", false
	}
	unit := s[len(s)-1]
	if unit != 'h' && unit != 'd' {
		return 0, "", "", false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, "", "", false
	}
	switch unit {
	case 'h':
		if n > 24*30 {
			return 0, "", "", false
		}
		token := fmt.Sprintf("%dh", n)
		return time.Duration(n) * time.Hour, token, token, true
	case 'd':
		if n > 90 {
			return 0, "", "", false
		}
		token := fmt.Sprintf("%dd", n)
		return time.Duration(n) * 24 * time.Hour, token, token, true
	default:
		return 0, "", "", false
	}
}

func setAwaitingCustomCumProfitWindow(chatID int64, v bool) {
	customCumProfitInput.mu.Lock()
	defer customCumProfitInput.mu.Unlock()
	if v {
		customCumProfitInput.awaiting[chatID] = true
		return
	}
	delete(customCumProfitInput.awaiting, chatID)
}

func isAwaitingCustomCumProfitWindow(chatID int64) bool {
	customCumProfitInput.mu.Lock()
	defer customCumProfitInput.mu.Unlock()
	return customCumProfitInput.awaiting[chatID]
}

func parseCumProfitGranularity(token string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "h":
		return "hours", "hours"
	case "d":
		return "days", "days"
	default:
		return "auto", "auto"
	}
}

func parseRangeAgoToken(token string) (time.Duration, string, bool) {
	t := strings.ToLower(strings.TrimSpace(token))
	if t == "now" || t == "0h" || t == "0d" {
		return 0, "now", true
	}
	dur, _, label, ok := parseCumProfitWindowInput(t)
	return dur, label, ok
}

func setRangeFromSelection(chatID int64, token string) {
	customCumProfitRange.mu.Lock()
	defer customCumProfitRange.mu.Unlock()
	customCumProfitRange.from[chatID] = strings.ToLower(strings.TrimSpace(token))
}

func getRangeFromSelection(chatID int64) (string, bool) {
	customCumProfitRange.mu.Lock()
	defer customCumProfitRange.mu.Unlock()
	v, ok := customCumProfitRange.from[chatID]
	return v, ok
}

func clearRangeFromSelection(chatID int64) {
	customCumProfitRange.mu.Lock()
	defer customCumProfitRange.mu.Unlock()
	delete(customCumProfitRange.from, chatID)
}

func setAwaitingCustomCumProfitDateFrom(chatID int64) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	st := customCumProfitDateRange.m[chatID]
	st.AwaitFrom = true
	st.AwaitTo = false
	st.From = time.Time{}
	customCumProfitDateRange.m[chatID] = st
}

func setAwaitingCustomCumProfitDateTo(chatID int64, from time.Time) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	st := customCumProfitDateRange.m[chatID]
	st.AwaitFrom = false
	st.AwaitTo = true
	st.From = from.UTC()
	customCumProfitDateRange.m[chatID] = st
}

func isAwaitingCustomCumProfitDateFrom(chatID int64) bool {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	return customCumProfitDateRange.m[chatID].AwaitFrom
}

func isAwaitingCustomCumProfitDateTo(chatID int64) bool {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	return customCumProfitDateRange.m[chatID].AwaitTo
}

func getCustomCumProfitDateFrom(chatID int64) (time.Time, bool) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	st, ok := customCumProfitDateRange.m[chatID]
	if !ok || st.From.IsZero() {
		return time.Time{}, false
	}
	return st.From, true
}

func clearCustomCumProfitDateRangeState(chatID int64) {
	customCumProfitDateRange.mu.Lock()
	defer customCumProfitDateRange.mu.Unlock()
	delete(customCumProfitDateRange.m, chatID)
}

func parseUserDateTime(raw string) (time.Time, bool) {
	s := strings.TrimSpace(raw)
	layouts := []string{
		"2006-01-02 15:04",
		"2006-01-02 15",
		"2006-01-02T15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, s, time.UTC)
		if err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func setCalendarRangePhase(chatID int64, phase string) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st := customCumProfitCalendarRange.m[chatID]
	st.Phase = phase
	customCumProfitCalendarRange.m[chatID] = st
}

func setCalendarRangeFromDate(chatID int64, dt time.Time) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st := customCumProfitCalendarRange.m[chatID]
	st.From = dt.UTC()
	customCumProfitCalendarRange.m[chatID] = st
}

func setCalendarRangeToDate(chatID int64, dt time.Time) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st := customCumProfitCalendarRange.m[chatID]
	st.To = dt.UTC()
	customCumProfitCalendarRange.m[chatID] = st
}

func getCalendarRangeState(chatID int64) (calendarRangeState, bool) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	st, ok := customCumProfitCalendarRange.m[chatID]
	return st, ok
}

func clearCalendarRangeState(chatID int64) {
	customCumProfitCalendarRange.mu.Lock()
	defer customCumProfitCalendarRange.mu.Unlock()
	delete(customCumProfitCalendarRange.m, chatID)
}

func parseCalendarMonthToken(token string) (int, time.Month, bool) {
	s := strings.TrimSpace(token)
	if len(s) != 6 {
		return 0, 0, false
	}
	y, errA := strconv.Atoi(s[:4])
	m, errB := strconv.Atoi(s[4:])
	if errA != nil || errB != nil || y < 2000 || y > 2100 || m < 1 || m > 12 {
		return 0, 0, false
	}
	return y, time.Month(m), true
}

func parseCalendarDayToken(token string) (time.Time, bool) {
	s := strings.TrimSpace(token)
	if len(s) != 8 {
		return time.Time{}, false
	}
	y, errA := strconv.Atoi(s[:4])
	m, errB := strconv.Atoi(s[4:6])
	d, errC := strconv.Atoi(s[6:])
	if errA != nil || errB != nil || errC != nil {
		return time.Time{}, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return time.Time{}, false
	}
	return t, true
}

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

func formatTradesTable(label string, trades []myTrade, cfg Config, bnbPrice float64, displayCurrency string) string {
	type group struct {
		Symbol  string
		Trades  int
		BuyVal  float64
		SellVal float64
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
		Symbol  string
		Trades  int
		BuyVal  float64
		SellVal float64
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

func formatSignedNoPlus(v float64, prec int) string {
	if math.Abs(v) < 1e-12 {
		v = 0
	}
	return strconv.FormatFloat(v, 'f', prec, 64)
}

func trimNum(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "0"
	}
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}

func symbolsCacheKey(symbols []string) string {
	cp := append([]string(nil), symbols...)
	sort.Strings(cp)
	return strings.Join(cp, ",")
}

func buildLineChartURL(title string, labels []string, values []float64, unit string) string {
	if len(labels) == 0 {
		return ""
	}
	isPnL := strings.Contains(strings.ToUpper(title), "PNL")
	chartType := "line"
	lineColor := "#14b8a6"
	fillColor := "rgba(20,184,166,0.18)"
	if isPnL {
		lineColor = "#0ea5e9"
		fillColor = "rgba(14,165,233,0.18)"
	}

	datasets := make([]map[string]any, 0, 2)
	dataset := map[string]any{
		"label":           unit,
		"data":            values,
		"borderColor":     lineColor,
		"backgroundColor": fillColor,
		"fill":            true,
		"tension":         0.25,
		"pointRadius":     0,
		"borderWidth":     2,
	}
	if isPnL {
		dataset["borderColor"] = "#0ea5e9"
		dataset["backgroundColor"] = "rgba(14,165,233,0.18)"
	}
	datasets = append(datasets, dataset)

	cfg := map[string]any{
		"type": chartType,
		"data": map[string]any{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": map[string]any{
			"layout": map[string]any{
				"padding": map[string]any{
					"left": 8, "right": 12, "top": 8, "bottom": 4,
				},
			},
			"plugins": map[string]any{
				"legend": map[string]any{"display": true, "position": "top"},
				"title":  map[string]any{"display": true, "text": title},
				"datalabels": map[string]any{
					"display":   false,
					"anchor":    "end",
					"align":     "end",
					"offset":    2,
					"font":      map[string]any{"size": 10},
					"formatter": "function(v){ if (v === null || v === undefined) return ''; var n = Number(v); if (!isFinite(n)) return ''; if (Math.abs(n) < 1e-6) return ''; if (Math.abs(n) < 0.01) return n.toPrecision(3); return n.toFixed(2); }",
				},
			},
			"scales": map[string]any{
				"x": map[string]any{
					"ticks": map[string]any{
						"autoSkip":      true,
						"maxTicksLimit": 8,
						"maxRotation":   0,
						"minRotation":   0,
					},
					"grid": map[string]any{"color": "rgba(0,0,0,0.06)"},
				},
				"y": map[string]any{
					"ticks": map[string]any{
						"maxTicksLimit": 6,
					},
					"grid": map[string]any{"color": "rgba(0,0,0,0.06)"},
				},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	q := url.QueryEscape(string(b))
	return "https://quickchart.io/chart?backgroundColor=white&width=1000&height=500&c=" + q
}

func buildCumulativeProfitChartURL(title string, labels []string, values []float64, unit string) string {
	if len(labels) == 0 {
		return ""
	}
	points := make([]map[string]any, 0, len(values))
	labelAlign := make([]string, 0, len(values))
	labelColors := make([]string, 0, len(values))
	for i, v := range values {
		diffLabel := ""
		align := "top"
		color := "#22c55e"
		if i > 0 {
			d := v - values[i-1]
			rounded := math.Round(d*100) / 100
			if math.Abs(rounded) >= 1e-9 {
				diffLabel = fmt.Sprintf("%+.2f", rounded)
				if rounded < 0 {
					align = "bottom"
					color = "#ef4444"
				}
			}
		}
		points = append(points, map[string]any{
			"y":     v,
			"label": diffLabel,
		})
		labelAlign = append(labelAlign, align)
		labelColors = append(labelColors, color)
	}
	cfg := map[string]any{
		"type": "line",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{
				{
					"label":           "Profit",
					"data":            points,
					"borderColor":     "#d9d9d9",
					"backgroundColor": "rgba(217,217,217,0.18)",
					"pointRadius":     3,
					"pointHoverRadius": 4,
					"pointBackgroundColor": "#d9d9d9",
					"fill":            false,
					"stepped":         true,
					"tension":         0,
					"borderWidth":     2,
				},
			},
		},
		"options": map[string]any{
			"plugins": map[string]any{
				"title": map[string]any{
					"display": true,
					"text":    title,
					"color":   "#ffffff",
					"font": map[string]any{
						"size": 20,
					},
				},
				"legend": map[string]any{
					"display": true,
					"labels": map[string]any{
						"color": "#e5e7eb",
					},
				},
				"datalabels": map[string]any{
					"display": true,
					"align":   labelAlign,
					"anchor": "end",
					"offset": 4,
					"font": map[string]any{
						"size": 10,
						"weight": "bold",
					},
					"color":  labelColors,
				},
			},
			"scales": map[string]any{
				"x": map[string]any{
					"ticks": map[string]any{"color": "#d1d5db", "maxTicksLimit": 8},
					"grid":  map[string]any{"color": "rgba(255,255,255,0.10)"},
					"title": map[string]any{"display": false},
				},
				"y": map[string]any{
					"ticks": map[string]any{"color": "#d1d5db"},
					"grid":  map[string]any{"color": "rgba(255,255,255,0.10)"},
					"title": map[string]any{"display": true, "text": unit, "color": "#d1d5db"},
				},
			},
		},
	}
	b, _ := json.Marshal(cfg)
	q := url.QueryEscape(string(b))
	return "https://quickchart.io/chart?backgroundColor=%23000000&width=1000&height=500&c=" + q
}

func cumulativeProfitSeriesWindow(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, d time.Duration) ([]string, []float64, string) {
	return cumulativeProfitSeriesWindowMode(ctx, cfg, state, binance, d, "auto")
}

func cumulativeProfitSeriesWindowMode(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, d time.Duration, mode string) ([]string, []float64, string) {
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, d)
	unit := cfg.QuoteAsset
	if strings.EqualFold(displayCurrency, "BNB") && spot > 0 {
		unit = cfg.BNBAsset
	}
	hours := int(d / time.Hour)
	if hours <= 0 {
		hours = 24
	}
	modeNorm := strings.ToLower(strings.TrimSpace(mode))
	hoursMode := d <= 7*24*time.Hour
	if modeNorm == "hours" {
		hoursMode = true
	} else if modeNorm == "days" {
		hoursMode = false
	}
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return nil, nil, unit
		}
		var labels []string
		var series []float64
		if hoursMode {
			labels, series = freqtradePnlSeriesByHourActive(trades, hours)
		} else {
			days := int(d / (24 * time.Hour))
			labels, series = freqtradePnlSeriesByDay(trades, days)
		}
		return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
	}

	if hoursMode {
		labels, series := state.pnlSeriesLastNHours(hours)
		return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
	}
	days := int(d / (24 * time.Hour))
	rows := state.dailyPnlRows(days)
	if len(rows) == 0 {
		return nil, nil, unit
	}
	activeDays := state.snapshotDaySet(days)
	labels := make([]string, 0, len(rows))
	series := make([]float64, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if !activeDays[r.Day] {
			continue
		}
		labels = append(labels, r.Day)
		series = append(series, r.PnL)
	}
	return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
}

func cumulativeProfitSeriesRangeMode(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, fromAgo, toAgo time.Duration, mode string) ([]string, []float64, string) {
	if fromAgo <= toAgo {
		return nil, nil, cfg.QuoteAsset
	}
	now := time.Now().UTC()
	start := now.Add(-fromAgo)
	end := now.Add(-toAgo)
	return cumulativeProfitSeriesBetweenMode(ctx, cfg, state, binance, start, end, mode)
}

func cumulativeProfitSeriesBetweenMode(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, start, end time.Time, mode string) ([]string, []float64, string) {
	if !start.Before(end) {
		return nil, nil, cfg.QuoteAsset
	}
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spotWindow := end.Sub(start)
	if spotWindow <= 0 {
		spotWindow = 24 * time.Hour
	}
	spot := spotForDisplay(ctx, cfg, binance, spotWindow)
	unit := cfg.QuoteAsset
	if strings.EqualFold(displayCurrency, "BNB") && spot > 0 {
		unit = cfg.BNBAsset
	}
	modeNorm := strings.ToLower(strings.TrimSpace(mode))
	hoursMode := end.Sub(start) <= 7*24*time.Hour
	if modeNorm == "hours" {
		hoursMode = true
	} else if modeNorm == "days" {
		hoursMode = false
	}

	var labels []string
	var series []float64
	if cfg.FreqtradeHistoryMode {
		trades, err := getFreqtradeTrades30dCached(ctx, cfg)
		if err != nil {
			return nil, nil, unit
		}
		if hoursMode {
			labels, series = freqtradePnlSeriesByHourRangeActive(trades, start, end)
		} else {
			labels, series = freqtradePnlSeriesByDayRangeActive(trades, start, end)
		}
	} else {
		if hoursMode {
			labels, series = state.pnlSeriesByHourRangeActive(start, end)
		} else {
			labels, series = state.pnlSeriesByDayRangeActive(start, end)
		}
	}
	return cumulativeDisplaySeries(labels, series), cumulativeDisplayValues(series, cfg, displayCurrency, spot), unit
}

func cumulativeFeesSeriesWindow(ctx context.Context, cfg Config, state *MonitorState, binance *BinanceClient, d time.Duration) ([]string, []float64, string, error) {
	hoursMode := d <= 24*time.Hour
	var (
		labels  []string
		feesBNB []float64
		err     error
	)
	if cfg.FreqtradeHistoryMode && hoursMode {
		trades, tErr := getFreqtradeTrades30dCached(ctx, cfg)
		if tErr != nil {
			return nil, nil, "", tErr
		}
		labels, feesBNB = freqtradeFeeSeriesByHourActive(trades, cfg.BNBAsset, 24)
	} else if hoursMode {
		labels, feesBNB, err = feeSeriesLastNHours(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, 24)
	} else {
		days := int(d / (24 * time.Hour))
		labels, feesBNB, err = feeSeriesLastNDaysCached(ctx, binance, cfg.TrackedSymbols, cfg.BNBAsset, days)
	}
	if err != nil {
		return nil, nil, "", err
	}
	if len(labels) == 0 {
		return nil, nil, "", nil
	}
	displayCurrency := state.getDisplayCurrency(cfg.FeeMainCurrency)
	spot := spotForDisplay(ctx, cfg, binance, d)
	unit := cfg.BNBAsset
	if strings.EqualFold(displayCurrency, "USDT") {
		unit = cfg.QuoteAsset
	}
	return cumulativeDisplaySeries(labels, feesBNB), cumulativeDisplayValues(feesBNB, cfg, displayCurrency, spot), unit, nil
}

func cumulativeDisplaySeries(labels []string, series []float64) []string {
	if len(labels) == 0 || len(series) == 0 {
		return nil
	}
	n := len(labels)
	if len(series) < n {
		n = len(series)
	}
	return append([]string(nil), labels[:n]...)
}

func cumulativeDisplayValues(series []float64, cfg Config, displayCurrency string, spot float64) []float64 {
	if len(series) == 0 {
		return nil
	}
	out := make([]float64, 0, len(series))
	cum := 0.0
	for _, raw := range series {
		v, _, ok := quoteToDisplay(raw, cfg, displayCurrency, spot)
		if !ok {
			v = raw
		}
		cum += v
		out = append(out, cum)
	}
	return out
}

func freqtradePnlSeriesByHourActive(trades []freqtradeTrade, hours int) ([]string, []float64) {
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	buckets := map[string]float64{}
	active := map[string]bool{}
	for _, tr := range trades {
		if tr.CloseTimestamp < start.UnixMilli() {
			continue
		}
		k := time.UnixMilli(tr.CloseTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
		active[k] = true
		buckets[k] += tr.ProfitAbs
	}
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		if !active[k] {
			continue
		}
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradePnlSeriesByHourRangeActive(trades []freqtradeTrade, start, end time.Time) ([]string, []float64) {
	startMS := start.UnixMilli()
	endMS := end.UnixMilli()
	buckets := map[time.Time]float64{}
	for _, tr := range trades {
		ts := tr.CloseTimestamp
		if ts < startMS || ts > endMS {
			continue
		}
		k := time.UnixMilli(ts).UTC().Truncate(time.Hour)
		buckets[k] += tr.ProfitAbs
	}
	if len(buckets) == 0 {
		return nil, nil
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, k.Format("01-02 15:00"))
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradePnlSeriesByDayRangeActive(trades []freqtradeTrade, start, end time.Time) ([]string, []float64) {
	startMS := start.UnixMilli()
	endMS := end.UnixMilli()
	buckets := map[time.Time]float64{}
	for _, tr := range trades {
		ts := tr.CloseTimestamp
		if ts < startMS || ts > endMS {
			continue
		}
		k := time.UnixMilli(ts).UTC().Truncate(24 * time.Hour)
		buckets[k] += tr.ProfitAbs
	}
	if len(buckets) == 0 {
		return nil, nil
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	for _, k := range keys {
		labels = append(labels, k.Format("2006-01-02"))
		values = append(values, buckets[k])
	}
	return labels, values
}

func freqtradeFeeSeriesByHourActive(trades []freqtradeTrade, feeAsset string, hours int) ([]string, []float64) {
	asset := strings.ToUpper(strings.TrimSpace(feeAsset))
	now := time.Now().UTC().Truncate(time.Hour)
	start := now.Add(-time.Duration(hours-1) * time.Hour)
	buckets := map[string]float64{}
	active := map[string]bool{}
	for _, tr := range trades {
		openFee, closeFee := freqtradeTradeFeeInAsset(tr, asset)
		if tr.OpenTimestamp >= start.UnixMilli() {
			k := time.UnixMilli(tr.OpenTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
			active[k] = true
			buckets[k] += openFee
		}
		if tr.CloseTimestamp >= start.UnixMilli() {
			k := time.UnixMilli(tr.CloseTimestamp).UTC().Truncate(time.Hour).Format("01-02 15:00")
			active[k] = true
			buckets[k] += closeFee
		}
	}
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		if !active[k] {
			continue
		}
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values
}

func feeSeriesLastNHours(ctx context.Context, binance *BinanceClient, symbols []string, bnbAsset string, hours int) ([]string, []float64, error) {
	start := time.Now().UTC().Add(-time.Duration(hours-1) * time.Hour).UnixMilli()
	end := time.Now().UTC().UnixMilli()
	buckets := map[string]float64{}
	active := map[string]bool{}
	type result struct {
		byHour map[string]float64
		active map[string]bool
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
				ch <- result{err: err}
				return
			}
			local := map[string]float64{}
			localActive := map[string]bool{}
			for _, tr := range trades {
				if !strings.EqualFold(strings.TrimSpace(tr.CommissionAsset), strings.TrimSpace(bnbAsset)) {
					continue
				}
				fee, err := strconv.ParseFloat(strings.TrimSpace(tr.Commission), 64)
				if err != nil {
					continue
				}
				k := time.UnixMilli(tr.Time).UTC().Truncate(time.Hour).Format("01-02 15:00")
				localActive[k] = true
				local[k] += fee
			}
			ch <- result{byHour: local, active: localActive}
		}()
	}
	for i := 0; i < len(symbols); i++ {
		r := <-ch
		if r.err != nil {
			return nil, nil, r.err
		}
		for k, v := range r.byHour {
			buckets[k] += v
		}
		for k := range r.active {
			active[k] = true
		}
	}
	now := time.Now().UTC().Truncate(time.Hour)
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		if !active[k] {
			continue
		}
		labels = append(labels, k)
		values = append(values, buckets[k])
	}
	return labels, values, nil
}

func (s *MonitorState) snapshotDaySet(days int) map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, days)
	start := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	for _, sn := range s.snapshots {
		if sn.TS < start {
			continue
		}
		day := time.UnixMilli(sn.TS).UTC().Format("2006-01-02")
		out[day] = true
	}
	return out
}

func newMonitorState(stateFile string, maxSnapshots int) *MonitorState {
	return &MonitorState{stateFile: stateFile, maxSnapshots: maxSnapshots}
}

func (s *MonitorState) incChecks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks++
	return s.checks
}

func (s *MonitorState) incStartCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startCount++
	return s.startCount
}

func (s *MonitorState) getStartCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startCount
}

func (s *MonitorState) addSnapshot(sn Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots = append(s.snapshots, sn)
	if len(s.snapshots) > s.maxSnapshots {
		drop := len(s.snapshots) - s.maxSnapshots
		s.snapshots = s.snapshots[drop:]
	}
}

func (s *MonitorState) getLastBuyAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastBuyAt
}

func (s *MonitorState) setLastBuyAt(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBuyAt = t
}

func (s *MonitorState) addRefillEvent(ev RefillEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refillEvents = append(s.refillEvents, ev)
	if len(s.refillEvents) > s.maxSnapshots {
		drop := len(s.refillEvents) - s.maxSnapshots
		s.refillEvents = s.refillEvents[drop:]
	}
}

func (s *MonitorState) getDisplayCurrency(defaultVal string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := strings.ToUpper(strings.TrimSpace(s.feeCurrency))
	if v == "BNB" || v == "USDT" {
		return v
	}
	d := strings.ToUpper(strings.TrimSpace(defaultVal))
	if d == "" {
		return ""
	}
	if d == "USDT" {
		return "USDT"
	}
	return "BNB"
}

func (s *MonitorState) setDisplayCurrency(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up := strings.ToUpper(strings.TrimSpace(v))
	if up != "BNB" && up != "USDT" {
		return
	}
	s.feeCurrency = up
}

func (s *MonitorState) addCustomCumWindow(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := strings.ToLower(strings.TrimSpace(token))
	if t == "" {
		return
	}
	filtered := make([]string, 0, len(s.customCumWin)+1)
	filtered = append(filtered, t)
	for _, it := range s.customCumWin {
		if strings.EqualFold(strings.TrimSpace(it), t) {
			continue
		}
		filtered = append(filtered, strings.ToLower(strings.TrimSpace(it)))
		if len(filtered) >= 12 {
			break
		}
	}
	s.customCumWin = filtered
}

func (s *MonitorState) customCumWindows() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.customCumWin))
	for _, it := range s.customCumWin {
		t := strings.ToLower(strings.TrimSpace(it))
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (s *MonitorState) addCustomRange(from, to time.Time) {
	if !to.After(from) {
		return
	}
	rec := rangeRecord{FromTS: from.UTC().Unix(), ToTS: to.UTC().Unix()}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]rangeRecord, 0, 5)
	next = append(next, rec)
	for _, it := range s.customRanges {
		if it.FromTS == rec.FromTS && it.ToTS == rec.ToTS {
			continue
		}
		next = append(next, it)
		if len(next) >= 5 {
			break
		}
	}
	s.customRanges = next
}

func (s *MonitorState) customRangeHistory() []rangeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rangeRecord, 0, len(s.customRanges))
	for _, it := range s.customRanges {
		if it.ToTS > it.FromTS {
			out = append(out, it)
		}
	}
	return out
}

type refillStats struct {
	Count       int
	QuoteSpent  float64
	BNBReceived float64
}

type dailyPnlRow struct {
	Day string
	PnL float64
	Pct float64
}

func (s *MonitorState) refillStatsSince(d time.Duration) refillStats {
	s.mu.Lock()
	defer s.mu.Unlock()

	cut := time.Now().UTC().Add(-d).UnixMilli()
	out := refillStats{}
	for _, ev := range s.refillEvents {
		if ev.TS < cut {
			continue
		}
		out.Count++
		out.QuoteSpent += ev.QuoteSpent
		out.BNBReceived += ev.BNBReceived
	}
	return out
}

func (s *MonitorState) pnlSince(d time.Duration) (float64, float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.snapshots) < 2 {
		return 0, 0, false
	}

	now := time.Now().UTC()
	cut := now.Add(-d).UnixMilli()
	latest := s.snapshots[len(s.snapshots)-1]

	base := s.snapshots[0]
	for _, sn := range s.snapshots {
		if sn.TS >= cut {
			base = sn
			break
		}
	}

	if base.PortfolioQuote <= 0 {
		return latest.PortfolioQuote - base.PortfolioQuote, 0, true
	}
	pnl := latest.PortfolioQuote - base.PortfolioQuote
	pct := (pnl / base.PortfolioQuote) * 100
	return pnl, pct, true
}

func (s *MonitorState) pnlSeriesLastNDays(days int) ([]string, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.snapshots) < 2 {
		return nil, nil
	}

	buckets := map[string]Snapshot{}
	for _, sn := range s.snapshots {
		day := time.UnixMilli(sn.TS).UTC().Format("2006-01-02")
		prev, ok := buckets[day]
		if !ok || sn.TS > prev.TS {
			buckets[day] = sn
		}
	}

	labels := make([]string, 0, days)
	values := make([]float64, 0, days)
	var prev *Snapshot
	for i := days - 1; i >= 0; i-- {
		day := time.Now().UTC().Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		sn, ok := buckets[day]
		if !ok {
			continue
		}
		labels = append(labels, day)
		if prev == nil {
			values = append(values, 0)
		} else {
			values = append(values, sn.PortfolioQuote-prev.PortfolioQuote)
		}
		copySN := sn
		prev = &copySN
	}

	return labels, values
}

func (s *MonitorState) portfolioSeriesLastNDays(days int) ([]string, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.snapshots) == 0 {
		return nil, nil
	}

	buckets := map[string]Snapshot{}
	for _, sn := range s.snapshots {
		day := time.UnixMilli(sn.TS).UTC().Format("2006-01-02")
		prev, ok := buckets[day]
		if !ok || sn.TS > prev.TS {
			buckets[day] = sn
		}
	}

	labels := make([]string, 0, days)
	values := make([]float64, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := time.Now().UTC().Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		sn, ok := buckets[day]
		if !ok {
			continue
		}
		labels = append(labels, day)
		values = append(values, sn.PortfolioQuote)
	}
	return labels, values
}

func (s *MonitorState) pnlSeriesLastNHours(hours int) ([]string, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.snapshots) < 2 {
		return nil, nil
	}
	buckets := map[string]Snapshot{}
	for _, sn := range s.snapshots {
		k := time.UnixMilli(sn.TS).UTC().Truncate(time.Hour).Format("01-02 15:00")
		prev, ok := buckets[k]
		if !ok || sn.TS > prev.TS {
			buckets[k] = sn
		}
	}
	now := time.Now().UTC().Truncate(time.Hour)
	labels := make([]string, 0, hours)
	values := make([]float64, 0, hours)
	var prev *Snapshot
	for i := hours - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Hour).Format("01-02 15:00")
		sn, ok := buckets[k]
		if !ok {
			continue
		}
		labels = append(labels, k)
		if prev == nil {
			values = append(values, 0)
		} else {
			values = append(values, sn.PortfolioQuote-prev.PortfolioQuote)
		}
		copySN := sn
		prev = &copySN
	}
	return labels, values
}

func (s *MonitorState) pnlSeriesByHourRangeActive(start, end time.Time) ([]string, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.snapshots) < 2 {
		return nil, nil
	}
	buckets := map[time.Time]Snapshot{}
	for _, sn := range s.snapshots {
		t := time.UnixMilli(sn.TS).UTC()
		if t.Before(start) || t.After(end) {
			continue
		}
		k := t.Truncate(time.Hour)
		prev, ok := buckets[k]
		if !ok || sn.TS > prev.TS {
			buckets[k] = sn
		}
	}
	if len(buckets) == 0 {
		return nil, nil
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	var prev *Snapshot
	for _, k := range keys {
		sn := buckets[k]
		labels = append(labels, k.Format("01-02 15:00"))
		if prev == nil {
			values = append(values, 0)
		} else {
			values = append(values, sn.PortfolioQuote-prev.PortfolioQuote)
		}
		copySN := sn
		prev = &copySN
	}
	return labels, values
}

func (s *MonitorState) pnlSeriesByDayRangeActive(start, end time.Time) ([]string, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.snapshots) < 2 {
		return nil, nil
	}
	buckets := map[time.Time]Snapshot{}
	for _, sn := range s.snapshots {
		t := time.UnixMilli(sn.TS).UTC()
		if t.Before(start) || t.After(end) {
			continue
		}
		k := t.Truncate(24 * time.Hour)
		prev, ok := buckets[k]
		if !ok || sn.TS > prev.TS {
			buckets[k] = sn
		}
	}
	if len(buckets) == 0 {
		return nil, nil
	}
	keys := make([]time.Time, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	labels := make([]string, 0, len(keys))
	values := make([]float64, 0, len(keys))
	var prev *Snapshot
	for _, k := range keys {
		sn := buckets[k]
		labels = append(labels, k.Format("2006-01-02"))
		if prev == nil {
			values = append(values, 0)
		} else {
			values = append(values, sn.PortfolioQuote-prev.PortfolioQuote)
		}
		copySN := sn
		prev = &copySN
	}
	return labels, values
}

func (s *MonitorState) dailyPnlRows(days int) []dailyPnlRow {
	s.mu.Lock()
	defer s.mu.Unlock()

	buckets := map[string]Snapshot{}
	for _, sn := range s.snapshots {
		day := time.UnixMilli(sn.TS).UTC().Format("2006-01-02")
		prev, ok := buckets[day]
		if !ok || sn.TS > prev.TS {
			buckets[day] = sn
		}
	}

	rows := make([]dailyPnlRow, 0, days)
	var prevClose float64
	havePrev := false
	for i := 0; i < days; i++ {
		day := time.Now().UTC().Add(-time.Duration(i) * 24 * time.Hour).Format("2006-01-02")
		sn, ok := buckets[day]
		if !ok {
			rows = append(rows, dailyPnlRow{Day: day, PnL: 0, Pct: 0})
			continue
		}
		closeVal := sn.PortfolioQuote
		pnl := 0.0
		pct := 0.0
		if havePrev {
			pnl = closeVal - prevClose
			if prevClose != 0 {
				pct = (pnl / prevClose) * 100
			}
		}
		rows = append(rows, dailyPnlRow{Day: day, PnL: pnl, Pct: pct})
		prevClose = closeVal
		havePrev = true
	}
	return rows
}

func (s *MonitorState) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p := persistState{
		Checks:       s.checks,
		StartCount:   s.startCount,
		LastUpdated:  time.Now().UTC().UnixMilli(),
		Snapshots:    append([]Snapshot(nil), s.snapshots...),
		RefillEvents: append([]RefillEvent(nil), s.refillEvents...),
		FeeCurrency:  s.feeCurrency,
		CustomCumWin: append([]string(nil), s.customCumWin...),
		CustomRanges: append([]rangeRecord(nil), s.customRanges...),
	}
	if !s.lastBuyAt.IsZero() {
		p.LastBuyAt = s.lastBuyAt.UnixMilli()
	}

	if err := os.MkdirAll(filepath.Dir(s.stateFile), 0o755); err != nil {
		return err
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.stateFile, b, 0o644)
}

func (s *MonitorState) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.stateFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var p persistState
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	s.checks = p.Checks
	s.startCount = p.StartCount
	if p.LastBuyAt > 0 {
		s.lastBuyAt = time.UnixMilli(p.LastBuyAt).UTC()
	}
	s.snapshots = p.Snapshots
	s.refillEvents = p.RefillEvents
	s.feeCurrency = strings.ToUpper(strings.TrimSpace(p.FeeCurrency))
	s.customCumWin = append([]string(nil), p.CustomCumWin...)
	s.customRanges = append([]rangeRecord(nil), p.CustomRanges...)
	if len(s.snapshots) > s.maxSnapshots {
		drop := len(s.snapshots) - s.maxSnapshots
		s.snapshots = s.snapshots[drop:]
	}
	if len(s.refillEvents) > s.maxSnapshots {
		drop := len(s.refillEvents) - s.maxSnapshots
		s.refillEvents = s.refillEvents[drop:]
	}
	return nil
}

func (b *BinanceClient) GetFreeBalances(ctx context.Context) (map[string]float64, error) {
	vals := url.Values{}
	body, err := b.signedRequest(ctx, http.MethodGet, "/api/v3/account", vals)
	if err != nil {
		return nil, err
	}

	var resp accountResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode account response: %w", err)
	}

	out := make(map[string]float64, len(resp.Balances))
	for _, bal := range resp.Balances {
		free, _ := strconv.ParseFloat(bal.Free, 64)
		locked, _ := strconv.ParseFloat(bal.Locked, 64)
		total := free + locked
		if total <= 0 {
			continue
		}
		out[bal.Asset] = total
	}
	return out, nil
}

func (b *BinanceClient) EstimatePortfolioQuote(ctx context.Context, balances map[string]float64, quoteAsset string) (float64, error) {
	priceMap, err := b.GetAllPrices(ctx)
	if err != nil {
		return 0, err
	}

	total := 0.0
	for asset, amount := range balances {
		if amount <= 0 {
			continue
		}
		if asset == quoteAsset {
			total += amount
			continue
		}

		direct := asset + quoteAsset
		if px, ok := priceMap[direct]; ok && px > 0 {
			total += amount * px
			continue
		}
		inverse := quoteAsset + asset
		if px, ok := priceMap[inverse]; ok && px > 0 {
			total += amount / px
			continue
		}

		// Approximate USD stablecoins as 1:1 when quote is USDT.
		if quoteAsset == "USDT" && isUSDStable(asset) {
			total += amount
		}
	}
	return total, nil
}

func isUSDStable(asset string) bool {
	switch asset {
	case "USDT", "USDC", "BUSD", "TUSD", "FDUSD", "USDP", "DAI":
		return true
	default:
		return false
	}
}

func (b *BinanceClient) GetPrice(ctx context.Context, symbol string) (float64, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/ticker/price", vals)
	if err != nil {
		return 0, err
	}
	var resp priceResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode price response: %w", err)
	}
	p, err := strconv.ParseFloat(resp.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("parse price: %w", err)
	}
	return p, nil
}

func (b *BinanceClient) GetAllPrices(ctx context.Context) (map[string]float64, error) {
	cacheKey := "prices:all"
	var cached map[string]float64
	if ok, err := b.cache.getJSON(ctx, cacheKey, &cached); err == nil && ok {
		return cached, nil
	}

	vals := url.Values{}
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/ticker/price", vals)
	if err != nil {
		return nil, err
	}

	var rows []priceResponse
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode prices response: %w", err)
	}

	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		p, err := strconv.ParseFloat(row.Price, 64)
		if err != nil || p <= 0 {
			continue
		}
		out[row.Symbol] = p
	}
	_ = b.cache.setJSON(ctx, cacheKey, out, b.pricesTTL)
	return out, nil
}

func (b *BinanceClient) GetMinNotional(ctx context.Context, symbol string) (float64, error) {
	b.mu.Lock()
	if b.loadedMin {
		v := b.minNotional
		b.mu.Unlock()
		return v, nil
	}
	b.mu.Unlock()

	vals := url.Values{}
	vals.Set("symbol", symbol)
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/exchangeInfo", vals)
	if err != nil {
		return 0, err
	}

	var resp exchangeInfoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode exchangeInfo response: %w", err)
	}
	if len(resp.Symbols) == 0 {
		return 0, errors.New("symbol not found in exchangeInfo")
	}

	minNotional := 0.0
	for _, f := range resp.Symbols[0].Filters {
		if f.FilterType == "MIN_NOTIONAL" || f.FilterType == "NOTIONAL" {
			v, err := strconv.ParseFloat(f.MinNotional, 64)
			if err == nil && v > 0 {
				minNotional = v
				break
			}
		}
	}

	b.mu.Lock()
	b.minNotional = minNotional
	b.loadedMin = true
	b.mu.Unlock()

	return minNotional, nil
}

func (b *BinanceClient) ListTradingSymbolsByQuote(ctx context.Context, quoteAsset string) ([]string, error) {
	vals := url.Values{}
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/exchangeInfo", vals)
	if err != nil {
		return nil, err
	}
	var resp exchangeInfoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode exchangeInfo response: %w", err)
	}
	out := make([]string, 0, len(resp.Symbols))
	quote := strings.ToUpper(strings.TrimSpace(quoteAsset))
	for _, s := range resp.Symbols {
		if s.Status != "TRADING" {
			continue
		}
		if strings.ToUpper(s.QuoteAsset) != quote {
			continue
		}
		out = append(out, s.Symbol)
	}
	sort.Strings(out)
	return out, nil
}

func (b *BinanceClient) GetMyTrades(ctx context.Context, symbol string, startTimeMS, endTimeMS int64) ([]myTrade, error) {
	if endTimeMS < startTimeMS {
		return nil, errors.New("endTime must be >= startTime")
	}
	const maxWindowMS int64 = 24*60*60*1000 - 1

	all := make([]myTrade, 0, 128)
	windowStart := startTimeMS
	for windowStart <= endTimeMS {
		windowEnd := windowStart + maxWindowMS
		if windowEnd > endTimeMS {
			windowEnd = endTimeMS
		}

		batch, err := b.getMyTradesWindow(ctx, symbol, windowStart, windowEnd)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)

		if windowEnd == endTimeMS {
			break
		}
		windowStart = windowEnd + 1
	}

	// Ensure deterministic order and remove any potential duplicates by trade id.
	sort.Slice(all, func(i, j int) bool { return all[i].Time < all[j].Time })
	uniq := make([]myTrade, 0, len(all))
	seen := make(map[int64]struct{}, len(all))
	for _, tr := range all {
		if _, ok := seen[tr.ID]; ok {
			continue
		}
		seen[tr.ID] = struct{}{}
		uniq = append(uniq, tr)
	}
	return uniq, nil
}

func (b *BinanceClient) getMyTradesWindow(ctx context.Context, symbol string, startTimeMS, endTimeMS int64) ([]myTrade, error) {
	cacheKey := fmt.Sprintf("trades:%s:%d:%d", symbol, startTimeMS, endTimeMS)
	var cached []myTrade
	if ok, err := b.cache.getJSON(ctx, cacheKey, &cached); err == nil && ok {
		log.Printf("mytrades cache hit symbol=%s start=%d end=%d count=%d", symbol, startTimeMS, endTimeMS, len(cached))
		return cached, nil
	}

	all := make([]myTrade, 0, 128)
	fromID := int64(-1)

	for page := 0; page < 10; page++ {
		// Global limiter for /myTrades to avoid hitting Binance request-weight bans.
		b.myTradesSem <- struct{}{}
		if err := b.waitMyTradesSlot(); err != nil {
			<-b.myTradesSem
			return nil, err
		}
		vals := url.Values{}
		vals.Set("symbol", symbol)
		vals.Set("limit", "1000")
		vals.Set("startTime", strconv.FormatInt(startTimeMS, 10))
		vals.Set("endTime", strconv.FormatInt(endTimeMS, 10))
		if fromID >= 0 {
			vals.Set("fromId", strconv.FormatInt(fromID, 10))
		}

		reqStarted := time.Now()
		body, err := b.signedRequest(ctx, http.MethodGet, "/api/v3/myTrades", vals)
		<-b.myTradesSem
		if err != nil {
			return nil, err
		}

		var batch []myTrade
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("decode myTrades response: %w", err)
		}
		log.Printf(
			"mytrades fetch symbol=%s start=%d end=%d page=%d fromId=%d batch=%d duration_ms=%d",
			symbol,
			startTimeMS,
			endTimeMS,
			page+1,
			fromID,
			len(batch),
			time.Since(reqStarted).Milliseconds(),
		)
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			batch[i].Symbol = symbol
		}

		all = append(all, batch...)
		if len(batch) < 1000 {
			break
		}
		fromID = batch[len(batch)-1].ID + 1
	}
	_ = b.cache.setJSON(ctx, cacheKey, all, b.tradeTTL)
	return all, nil
}

func (b *BinanceClient) waitMyTradesSlot() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.banUntil.After(now) {
		return fmt.Errorf("binance temporary ban active until %s", b.banUntil.UTC().Format(time.RFC3339))
	}
	if b.myTradesMinInterval <= 0 {
		return nil
	}
	next := b.lastMyTradesRequest.Add(b.myTradesMinInterval)
	if next.After(now) {
		time.Sleep(next.Sub(now))
	}
	b.lastMyTradesRequest = time.Now()
	return nil
}

func (b *BinanceClient) MarketBuyByQuote(ctx context.Context, symbol string, quoteAmount float64) (orderResponse, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)
	vals.Set("side", "BUY")
	vals.Set("type", "MARKET")
	vals.Set("quoteOrderQty", formatFloat(quoteAmount, 8))

	body, err := b.signedRequest(ctx, http.MethodPost, "/api/v3/order", vals)
	if err != nil {
		return orderResponse{}, err
	}

	var resp orderResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return orderResponse{}, fmt.Errorf("decode order response: %w", err)
	}
	return resp, nil
}

func (b *BinanceClient) publicRequest(ctx context.Context, method, path string, params url.Values) ([]byte, error) {
	started := time.Now()
	defer logTiming("binance_public_"+path, started)
	source := "binance.public" + path
	endpoint := b.baseURL + path
	if enc := params.Encode(); enc != "" {
		endpoint += "?" + enc
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}

	res, err := b.httpClient.Do(req)
	if err != nil {
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		err := decodeBinanceError(res.StatusCode, body)
		logIfErr("binance_public_"+path, err)
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	if runtimeAlerts != nil {
		runtimeAlerts.observeAPICall(source, time.Since(started), nil)
	}
	return body, nil
}

func (b *BinanceClient) signedRequest(ctx context.Context, method, path string, params url.Values) ([]byte, error) {
	started := time.Now()
	defer logTiming("binance_signed_"+path, started)
	source := "binance.signed" + path
	if err := b.checkBan(); err != nil {
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, 0, err)
		}
		return nil, err
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UTC().UnixMilli(), 10))
	params.Set("recvWindow", strconv.FormatInt(b.recvWindow, 10))

	query := params.Encode()
	sig := signHMACSHA256(query, b.secret)
	query = query + "&signature=" + sig

	endpoint := b.baseURL + path + "?" + query
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", b.apiKey)

	res, err := b.httpClient.Do(req)
	if err != nil {
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		err := decodeBinanceError(res.StatusCode, body)
		b.captureBan(err)
		logIfErr("binance_signed_"+path, err)
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	if runtimeAlerts != nil {
		runtimeAlerts.observeAPICall(source, time.Since(started), nil)
	}
	return body, nil
}

func (b *BinanceClient) checkBan() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.banUntil.After(time.Now()) {
		return fmt.Errorf("binance temporary ban active until %s", b.banUntil.UTC().Format(time.RFC3339))
	}
	return nil
}

func (b *BinanceClient) captureBan(err error) {
	be, ok := err.(*binanceAPIError)
	if !ok || be.BanUntil.IsZero() {
		return
	}
	b.mu.Lock()
	if be.BanUntil.After(b.banUntil) {
		b.banUntil = be.BanUntil
	}
	b.mu.Unlock()
}

func decodeBinanceError(code int, body []byte) error {
	var be binanceErrorResponse
	if err := json.Unmarshal(body, &be); err == nil && be.Msg != "" {
		out := &binanceAPIError{
			HTTPStatus: code,
			Code:       be.Code,
			Msg:        be.Msg,
		}
		// Example msg: "... banned until 1772839084287 ..."
		if idx := strings.Index(be.Msg, "until "); idx >= 0 {
			rest := be.Msg[idx+len("until "):]
			num := make([]rune, 0, 13)
			for _, ch := range rest {
				if ch < '0' || ch > '9' {
					break
				}
				num = append(num, ch)
			}
			if len(num) >= 10 {
				if v, parseErr := strconv.ParseInt(string(num), 10, 64); parseErr == nil {
					if len(num) >= 13 {
						out.BanUntil = time.UnixMilli(v).UTC()
					} else {
						out.BanUntil = time.Unix(v, 0).UTC()
					}
				}
			}
		}
		return out
	}
	return fmt.Errorf("binance http=%d body=%s", code, strings.TrimSpace(string(body)))
}

func signHMACSHA256(msg, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}

func (t *TelegramNotifier) Send(text string, markup any) error {
	if t.token == "" || t.chatID == "" {
		return nil
	}
	id, err := strconv.ParseInt(t.chatID, 10, 64)
	if err != nil {
		return err
	}
	return t.SendToChat(id, text, markup)
}

func (t *TelegramNotifier) SendToChat(chatID int64, text string, markup any) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	_, err := t.call("sendMessage", payload)
	return err
}

func (t *TelegramNotifier) SendPhotoURL(chatID int64, photoURL, caption string) error {
	if photoURL == "" {
		return errors.New("empty photo url")
	}
	payload := map[string]any{
		"chat_id": chatID,
		"photo":   photoURL,
		"caption": caption,
	}
	_, err := t.call("sendPhoto", payload)
	return err
}

func (t *TelegramNotifier) SendPhoto(photoURL, caption string) error {
	if t.token == "" || t.chatID == "" {
		return nil
	}
	id, err := strconv.ParseInt(t.chatID, 10, 64)
	if err != nil {
		return err
	}
	return t.SendPhotoURL(id, photoURL, caption)
}

func (t *TelegramNotifier) GetUpdates(ctx context.Context, offset int) ([]tgUpdate, int, error) {
	payload := map[string]any{
		"offset":  offset,
		"timeout": 20,
	}
	body, err := t.callWithContext(ctx, "getUpdates", payload)
	if err != nil {
		return nil, offset, err
	}
	var resp tgUpdateResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, offset, err
	}
	if !resp.OK {
		return nil, offset, errors.New("telegram getUpdates not ok")
	}
	next := offset
	for _, u := range resp.Result {
		if u.UpdateID >= next {
			next = u.UpdateID + 1
		}
	}
	return resp.Result, next, nil
}

func (t *TelegramNotifier) AnswerCallback(callbackID, text string) error {
	_, err := t.call("answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	})
	return err
}

func (t *TelegramNotifier) allowedChat(chatID int64) bool {
	return strconv.FormatInt(chatID, 10) == strings.TrimSpace(t.chatID)
}

func (t *TelegramNotifier) call(method string, payload any) ([]byte, error) {
	return t.callWithContext(context.Background(), method, payload)
}

func (t *TelegramNotifier) callWithContext(ctx context.Context, method string, payload any) ([]byte, error) {
	if t.token == "" {
		return nil, errors.New("telegram token missing")
	}
	endpoint := fmt.Sprintf("%s/bot%s/%s", t.baseURL, t.token, method)
	buf, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("telegram %s http=%d body=%s", method, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func safeSend(n *TelegramNotifier, text string, markup any) {
	logIfErr("telegram.send", n.Send(text, markup))
}

func safeSendToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	logIfErr("telegram.send_to_chat", n.SendToChat(chatID, text, markup))
}

func safeSendPhoto(n *TelegramNotifier, photoURL, caption string) {
	logIfErr("telegram.send_photo", n.SendPhoto(photoURL, caption))
}

func safeSendPhotoToChat(n *TelegramNotifier, chatID int64, photoURL, caption string) {
	logIfErr("telegram.send_photo_to_chat", n.SendPhotoURL(chatID, photoURL, caption))
}

func safeAnswerCallback(n *TelegramNotifier, callbackID, text string) {
	logIfErr("telegram.answer_callback", n.AnswerCallback(callbackID, text))
}

func safeSendLargeToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	const maxChunk = 3500
	if len(text) <= maxChunk {
		safeSendToChat(n, chatID, text, markup)
		return
	}

	lines := strings.Split(text, "\n")
	var chunk strings.Builder
	for _, line := range lines {
		// +1 for newline
		if chunk.Len()+len(line)+1 > maxChunk {
			safeSendToChat(n, chatID, chunk.String(), nil)
			chunk.Reset()
		}
		chunk.WriteString(line)
		chunk.WriteByte('\n')
	}
	if chunk.Len() > 0 {
		safeSendToChat(n, chatID, chunk.String(), markup)
	}
}

func safeSendPreToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	escaped := html.EscapeString(text)
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     "<pre>" + escaped + "</pre>",
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	_, err := n.call("sendMessage", payload)
	logIfErr("telegram.send_pre", err)
}

func safeSendPreLargeToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	const maxChunk = 3300
	if len(text) <= maxChunk {
		safeSendPreToChat(n, chatID, text, markup)
		return
	}
	lines := strings.Split(text, "\n")
	var chunk strings.Builder
	for _, line := range lines {
		if chunk.Len()+len(line)+1 > maxChunk {
			safeSendPreToChat(n, chatID, chunk.String(), nil)
			chunk.Reset()
		}
		chunk.WriteString(line)
		chunk.WriteByte('\n')
	}
	if chunk.Len() > 0 {
		safeSendPreToChat(n, chatID, chunk.String(), markup)
	}
}

func loadConfig() (Config, error) {
	cfg := Config{
		Symbol:                    getEnv("SYMBOL", "BNBUSDT"),
		TrackedSymbols:            parseSymbols(getEnv("TRACKED_SYMBOLS", "BNBUSDT")),
		MaxAutoTrackedSymbols:     mustInt("MAX_AUTO_TRACKED_SYMBOLS", 40),
		BNBAsset:                  getEnv("BNB_ASSET", "BNB"),
		QuoteAsset:                getEnv("QUOTE_ASSET", "USDT"),
		CheckInterval:             mustDuration("CHECK_INTERVAL", "2m"),
		BuyCooldown:               mustDuration("BUY_COOLDOWN", "15m"),
		RecvWindowMs:              mustInt64("RECV_WINDOW_MS", 5000),
		SummaryEveryChecks:        mustInt("SUMMARY_EVERY_CHECKS", 10),
		NotifyOnEveryCheck:        mustBool("NOTIFY_ON_EVERY_CHECK", false),
		BinanceBaseURL:            getEnv("BINANCE_BASE_URL", "https://api.binance.com"),
		TelegramBaseURL:           getEnv("TELEGRAM_BASE_URL", "https://api.telegram.org"),
		AccountReserveRatio:       mustFloat("ACCOUNT_RESERVE_RATIO", 0.98),
		MinBuyQuote:               mustFloat("MIN_BUY_QUOTE", 5.0),
		StateFile:                 getEnv("STATE_FILE", "./data/state.json"),
		MaxSnapshots:              mustInt("MAX_SNAPSHOTS", 3000),
		DailyReportEnabled:        mustBool("DAILY_REPORT_ENABLED", true),
		DailyReportTimeUTC:        getEnv("DAILY_REPORT_TIME_UTC", "00:05"),
		DailyReportTimezone:       getEnv("DAILY_REPORT_TIMEZONE", "UTC"),
		DailyReportMode:           strings.ToLower(getEnv("DAILY_REPORT_MODE", "full")),
		DailyDigestTrades:         mustInt("DAILY_DIGEST_TRADES", 3),
		FeeMainCurrency:           strings.ToUpper(getEnv("FEE_MAIN_CURRENCY", "BNB")),
		HeartbeatEnabled:          mustBool("HEARTBEAT_ENABLED", true),
		HeartbeatStaleAfter:       mustDuration("HEARTBEAT_STALE_AFTER", "10m"),
		HeartbeatCheckInterval:    mustDuration("HEARTBEAT_CHECK_INTERVAL", "1m"),
		HeartbeatPingURL:          getEnv("HEARTBEAT_PING_URL", ""),
		APIFailureAlertEnabled:    mustBool("API_FAILURE_ALERT_ENABLED", true),
		APIFailureThreshold:       mustInt("API_FAILURE_THRESHOLD", 3),
		APIFailureAlertCooldown:   mustDuration("API_FAILURE_ALERT_COOLDOWN", "15m"),
		APILatencyThreshold:       mustDuration("API_LATENCY_THRESHOLD", "8s"),
		APILatencySpikeThreshold:  mustInt("API_LATENCY_SPIKE_THRESHOLD", 3),
		AbnormalMoveAlertEnabled:  mustBool("ABNORMAL_MOVE_ALERT_ENABLED", true),
		AbnormalMoveDrop1hPct:     mustFloat("ABNORMAL_MOVE_DROP_1H_PCT", 3.0),
		AbnormalMoveDrop24hPct:    mustFloat("ABNORMAL_MOVE_DROP_24H_PCT", 8.0),
		AbnormalMoveAlertCooldown: mustDuration("ABNORMAL_MOVE_ALERT_COOLDOWN", "2h"),
		BNBRatioMode:              mustBool("BNB_RATIO_MODE", false),
		RedisEnabled:              mustBool("REDIS_ENABLED", false),
		RedisAddr:                 getEnv("REDIS_ADDR", "redis:6379"),
		RedisPassword:             getEnv("REDIS_PASSWORD", ""),
		RedisDB:                   mustInt("REDIS_DB", 0),
		RedisTradeTTL:             mustDuration("REDIS_TRADE_TTL", "10m"),
		RedisPricesTTL:            mustDuration("REDIS_PRICES_TTL", "15s"),
		RedisKeyPrefix:            getEnv("REDIS_KEY_PREFIX", "bnbfm:"),
		MyTradesMaxConcurrency:    mustInt("MYTRADES_MAX_CONCURRENCY", 1),
		MyTradesMinInterval:       mustDuration("MYTRADES_MIN_INTERVAL", "400ms"),
		SQLiteEnabled:             mustBool("SQLITE_ENABLED", false),
		SQLitePath:                getEnv("SQLITE_PATH", "./data/trades.db"),
		SQLiteInitialLookbackDays: mustInt("SQLITE_INITIAL_LOOKBACK_DAYS", 7),
		SQLiteSyncInterval:        mustDuration("SQLITE_SYNC_INTERVAL", "5m"),
		SQLiteMaxLookbackDays:     mustInt("SQLITE_MAX_LOOKBACK_DAYS", 30),
		FreqtradeAPIURL:           getEnv("FREQTRADE_API_URL", ""),
		FreqtradeUsername:         getEnv("FREQTRADE_USERNAME", ""),
		FreqtradePassword:         getEnv("FREQTRADE_PASSWORD", ""),
		FreqtradeTradesLimit:      mustInt("FREQTRADE_TRADES_LIMIT", 500),
		FreqtradeMaxPages:         mustInt("FREQTRADE_MAX_PAGES", 20),
	}

	cfg.BinanceAPIKey = strings.TrimSpace(os.Getenv("BINANCE_API_KEY"))
	cfg.BinanceAPISecret = strings.TrimSpace(os.Getenv("BINANCE_API_SECRET"))
	cfg.TelegramToken = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	cfg.TelegramChatID = strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
	cfg.MinBNB = mustFloat("MIN_BNB_THRESHOLD", 0)
	cfg.TargetBNB = mustFloat("TARGET_BNB", 0)
	cfg.MinBNBUSDT = mustFloat("MIN_BNB_THRESHOLD_USDT", 0)
	cfg.TargetBNBUSDT = mustFloat("TARGET_BNB_USDT", 0)
	cfg.BNBRatioMin = mustFloat("BNB_RATIO_MIN", 0)
	cfg.BNBRatioTarget = mustFloat("BNB_RATIO_TARGET", 0)
	cfg.MaxBuyQuote = mustFloat("MAX_BUY_QUOTE", 25)

	if cfg.BinanceAPIKey == "" || cfg.BinanceAPISecret == "" {
		return Config{}, errors.New("BINANCE_API_KEY and BINANCE_API_SECRET are required")
	}
	if cfg.useRatioThresholds() {
		if cfg.BNBRatioMin <= 0 || cfg.BNBRatioTarget <= 0 {
			return Config{}, errors.New("BNB_RATIO_MIN and BNB_RATIO_TARGET must both be > 0 when BNB_RATIO_MODE=true")
		}
		if cfg.BNBRatioTarget < cfg.BNBRatioMin {
			return Config{}, errors.New("BNB_RATIO_TARGET must be >= BNB_RATIO_MIN")
		}
		if cfg.BNBRatioTarget > 1 {
			return Config{}, errors.New("BNB_RATIO_TARGET must be <= 1")
		}
	} else if cfg.useUSDTThresholds() {
		if cfg.MinBNBUSDT <= 0 || cfg.TargetBNBUSDT <= 0 {
			return Config{}, errors.New("MIN_BNB_THRESHOLD_USDT and TARGET_BNB_USDT must both be > 0 when using USDT thresholds")
		}
		if cfg.TargetBNBUSDT < cfg.MinBNBUSDT {
			return Config{}, errors.New("TARGET_BNB_USDT must be >= MIN_BNB_THRESHOLD_USDT")
		}
	} else {
		if cfg.MinBNB <= 0 {
			return Config{}, errors.New("MIN_BNB_THRESHOLD must be > 0")
		}
		if cfg.TargetBNB < cfg.MinBNB {
			return Config{}, errors.New("TARGET_BNB must be >= MIN_BNB_THRESHOLD")
		}
	}
	if cfg.MaxBuyQuote <= 0 {
		return Config{}, errors.New("MAX_BUY_QUOTE must be > 0")
	}
	if cfg.SummaryEveryChecks < 0 {
		return Config{}, errors.New("SUMMARY_EVERY_CHECKS must be >= 0")
	}
	if cfg.DailyDigestTrades <= 0 {
		cfg.DailyDigestTrades = 3
	}
	if cfg.DailyReportMode != "full" && cfg.DailyReportMode != "digest" {
		return Config{}, errors.New("DAILY_REPORT_MODE must be 'full' or 'digest'")
	}
	if cfg.FeeMainCurrency != "BNB" && cfg.FeeMainCurrency != "USDT" {
		return Config{}, errors.New("FEE_MAIN_CURRENCY must be BNB or USDT")
	}
	if cfg.HeartbeatCheckInterval <= 0 {
		cfg.HeartbeatCheckInterval = time.Minute
	}
	if cfg.HeartbeatStaleAfter <= 0 {
		cfg.HeartbeatStaleAfter = 10 * time.Minute
	}
	if cfg.APIFailureThreshold <= 0 {
		cfg.APIFailureThreshold = 3
	}
	if cfg.APIFailureAlertCooldown <= 0 {
		cfg.APIFailureAlertCooldown = 15 * time.Minute
	}
	if cfg.APILatencySpikeThreshold <= 0 {
		cfg.APILatencySpikeThreshold = 3
	}
	if cfg.AbnormalMoveDrop1hPct < 0 || cfg.AbnormalMoveDrop24hPct < 0 {
		return Config{}, errors.New("ABNORMAL_MOVE thresholds must be >= 0")
	}
	if cfg.AbnormalMoveAlertCooldown <= 0 {
		cfg.AbnormalMoveAlertCooldown = 2 * time.Hour
	}
	if cfg.AccountReserveRatio <= 0 || cfg.AccountReserveRatio > 1 {
		return Config{}, errors.New("ACCOUNT_RESERVE_RATIO must be > 0 and <= 1")
	}
	if cfg.TelegramToken == "" || cfg.TelegramChatID == "" {
		return Config{}, errors.New("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required for button monitoring")
	}
	if len(cfg.TrackedSymbols) == 0 {
		return Config{}, errors.New("TRACKED_SYMBOLS must have at least one symbol")
	}
	if len(cfg.TrackedSymbols) == 1 && cfg.TrackedSymbols[0] == "FREQTRADE" {
		cfg.FreqtradeHistoryMode = true
		if strings.TrimSpace(cfg.FreqtradeAPIURL) == "" {
			return Config{}, errors.New("FREQTRADE_API_URL is required when TRACKED_SYMBOLS=FREQTRADE")
		}
		if strings.TrimSpace(cfg.FreqtradeUsername) == "" || strings.TrimSpace(cfg.FreqtradePassword) == "" {
			return Config{}, errors.New("FREQTRADE_USERNAME and FREQTRADE_PASSWORD are required when TRACKED_SYMBOLS=FREQTRADE")
		}
		if cfg.FreqtradeTradesLimit <= 0 {
			cfg.FreqtradeTradesLimit = 500
		}
		if cfg.FreqtradeTradesLimit > 500 {
			cfg.FreqtradeTradesLimit = 500
		}
		if cfg.FreqtradeMaxPages <= 0 {
			cfg.FreqtradeMaxPages = 20
		}
	}
	if cfg.MaxAutoTrackedSymbols <= 0 {
		cfg.MaxAutoTrackedSymbols = 40
	}
	if cfg.MyTradesMaxConcurrency <= 0 {
		cfg.MyTradesMaxConcurrency = 1
	}
	if cfg.MyTradesMinInterval <= 0 {
		cfg.MyTradesMinInterval = 400 * time.Millisecond
	}
	if cfg.SQLiteEnabled {
		if strings.TrimSpace(cfg.SQLitePath) == "" {
			return Config{}, errors.New("SQLITE_PATH is required when SQLITE_ENABLED=true")
		}
		if cfg.SQLiteInitialLookbackDays <= 0 {
			cfg.SQLiteInitialLookbackDays = 7
		}
		if cfg.SQLiteSyncInterval <= 0 {
			cfg.SQLiteSyncInterval = 5 * time.Minute
		}
		if cfg.SQLiteMaxLookbackDays <= 0 {
			cfg.SQLiteMaxLookbackDays = 30
		}
		if cfg.SQLiteMaxLookbackDays > 30 {
			cfg.SQLiteMaxLookbackDays = 30
		}
		if cfg.SQLiteInitialLookbackDays > cfg.SQLiteMaxLookbackDays {
			cfg.SQLiteInitialLookbackDays = cfg.SQLiteMaxLookbackDays
		}
	}
	if cfg.MaxSnapshots < 100 {
		cfg.MaxSnapshots = 100
	}
	if cfg.RedisTradeTTL <= 0 {
		return Config{}, errors.New("REDIS_TRADE_TTL must be > 0")
	}
	if cfg.RedisPricesTTL <= 0 {
		return Config{}, errors.New("REDIS_PRICES_TTL must be > 0")
	}
	if strings.TrimSpace(cfg.RedisKeyPrefix) == "" {
		cfg.RedisKeyPrefix = "bnbfm:"
	}
	if _, _, err := parseHHMM(cfg.DailyReportTimeUTC); err != nil {
		return Config{}, fmt.Errorf("DAILY_REPORT_TIME_UTC invalid: %w", err)
	}
	if !strings.EqualFold(cfg.DailyReportTimezone, "AUTO") && !strings.EqualFold(cfg.DailyReportTimezone, "AUTO_IP") {
		if _, err := time.LoadLocation(cfg.DailyReportTimezone); err != nil {
			return Config{}, fmt.Errorf("DAILY_REPORT_TIMEZONE invalid: %w", err)
		}
	}
	return cfg, nil
}

func parseSymbols(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		s := strings.ToUpper(strings.TrimSpace(p))
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func getEnv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func mustInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Fatalf("invalid int %s=%s", key, raw)
	}
	return v
}

func mustInt64(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		log.Fatalf("invalid int64 %s=%s", key, raw)
	}
	return v
}

func mustFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		log.Fatalf("invalid float %s=%s", key, raw)
	}
	return v
}

func mustDuration(key, fallback string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		raw = fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		log.Fatalf("invalid duration %s=%s", key, raw)
	}
	return d
}

func mustBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		log.Fatalf("invalid bool %s=%s", key, raw)
	}
	return v
}

func formatFloat(v float64, precision int) string {
	s := strconv.FormatFloat(v, 'f', precision, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" {
		return "0"
	}
	return s
}
