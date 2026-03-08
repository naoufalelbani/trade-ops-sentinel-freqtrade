package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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

func (s *MonitorState) getChartTheme(defaultVal string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := strings.ToLower(strings.TrimSpace(s.chartTheme))
	if v == "dark" || v == "light" {
		return v
	}
	d := strings.ToLower(strings.TrimSpace(defaultVal))
	if d == "light" {
		return "light"
	}
	return "dark"
}

func (s *MonitorState) setChartTheme(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := strings.ToLower(strings.TrimSpace(v))
	if t != "dark" && t != "light" {
		return
	}
	s.chartTheme = t
}

func (s *MonitorState) getChartSize(defaultVal string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := strings.ToLower(strings.TrimSpace(s.chartSize))
	if v == "compact" || v == "standard" || v == "wide" {
		return v
	}
	d := strings.ToLower(strings.TrimSpace(defaultVal))
	if d == "compact" || d == "wide" {
		return d
	}
	return "standard"
}

func (s *MonitorState) setChartSize(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := strings.ToLower(strings.TrimSpace(v))
	if t != "compact" && t != "standard" && t != "wide" {
		return
	}
	s.chartSize = t
}

func (s *MonitorState) getChartLabelsEnabled(defaultVal bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasChartLabelsEnabled {
		return s.chartLabelsEnabled
	}
	return defaultVal
}

func (s *MonitorState) setChartLabelsEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chartLabelsEnabled = v
	s.hasChartLabelsEnabled = true
}

func (s *MonitorState) getChartGridEnabled(defaultVal bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasChartGridEnabled {
		return s.chartGridEnabled
	}
	return defaultVal
}

func (s *MonitorState) setChartGridEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chartGridEnabled = v
	s.hasChartGridEnabled = true
}

func (s *MonitorState) getPnLEmojisEnabled(defaultVal bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasPnLEmojisEnabled {
		return s.pnlEmojisEnabled
	}
	return defaultVal
}

func (s *MonitorState) setPnLEmojisEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pnlEmojisEnabled = v
	s.hasPnLEmojisEnabled = true
}

func (s *MonitorState) getHeartbeatAlertsEnabled(defaultVal bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasHeartbeatAlertsEnabled {
		return s.heartbeatAlertsEnabled
	}
	return defaultVal
}

func (s *MonitorState) setHeartbeatAlertsEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeatAlertsEnabled = v
	s.hasHeartbeatAlertsEnabled = true
}

func (s *MonitorState) getAPIFailureAlertsEnabled(defaultVal bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasAPIFailureAlertsEnabled {
		return s.apiFailureAlertsEnabled
	}
	return defaultVal
}

func (s *MonitorState) setAPIFailureAlertsEnabled(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiFailureAlertsEnabled = v
	s.hasAPIFailureAlertsEnabled = true
}

func (s *MonitorState) settingsSummary(cfg Config, alerts *alertManager) string {
	displayCurrency := s.getDisplayCurrency(cfg.FeeMainCurrency)
	chartTheme := strings.Title(s.getChartTheme("dark"))
	chartSize := strings.Title(s.getChartSize("standard"))
	chartLabelsEnabled := s.getChartLabelsEnabled(true)
	chartGridEnabled := s.getChartGridEnabled(true)
	pnlEmojisEnabled := s.getPnLEmojisEnabled(true)
	heartbeatEnabled := cfg.HeartbeatAlertEnabled
	apiEnabled := cfg.APIFailureAlertEnabled
	if alerts != nil {
		heartbeatEnabled = alerts.heartbeatAlertsOn()
		apiEnabled = alerts.apiFailureAlertsOn()
	} else {
		heartbeatEnabled = s.getHeartbeatAlertsEnabled(cfg.HeartbeatAlertEnabled)
		apiEnabled = s.getAPIFailureAlertsEnabled(cfg.APIFailureAlertEnabled)
	}
	return strings.Join([]string{
		"Settings Summary",
		fmt.Sprintf("Currency=%s", displayCurrency),
		fmt.Sprintf("Chart theme=%s", chartTheme),
		fmt.Sprintf("Chart size=%s", chartSize),
		fmt.Sprintf("Chart labels=%t", chartLabelsEnabled),
		fmt.Sprintf("Chart grid=%t", chartGridEnabled),
		fmt.Sprintf("PnL emojis=%t", pnlEmojisEnabled),
		fmt.Sprintf("Heartbeat alerts=%t", heartbeatEnabled),
		fmt.Sprintf("API failure alerts=%t", apiEnabled),
	}, "\n")
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

func (s *MonitorState) pnlSeriesLastNMinutes(minutes int) ([]string, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.snapshots) < 2 {
		return nil, nil
	}
	buckets := map[string]Snapshot{}
	for _, sn := range s.snapshots {
		k := time.UnixMilli(sn.TS).UTC().Truncate(time.Minute).Format("01-02 15:04")
		prev, ok := buckets[k]
		if !ok || sn.TS > prev.TS {
			buckets[k] = sn
		}
	}
	now := time.Now().UTC().Truncate(time.Minute)
	labels := make([]string, 0, minutes)
	values := make([]float64, 0, minutes)
	var prev *Snapshot
	for i := minutes - 1; i >= 0; i-- {
		k := now.Add(-time.Duration(i) * time.Minute).Format("01-02 15:04")
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

func (s *MonitorState) pnlSeriesByMinuteRangeActive(start, end time.Time) ([]string, []float64) {
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
		k := t.Truncate(time.Minute)
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
		labels = append(labels, k.Format("01-02 15:04"))
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
		ChartTheme:   s.chartTheme,
		ChartSize:    s.chartSize,
		CustomCumWin: append([]string(nil), s.customCumWin...),
		CustomRanges: append([]rangeRecord(nil), s.customRanges...),
	}
	if s.hasChartLabelsEnabled {
		v := s.chartLabelsEnabled
		p.ChartLabelsEnabled = &v
	}
	if s.hasChartGridEnabled {
		v := s.chartGridEnabled
		p.ChartGridEnabled = &v
	}
	if s.hasPnLEmojisEnabled {
		v := s.pnlEmojisEnabled
		p.PnLEmojisEnabled = &v
	}
	if s.hasHeartbeatAlertsEnabled {
		v := s.heartbeatAlertsEnabled
		p.HeartbeatAlertsEnabled = &v
	}
	if s.hasAPIFailureAlertsEnabled {
		v := s.apiFailureAlertsEnabled
		p.APIFailureAlertsEnabled = &v
	}
	if !s.lastBuyAt.IsZero() {
		p.LastBuyAt = s.lastBuyAt.UnixMilli()
	}

	if err := os.MkdirAll(filepath.Dir(s.stateFile), 0o700); err != nil {
		return err
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.stateFile, b, 0o600)
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
	s.chartTheme = strings.ToLower(strings.TrimSpace(p.ChartTheme))
	s.chartSize = strings.ToLower(strings.TrimSpace(p.ChartSize))
	if p.ChartLabelsEnabled != nil {
		s.hasChartLabelsEnabled = true
		s.chartLabelsEnabled = *p.ChartLabelsEnabled
	}
	if p.ChartGridEnabled != nil {
		s.hasChartGridEnabled = true
		s.chartGridEnabled = *p.ChartGridEnabled
	}
	if p.PnLEmojisEnabled != nil {
		s.hasPnLEmojisEnabled = true
		s.pnlEmojisEnabled = *p.PnLEmojisEnabled
	}
	if p.HeartbeatAlertsEnabled != nil {
		s.hasHeartbeatAlertsEnabled = true
		s.heartbeatAlertsEnabled = *p.HeartbeatAlertsEnabled
	}
	if p.APIFailureAlertsEnabled != nil {
		s.hasAPIFailureAlertsEnabled = true
		s.apiFailureAlertsEnabled = *p.APIFailureAlertsEnabled
	}
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
