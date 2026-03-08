package main

import (
	"database/sql"
	"net/http"
	"sync"
	"time"
	"trade-ops-sentinel/internal/domain"

	"github.com/redis/go-redis/v9"
)

type feeSummary struct {
	Day   float64
	Week  float64
	Month float64
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
	HeartbeatAlertEnabled  bool
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

type Snapshot = domain.Snapshot
type RefillEvent = domain.RefillEvent
type rangeRecord = domain.RangeRecord

type persistState struct {
	Checks                  int           `json:"checks"`
	StartCount              int           `json:"start_count"`
	LastBuyAt               int64         `json:"last_buy_at"`
	Snapshots               []Snapshot    `json:"snapshots"`
	RefillEvents            []RefillEvent `json:"refill_events"`
	FeeCurrency             string        `json:"fee_currency"`
	ChartTheme              string        `json:"chart_theme,omitempty"`
	ChartSize               string        `json:"chart_size,omitempty"`
	ChartLabelsEnabled      *bool         `json:"chart_labels_enabled,omitempty"`
	ChartGridEnabled        *bool         `json:"chart_grid_enabled,omitempty"`
	PnLEmojisEnabled        *bool         `json:"pnl_emojis_enabled,omitempty"`
	HeartbeatAlertsEnabled  *bool         `json:"heartbeat_alerts_enabled,omitempty"`
	APIFailureAlertsEnabled *bool         `json:"api_failure_alerts_enabled,omitempty"`
	CustomCumWin            []string      `json:"custom_cum_windows,omitempty"`
	CustomRanges            []rangeRecord `json:"custom_ranges,omitempty"`
	LastUpdated             int64         `json:"last_updated"`
}

type MonitorState struct {
	mu                         sync.Mutex
	checks                     int
	startCount                 int
	lastBuyAt                  time.Time
	snapshots                  []Snapshot
	refillEvents               []RefillEvent
	feeCurrency                string
	chartTheme                 string
	chartSize                  string
	chartLabelsEnabled         bool
	hasChartLabelsEnabled      bool
	chartGridEnabled           bool
	hasChartGridEnabled        bool
	pnlEmojisEnabled           bool
	hasPnLEmojisEnabled        bool
	heartbeatAlertsEnabled     bool
	hasHeartbeatAlertsEnabled  bool
	apiFailureAlertsEnabled    bool
	hasAPIFailureAlertsEnabled bool
	customCumWin               []string
	customRanges               []rangeRecord
	stateFile                  string
	maxSnapshots               int
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

type TelegramNotifier struct {
	token      string
	chatID     string
	baseURL    string
	httpClient *http.Client
}
