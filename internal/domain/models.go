package domain

import "time"

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

type RangeRecord struct {
	FromTS int64 `json:"from_ts"`
	ToTS   int64 `json:"to_ts"`
}
