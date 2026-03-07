package services

import (
	"fmt"
	"time"
)

type PnLSinceFn func(d time.Duration) (pnl float64, pct float64, ok bool)
type SendDedupFn func(key string, cooldown time.Duration, msg string)

func CheckAbnormalMoveAlerts(enabled bool, quoteAsset string, drop1hPct float64, drop24hPct float64, cooldown time.Duration, pnlSince PnLSinceFn, sendDedup SendDedupFn) {
	if !enabled || pnlSince == nil || sendDedup == nil {
		return
	}
	if drop1hPct > 0 {
		pnl, pct, ok := pnlSince(time.Hour)
		if ok && pct <= -drop1hPct {
			sendDedup(
				"move_drop_1h",
				cooldown,
				fmt.Sprintf("Abnormal move alert: 1h portfolio change %.2f%% (%.4f %s)", pct, pnl, quoteAsset),
			)
		}
	}
	if drop24hPct > 0 {
		pnl, pct, ok := pnlSince(24 * time.Hour)
		if ok && pct <= -drop24hPct {
			sendDedup(
				"move_drop_24h",
				cooldown,
				fmt.Sprintf("Abnormal move alert: 24h portfolio change %.2f%% (%.4f %s)", pct, pnl, quoteAsset),
			)
		}
	}
}
