package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	log.Printf("trade-ops-sentinel %s", versionSummary())
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
	chartTheme := state.getChartTheme("dark")
	state.setChartTheme(chartTheme)
	chartSize := state.getChartSize("standard")
	state.setChartSize(chartSize)
	chartLabelsEnabled := state.getChartLabelsEnabled(true)
	state.setChartLabelsEnabled(chartLabelsEnabled)
	chartGridEnabled := state.getChartGridEnabled(true)
	state.setChartGridEnabled(chartGridEnabled)
	pnlEmojisEnabled := state.getPnLEmojisEnabled(true)
	state.setPnLEmojisEnabled(pnlEmojisEnabled)
	heartbeatAlertsEnabled := state.getHeartbeatAlertsEnabled(cfg.HeartbeatAlertEnabled)
	apiFailureAlertsEnabled := state.getAPIFailureAlertsEnabled(cfg.APIFailureAlertEnabled)
	state.setHeartbeatAlertsEnabled(heartbeatAlertsEnabled)
	state.setAPIFailureAlertsEnabled(apiFailureAlertsEnabled)
	if runtimeAlerts != nil {
		runtimeAlerts.setHeartbeatAlertsEnabled(heartbeatAlertsEnabled)
		runtimeAlerts.setAPIFailureAlertsEnabled(apiFailureAlertsEnabled)
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
		"<b>Trade Ops Sentinel started</b>\nVersion=<i>%s</i>\n<b>Symbol</b>=<code>%s</code>\n<i>%s</i>\n<b>Tracked symbols</b>=%d\n<b>Interval</b>=%s\n<b>Container</b>=<code>%s</code> <b>Restarts</b>=%d",
		versionSummary(),
		cfg.Symbol,
		cfg.thresholdModeLine(),
		len(cfg.TrackedSymbols),
		cfg.CheckInterval,
		strings.TrimSpace(orDefault(os.Getenv("HOSTNAME"), "trade-ops-sentinel")),
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
