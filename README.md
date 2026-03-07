# BNB Fees Monitor (Go + Docker)

Monitors your BNB balance for Freqtrade fee payments, auto-buys BNB when low, and provides Telegram buttons plus automatic daily reports with charts.

## Features

- Auto top-up BNB for trading fees (`MIN_BNB_THRESHOLD` -> `TARGET_BNB`)
- Optional quote-based threshold mode (`MIN_BNB_THRESHOLD_USDT` -> `TARGET_BNB_USDT`)
- Optional ratio mode (`BNB_RATIO_MODE=true`) to keep BNB as a % of total account value
- Telegram inline buttons:
  - `Status`
  - `Daily Report Now`
  - `Daily Report / Weekly Report / Monthly Report`
  - `Fees Day / Week / Month`
  - `Trades Day / Week / Month`
  - `PnL 7d Table` (Freqtrade-like table format + refresh button)
  - `PnL Day / Week / Month`
  - `Fees Chart` and `PnL Chart`
- Automatic daily Telegram report (summary + fees chart + portfolio chart + pnl chart)
- Daily report mode: `full` (charts) or `digest` (one compact message)
- Heartbeat monitor with stale-check alerts and optional external ping URL
- API failure alerts (auth/network failures + timeout spike detection)
- Abnormal move alerts for portfolio drops (1h / 24h thresholds)
- Optional auto timezone (`DAILY_REPORT_TIMEZONE=AUTO`) for travel/network-aware scheduling
- Persists snapshots to local state file for PnL tracking
- Optional Redis cache for Binance trade history windows and price map
- SQLite trade store with incremental sync (fetches only missing trades after first sync)
- Dockerized (no local Go install needed)

## Configure

```bash
cp .env.example .env
```

Set required values in `.env`:

- `BINANCE_API_KEY`, `BINANCE_API_SECRET`
- `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`
- `TRACKED_SYMBOLS` (pairs your bot trades, comma-separated)
- `TRACKED_SYMBOLS=ALL` to auto-track all tradable pairs for your `QUOTE_ASSET` (capped by `MAX_AUTO_TRACKED_SYMBOLS`)
- `TRACKED_SYMBOLS=FREQTRADE` to auto-track pairs from Freqtrade API (`FREQTRADE_API_URL`, `FREQTRADE_USERNAME`, `FREQTRADE_PASSWORD`)
- `DAILY_REPORT_ENABLED`, `DAILY_REPORT_TIME_UTC`, `DAILY_REPORT_TIMEZONE`
- `DAILY_REPORT_MODE` (`full` or `digest`), `DAILY_DIGEST_TRADES`
- `FEE_MAIN_CURRENCY` (`BNB` or `USDT`) for fee displays
  - Can also be changed from Telegram via `Currency` button
- `HEARTBEAT_ENABLED`, `HEARTBEAT_STALE_AFTER`, `HEARTBEAT_CHECK_INTERVAL`, optional `HEARTBEAT_PING_URL`
- `API_FAILURE_ALERT_ENABLED`, `API_FAILURE_THRESHOLD`, `API_FAILURE_ALERT_COOLDOWN`
- `API_LATENCY_THRESHOLD`, `API_LATENCY_SPIKE_THRESHOLD`
- `ABNORMAL_MOVE_ALERT_ENABLED`, `ABNORMAL_MOVE_DROP_1H_PCT`, `ABNORMAL_MOVE_DROP_24H_PCT`, `ABNORMAL_MOVE_ALERT_COOLDOWN`
- Optional: `REDIS_ENABLED`, `REDIS_ADDR`, `REDIS_TRADE_TTL`, `REDIS_PRICES_TTL`
- Optional: `SQLITE_ENABLED`, `SQLITE_PATH`, `SQLITE_INITIAL_LOOKBACK_DAYS`, `SQLITE_SYNC_INTERVAL`, `SQLITE_MAX_LOOKBACK_DAYS`
- `FREQTRADE_API_URL`, `FREQTRADE_USERNAME`, `FREQTRADE_PASSWORD`, `FREQTRADE_TRADES_LIMIT`, `FREQTRADE_MAX_PAGES` (when `TRACKED_SYMBOLS=FREQTRADE`)

Threshold options:

- BNB mode: set `MIN_BNB_THRESHOLD` and `TARGET_BNB`
- Quote mode (no manual BNB calculation): set `MIN_BNB_THRESHOLD_USDT` and `TARGET_BNB_USDT`
- Ratio mode: set `BNB_RATIO_MODE=true`, `BNB_RATIO_MIN`, `BNB_RATIO_TARGET` (example `0.002` = 0.2%)
- Priority: ratio mode overrides quote mode, quote mode overrides BNB mode.
- If quote mode is set, bot converts thresholds to BNB using live symbol price each check.
- `MAX_BUY_QUOTE` and `MIN_BUY_QUOTE` are already quote-asset values (USDT when `QUOTE_ASSET=USDT`).

## Run

```bash
docker compose up -d --build
```

State data is persisted in `./data` (mounted to `/app/data` in container).

Logs:

```bash
docker compose logs -f
```

Stop:

```bash
docker compose down
```

## Telegram usage

- Start chat with your bot.
- Send `/start` or `/menu`.
- Bottom reply keyboard is shown with shortcuts: `Status`, `Daily Report`, `Menu`, `Help`.
- Use buttons to request fee totals, PnL totals, and charts.
- Send `/daily` to force-send the full daily report immediately.
- Send `/help` to see all commands.

## Notes

- Fee totals are calculated from Freqtrade trades when `TRACKED_SYMBOLS=FREQTRADE`, otherwise Binance `myTrades`.
- PnL and ratio mode use estimated total portfolio value in `QUOTE_ASSET` (conversion by Binance spot prices where available).
- This places real market orders; test with small limits first.
