package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

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
		HeartbeatAlertEnabled:     mustBool("HEARTBEAT_ALERT_ENABLED", true),
		HeartbeatStaleAfter:       mustDuration("HEARTBEAT_STALE_AFTER", "10m"),
		HeartbeatCheckInterval:    mustDuration("HEARTBEAT_CHECK_INTERVAL", "1m"),
		HeartbeatPingURL:          getEnv("HEARTBEAT_PING_URL", ""),
		APIFailureAlertEnabled:    mustBool("API_FAILURE_ALERT_ENABLED", true),
		APIFailureThreshold:       mustInt("API_FAILURE_THRESHOLD", 3),
		APIFailureAlertCooldown:   mustDuration("API_FAILURE_ALERT_COOLDOWN", "10m"),
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
		FreqtradeAlertOnStopped:   mustBool("FREQTRADE_ALERT_ON_STOPPED", true),
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
	if cfg.UseRatioThresholds() {
		if cfg.BNBRatioMin <= 0 || cfg.BNBRatioTarget <= 0 {
			return Config{}, errors.New("BNB_RATIO_MIN and BNB_RATIO_TARGET must both be > 0 when BNB_RATIO_MODE=true")
		}
		if cfg.BNBRatioTarget < cfg.BNBRatioMin {
			return Config{}, errors.New("BNB_RATIO_TARGET must be >= BNB_RATIO_MIN")
		}
		if cfg.BNBRatioTarget > 1 {
			return Config{}, errors.New("BNB_RATIO_TARGET must be <= 1")
		}
	} else if cfg.UseUSDTThresholds() {
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
	if err := requireHTTPSBaseURL("BINANCE_BASE_URL", cfg.BinanceBaseURL); err != nil {
		return Config{}, err
	}
	if err := requireHTTPSBaseURL("TELEGRAM_BASE_URL", cfg.TelegramBaseURL); err != nil {
		return Config{}, err
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

func requireHTTPSBaseURL(name, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s invalid URL: %q", name, raw)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return fmt.Errorf("%s must use https", name)
	}
	return nil
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
