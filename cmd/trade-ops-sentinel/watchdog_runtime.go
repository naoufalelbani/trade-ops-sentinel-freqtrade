package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

func heartbeatLoop(ctx context.Context, cfg Config, alerts *alertManager, state *MonitorState) {
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

			// Check for scheduled Freqtrade restart
			restartAt := state.getFreqtradeRestartAt()
			if !restartAt.IsZero() && time.Now().UTC().After(restartAt) {
				log.Printf("watchdog: triggering scheduled Freqtrade restart (due at %s)", restartAt.Format(time.RFC3339))
				state.setFreqtradeRestartAt(time.Time{}) // Clear it
				_ = state.save()

				if err := startFreqtrade(ctx, cfg); err != nil {
					log.Printf("watchdog: freqtrade restart failed: %v", err)
					alerts.notifier.Send(fmt.Sprintf("⚠️ <b>Scheduled Freqtrade restart failed</b>: %v", err), defaultKeyboard())
				} else {
					log.Printf("watchdog: freqtrade restart success")
					alerts.notifier.Send("✅ <b>Scheduled Freqtrade restart successful</b>. Bot should be running now.", defaultKeyboard())
				}
			}

			if strings.TrimSpace(cfg.FreqtradeAPIURL) != "" {
				ftState, err := fetchFreqtradeState(ctx, cfg)
				if err != nil {
					alerts.observeAPICall("freqtrade.state", 0, err)
				} else {
					alerts.observeAPICall("freqtrade.state", 0, nil)
					alerts.observeFreqtradeState(ftState)
				}
			}
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
		return fmt.Errorf("heartbeat ping http=%d body=%s", res.StatusCode, sanitizeHTTPErrorBody(body, 200))
	}
	return nil
}

func pingFreqtrade(ctx context.Context, endpoint, username, password string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return fmt.Errorf("http=%d", res.StatusCode)
	}
	return nil
}
