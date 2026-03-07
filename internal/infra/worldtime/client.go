package worldtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func FetchTimezoneByIP(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://worldtimeapi.org/api/ip", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("worldtimeapi http=%d body=%s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Timezone string `json:"timezone"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Timezone) == "" {
		return "", errors.New("empty timezone from worldtimeapi")
	}
	return payload.Timezone, nil
}
