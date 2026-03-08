package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
		return fmt.Errorf("heartbeat ping http=%d body=%s", res.StatusCode, sanitizeHTTPErrorBody(body, 200))
	}
	return nil
}
