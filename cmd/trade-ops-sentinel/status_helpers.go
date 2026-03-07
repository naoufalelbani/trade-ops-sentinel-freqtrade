package main

import (
	"trade-ops-sentinel/internal/domain"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

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
		"Status\nBNB: %s\n%s: %s\n%s: %.4f\nPortfolio: %s\n\n%s\nRefills: D=%d W=%d M=%d\n%s\n%s\n\nPnL\n%s\n%s\n%s",
		formatBNBWithQuote(bnbFree, price, cfg),
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
	return c.toThresholdPolicy().UseUSDTThresholds()
}

func (c Config) useRatioThresholds() bool {
	return c.toThresholdPolicy().UseRatioThresholds()
}

func (c Config) resolveBNBThresholds(price, portfolioQuote float64) (float64, float64, error) {
	return domain.ResolveBNBThresholds(price, portfolioQuote, c.toThresholdPolicy())
}

func (c Config) thresholdModeLine() string {
	return domain.ThresholdModeLine(c.toThresholdPolicy())
}

func (c Config) toThresholdPolicy() domain.ThresholdPolicy {
	return domain.ThresholdPolicy{
		MinBNB:         c.MinBNB,
		TargetBNB:      c.TargetBNB,
		MinBNBUSDT:     c.MinBNBUSDT,
		TargetBNBUSDT:  c.TargetBNBUSDT,
		BNBRatioMode:   c.BNBRatioMode,
		BNBRatioMin:    c.BNBRatioMin,
		BNBRatioTarget: c.BNBRatioTarget,
		QuoteAsset:     c.QuoteAsset,
	}
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
