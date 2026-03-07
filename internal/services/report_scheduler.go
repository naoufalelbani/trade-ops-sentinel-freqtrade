package services

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func ResolveDailyTimezone(ctx context.Context, configured string, fetchAuto func(context.Context) (string, error)) (*time.Location, string, error) {
	raw := strings.TrimSpace(configured)
	if raw == "" {
		return time.UTC, "UTC", nil
	}
	if strings.EqualFold(raw, "AUTO") || strings.EqualFold(raw, "AUTO_IP") {
		tzName, err := fetchAuto(ctx)
		if err != nil {
			return time.UTC, "UTC", fmt.Errorf("timezone auto-detect failed: %w", err)
		}
		loc, err := time.LoadLocation(tzName)
		if err != nil {
			return time.UTC, "UTC", fmt.Errorf("timezone load failed: %w", err)
		}
		return loc, tzName, nil
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		return time.UTC, "UTC", fmt.Errorf("timezone load failed: %w", err)
	}
	return loc, raw, nil
}

func NextDailyRun(now time.Time, hour, minute int, loc *time.Location) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}
