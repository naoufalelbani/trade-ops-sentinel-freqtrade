package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (b *BinanceClient) GetFreeBalances(ctx context.Context) (map[string]float64, error) {
	vals := url.Values{}
	body, err := b.signedRequest(ctx, http.MethodGet, "/api/v3/account", vals)
	if err != nil {
		return nil, err
	}

	var resp accountResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode account response: %w", err)
	}

	out := make(map[string]float64, len(resp.Balances))
	for _, bal := range resp.Balances {
		free, _ := strconv.ParseFloat(bal.Free, 64)
		locked, _ := strconv.ParseFloat(bal.Locked, 64)
		total := free + locked
		if total <= 0 {
			continue
		}
		out[bal.Asset] = total
	}
	return out, nil
}

func (b *BinanceClient) EstimatePortfolioQuote(ctx context.Context, balances map[string]float64, quoteAsset string) (float64, error) {
	priceMap, err := b.GetAllPrices(ctx)
	if err != nil {
		return 0, err
	}

	total := 0.0
	for asset, amount := range balances {
		if amount <= 0 {
			continue
		}
		if asset == quoteAsset {
			total += amount
			continue
		}

		direct := asset + quoteAsset
		if px, ok := priceMap[direct]; ok && px > 0 {
			total += amount * px
			continue
		}
		inverse := quoteAsset + asset
		if px, ok := priceMap[inverse]; ok && px > 0 {
			total += amount / px
			continue
		}

		// Approximate USD stablecoins as 1:1 when quote is USDT.
		if quoteAsset == "USDT" && isUSDStable(asset) {
			total += amount
		}
	}
	return total, nil
}

func isUSDStable(asset string) bool {
	switch asset {
	case "USDT", "USDC", "BUSD", "TUSD", "FDUSD", "USDP", "DAI":
		return true
	default:
		return false
	}
}

func (b *BinanceClient) GetPrice(ctx context.Context, symbol string) (float64, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/ticker/price", vals)
	if err != nil {
		return 0, err
	}
	var resp priceResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode price response: %w", err)
	}
	p, err := strconv.ParseFloat(resp.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("parse price: %w", err)
	}
	return p, nil
}

func (b *BinanceClient) GetAllPrices(ctx context.Context) (map[string]float64, error) {
	cacheKey := "prices:all"
	var cached map[string]float64
	if ok, err := b.cache.getJSON(ctx, cacheKey, &cached); err == nil && ok {
		return cached, nil
	}

	vals := url.Values{}
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/ticker/price", vals)
	if err != nil {
		return nil, err
	}

	var rows []priceResponse
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("decode prices response: %w", err)
	}

	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		p, err := strconv.ParseFloat(row.Price, 64)
		if err != nil || p <= 0 {
			continue
		}
		out[row.Symbol] = p
	}
	_ = b.cache.setJSON(ctx, cacheKey, out, b.pricesTTL)
	return out, nil
}

func (b *BinanceClient) GetMinNotional(ctx context.Context, symbol string) (float64, error) {
	b.mu.Lock()
	if b.loadedMin {
		v := b.minNotional
		b.mu.Unlock()
		return v, nil
	}
	b.mu.Unlock()

	vals := url.Values{}
	vals.Set("symbol", symbol)
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/exchangeInfo", vals)
	if err != nil {
		return 0, err
	}

	var resp exchangeInfoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, fmt.Errorf("decode exchangeInfo response: %w", err)
	}
	if len(resp.Symbols) == 0 {
		return 0, errors.New("symbol not found in exchangeInfo")
	}

	minNotional := 0.0
	for _, f := range resp.Symbols[0].Filters {
		if f.FilterType == "MIN_NOTIONAL" || f.FilterType == "NOTIONAL" {
			v, err := strconv.ParseFloat(f.MinNotional, 64)
			if err == nil && v > 0 {
				minNotional = v
				break
			}
		}
	}

	b.mu.Lock()
	b.minNotional = minNotional
	b.loadedMin = true
	b.mu.Unlock()

	return minNotional, nil
}

func (b *BinanceClient) ListTradingSymbolsByQuote(ctx context.Context, quoteAsset string) ([]string, error) {
	vals := url.Values{}
	body, err := b.publicRequest(ctx, http.MethodGet, "/api/v3/exchangeInfo", vals)
	if err != nil {
		return nil, err
	}
	var resp exchangeInfoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode exchangeInfo response: %w", err)
	}
	out := make([]string, 0, len(resp.Symbols))
	quote := strings.ToUpper(strings.TrimSpace(quoteAsset))
	for _, s := range resp.Symbols {
		if s.Status != "TRADING" {
			continue
		}
		if strings.ToUpper(s.QuoteAsset) != quote {
			continue
		}
		out = append(out, s.Symbol)
	}
	sort.Strings(out)
	return out, nil
}

func (b *BinanceClient) GetMyTrades(ctx context.Context, symbol string, startTimeMS, endTimeMS int64) ([]myTrade, error) {
	if endTimeMS < startTimeMS {
		return nil, errors.New("endTime must be >= startTime")
	}
	const maxWindowMS int64 = 24*60*60*1000 - 1

	all := make([]myTrade, 0, 128)
	windowStart := startTimeMS
	for windowStart <= endTimeMS {
		windowEnd := windowStart + maxWindowMS
		if windowEnd > endTimeMS {
			windowEnd = endTimeMS
		}

		batch, err := b.getMyTradesWindow(ctx, symbol, windowStart, windowEnd)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)

		if windowEnd == endTimeMS {
			break
		}
		windowStart = windowEnd + 1
	}

	// Ensure deterministic order and remove any potential duplicates by trade id.
	sort.Slice(all, func(i, j int) bool { return all[i].Time < all[j].Time })
	uniq := make([]myTrade, 0, len(all))
	seen := make(map[int64]struct{}, len(all))
	for _, tr := range all {
		if _, ok := seen[tr.ID]; ok {
			continue
		}
		seen[tr.ID] = struct{}{}
		uniq = append(uniq, tr)
	}
	return uniq, nil
}

func (b *BinanceClient) getMyTradesWindow(ctx context.Context, symbol string, startTimeMS, endTimeMS int64) ([]myTrade, error) {
	cacheKey := fmt.Sprintf("trades:%s:%d:%d", symbol, startTimeMS, endTimeMS)
	var cached []myTrade
	if ok, err := b.cache.getJSON(ctx, cacheKey, &cached); err == nil && ok {
		log.Printf("mytrades cache hit symbol=%s start=%d end=%d count=%d", symbol, startTimeMS, endTimeMS, len(cached))
		return cached, nil
	}

	all := make([]myTrade, 0, 128)
	fromID := int64(-1)

	for page := 0; page < 10; page++ {
		// Global limiter for /myTrades to avoid hitting Binance request-weight bans.
		b.myTradesSem <- struct{}{}
		if err := b.waitMyTradesSlot(); err != nil {
			<-b.myTradesSem
			return nil, err
		}
		vals := url.Values{}
		vals.Set("symbol", symbol)
		vals.Set("limit", "1000")
		vals.Set("startTime", strconv.FormatInt(startTimeMS, 10))
		vals.Set("endTime", strconv.FormatInt(endTimeMS, 10))
		if fromID >= 0 {
			vals.Set("fromId", strconv.FormatInt(fromID, 10))
		}

		reqStarted := time.Now()
		body, err := b.signedRequest(ctx, http.MethodGet, "/api/v3/myTrades", vals)
		<-b.myTradesSem
		if err != nil {
			return nil, err
		}

		var batch []myTrade
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, fmt.Errorf("decode myTrades response: %w", err)
		}
		log.Printf(
			"mytrades fetch symbol=%s start=%d end=%d page=%d fromId=%d batch=%d duration_ms=%d",
			symbol,
			startTimeMS,
			endTimeMS,
			page+1,
			fromID,
			len(batch),
			time.Since(reqStarted).Milliseconds(),
		)
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			batch[i].Symbol = symbol
		}

		all = append(all, batch...)
		if len(batch) < 1000 {
			break
		}
		fromID = batch[len(batch)-1].ID + 1
	}
	_ = b.cache.setJSON(ctx, cacheKey, all, b.tradeTTL)
	return all, nil
}

func (b *BinanceClient) waitMyTradesSlot() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.banUntil.After(now) {
		return fmt.Errorf("binance temporary ban active until %s", b.banUntil.UTC().Format(time.RFC3339))
	}
	if b.myTradesMinInterval <= 0 {
		return nil
	}
	next := b.lastMyTradesRequest.Add(b.myTradesMinInterval)
	if next.After(now) {
		time.Sleep(next.Sub(now))
	}
	b.lastMyTradesRequest = time.Now()
	return nil
}

func (b *BinanceClient) MarketBuyByQuote(ctx context.Context, symbol string, quoteAmount float64) (orderResponse, error) {
	vals := url.Values{}
	vals.Set("symbol", symbol)
	vals.Set("side", "BUY")
	vals.Set("type", "MARKET")
	vals.Set("quoteOrderQty", formatFloat(quoteAmount, 8))

	body, err := b.signedRequest(ctx, http.MethodPost, "/api/v3/order", vals)
	if err != nil {
		return orderResponse{}, err
	}

	var resp orderResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return orderResponse{}, fmt.Errorf("decode order response: %w", err)
	}
	return resp, nil
}

func (b *BinanceClient) publicRequest(ctx context.Context, method, path string, params url.Values) ([]byte, error) {
	started := time.Now()
	defer logTiming("binance_public_"+path, started)
	source := "binance.public" + path
	endpoint := b.baseURL + path
	if enc := params.Encode(); enc != "" {
		endpoint += "?" + enc
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}

	res, err := b.httpClient.Do(req)
	if err != nil {
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		err := decodeBinanceError(res.StatusCode, body)
		logIfErr("binance_public_"+path, err)
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	if runtimeAlerts != nil {
		runtimeAlerts.observeAPICall(source, time.Since(started), nil)
	}
	return body, nil
}

func (b *BinanceClient) signedRequest(ctx context.Context, method, path string, params url.Values) ([]byte, error) {
	started := time.Now()
	defer logTiming("binance_signed_"+path, started)
	source := "binance.signed" + path
	if err := b.checkBan(); err != nil {
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, 0, err)
		}
		return nil, err
	}
	params.Set("timestamp", strconv.FormatInt(time.Now().UTC().UnixMilli(), 10))
	params.Set("recvWindow", strconv.FormatInt(b.recvWindow, 10))

	query := params.Encode()
	sig := signHMACSHA256(query, b.secret)
	query = query + "&signature=" + sig

	endpoint := b.baseURL + path + "?" + query
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-MBX-APIKEY", b.apiKey)

	res, err := b.httpClient.Do(req)
	if err != nil {
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		err := decodeBinanceError(res.StatusCode, body)
		b.captureBan(err)
		logIfErr("binance_signed_"+path, err)
		if runtimeAlerts != nil {
			runtimeAlerts.observeAPICall(source, time.Since(started), err)
		}
		return nil, err
	}
	if runtimeAlerts != nil {
		runtimeAlerts.observeAPICall(source, time.Since(started), nil)
	}
	return body, nil
}

func (b *BinanceClient) checkBan() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.banUntil.After(time.Now()) {
		return fmt.Errorf("binance temporary ban active until %s", b.banUntil.UTC().Format(time.RFC3339))
	}
	return nil
}

func (b *BinanceClient) captureBan(err error) {
	be, ok := err.(*binanceAPIError)
	if !ok || be.BanUntil.IsZero() {
		return
	}
	b.mu.Lock()
	if be.BanUntil.After(b.banUntil) {
		b.banUntil = be.BanUntil
	}
	b.mu.Unlock()
}

func decodeBinanceError(code int, body []byte) error {
	var be binanceErrorResponse
	if err := json.Unmarshal(body, &be); err == nil && be.Msg != "" {
		out := &binanceAPIError{
			HTTPStatus: code,
			Code:       be.Code,
			Msg:        be.Msg,
		}
		// Example msg: "... banned until 1772839084287 ..."
		if idx := strings.Index(be.Msg, "until "); idx >= 0 {
			rest := be.Msg[idx+len("until "):]
			num := make([]rune, 0, 13)
			for _, ch := range rest {
				if ch < '0' || ch > '9' {
					break
				}
				num = append(num, ch)
			}
			if len(num) >= 10 {
				if v, parseErr := strconv.ParseInt(string(num), 10, 64); parseErr == nil {
					if len(num) >= 13 {
						out.BanUntil = time.UnixMilli(v).UTC()
					} else {
						out.BanUntil = time.Unix(v, 0).UTC()
					}
				}
			}
		}
		return out
	}
	return fmt.Errorf("binance http=%d body=%s", code, strings.TrimSpace(string(body)))
}

func signHMACSHA256(msg, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write([]byte(msg))
	return hex.EncodeToString(h.Sum(nil))
}

func (t *TelegramNotifier) Send(text string, markup any) error {
	if t.token == "" || t.chatID == "" {
		return nil
	}
	id, err := strconv.ParseInt(t.chatID, 10, 64)
	if err != nil {
		return err
	}
	return t.SendToChat(id, text, markup)
}

func (t *TelegramNotifier) SendToChat(chatID int64, text string, markup any) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	_, err := t.call("sendMessage", payload)
	return err
}

func (t *TelegramNotifier) SendPhotoURL(chatID int64, photoURL, caption string) error {
	if photoURL == "" {
		return errors.New("empty photo url")
	}
	payload := map[string]any{
		"chat_id": chatID,
		"photo":   photoURL,
		"caption": caption,
	}
	_, err := t.call("sendPhoto", payload)
	return err
}

func (t *TelegramNotifier) SendPhoto(photoURL, caption string) error {
	if t.token == "" || t.chatID == "" {
		return nil
	}
	id, err := strconv.ParseInt(t.chatID, 10, 64)
	if err != nil {
		return err
	}
	return t.SendPhotoURL(id, photoURL, caption)
}

func (t *TelegramNotifier) GetUpdates(ctx context.Context, offset int) ([]tgUpdate, int, error) {
	payload := map[string]any{
		"offset":  offset,
		"timeout": 20,
	}
	body, err := t.callWithContext(ctx, "getUpdates", payload)
	if err != nil {
		return nil, offset, err
	}
	var resp tgUpdateResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, offset, err
	}
	if !resp.OK {
		return nil, offset, errors.New("telegram getUpdates not ok")
	}
	next := offset
	for _, u := range resp.Result {
		if u.UpdateID >= next {
			next = u.UpdateID + 1
		}
	}
	return resp.Result, next, nil
}

func (t *TelegramNotifier) AnswerCallback(callbackID, text string) error {
	_, err := t.call("answerCallbackQuery", map[string]any{
		"callback_query_id": callbackID,
		"text":              text,
	})
	return err
}

func (t *TelegramNotifier) allowedChat(chatID int64) bool {
	return strconv.FormatInt(chatID, 10) == strings.TrimSpace(t.chatID)
}

func (t *TelegramNotifier) call(method string, payload any) ([]byte, error) {
	return t.callWithContext(context.Background(), method, payload)
}

func (t *TelegramNotifier) callWithContext(ctx context.Context, method string, payload any) ([]byte, error) {
	if t.token == "" {
		return nil, errors.New("telegram token missing")
	}
	endpoint := fmt.Sprintf("%s/bot%s/%s", t.baseURL, t.token, method)
	buf, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("telegram %s http=%d body=%s", method, res.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func safeSend(n *TelegramNotifier, text string, markup any) {
	logIfErr("telegram.send", n.Send(text, markup))
}

func safeSendToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	logIfErr("telegram.send_to_chat", n.SendToChat(chatID, text, markup))
}

func safeSendPhoto(n *TelegramNotifier, photoURL, caption string) {
	logIfErr("telegram.send_photo", n.SendPhoto(photoURL, caption))
}

func safeSendPhotoToChat(n *TelegramNotifier, chatID int64, photoURL, caption string) {
	logIfErr("telegram.send_photo_to_chat", n.SendPhotoURL(chatID, photoURL, caption))
}

func safeAnswerCallback(n *TelegramNotifier, callbackID, text string) {
	logIfErr("telegram.answer_callback", n.AnswerCallback(callbackID, text))
}

func safeSendLargeToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	const maxChunk = 3500
	if len(text) <= maxChunk {
		safeSendToChat(n, chatID, text, markup)
		return
	}

	lines := strings.Split(text, "\n")
	var chunk strings.Builder
	for _, line := range lines {
		// +1 for newline
		if chunk.Len()+len(line)+1 > maxChunk {
			safeSendToChat(n, chatID, chunk.String(), nil)
			chunk.Reset()
		}
		chunk.WriteString(line)
		chunk.WriteByte('\n')
	}
	if chunk.Len() > 0 {
		safeSendToChat(n, chatID, chunk.String(), markup)
	}
}

func safeSendPreToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	escaped := html.EscapeString(text)
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     "<pre>" + escaped + "</pre>",
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if markup != nil {
		payload["reply_markup"] = markup
	}
	_, err := n.call("sendMessage", payload)
	logIfErr("telegram.send_pre", err)
}

func safeSendPreLargeToChat(n *TelegramNotifier, chatID int64, text string, markup any) {
	const maxChunk = 3300
	if len(text) <= maxChunk {
		safeSendPreToChat(n, chatID, text, markup)
		return
	}
	lines := strings.Split(text, "\n")
	var chunk strings.Builder
	for _, line := range lines {
		if chunk.Len()+len(line)+1 > maxChunk {
			safeSendPreToChat(n, chatID, chunk.String(), nil)
			chunk.Reset()
		}
		chunk.WriteString(line)
		chunk.WriteByte('\n')
	}
	if chunk.Len() > 0 {
		safeSendPreToChat(n, chatID, chunk.String(), markup)
	}
}
