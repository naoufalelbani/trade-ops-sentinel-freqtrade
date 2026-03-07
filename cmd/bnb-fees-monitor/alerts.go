package main

import (
    "fmt"
    "os"
    "sort"
    "strconv"
    "strings"
    "sync"
    "time"
)

var runtimeAlerts *alertManager

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
