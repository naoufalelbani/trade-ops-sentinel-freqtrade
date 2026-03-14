# Trade Ops Sentinel

Automated BNB fee management and portfolio reporting for Binance spot accounts, with Telegram controls, chart reporting, and optional Freqtrade-aware analytics.

## Why I Built This

I created this project because I wanted a reliable way to make sure there is always enough BNB available to pay trading fees. Running out of BNB at the wrong time can interrupt strategy execution, so this bot monitors balances continuously, refills automatically when needed, and reports status through Telegram.

## What It Does

- Monitors BNB balance and automatically buys BNB when it drops below threshold.
- Supports three threshold strategies:
  - `BNB` units (`MIN_BNB_THRESHOLD`, `TARGET_BNB`)
  - quote value (`MIN_BNB_THRESHOLD_USDT`, `TARGET_BNB_USDT`)
  - portfolio ratio (`BNB_RATIO_MODE=true`, `BNB_RATIO_MIN`, `BNB_RATIO_TARGET`)
- Sends Telegram status, reports, and charts.
- Builds daily reports in `full` (message + charts) or `digest` mode.
- Supports Freqtrade trade history mode (`TRACKED_SYMBOLS=FREQTRADE`).
- Includes runtime watchdog/heartbeat and API failure alerting.
- Persists state locally and optionally stores trades in SQLite and cache in Redis.

## Core Features

- Auto-refill with cooldown and exchange min-notional safety checks.
- Manual actions from Telegram (`Refill Now`, `Force Buy BNB`).
- Daily/weekly/monthly reports for fees, PnL, and trade summaries.
- Cumulative profit/fees charts with preset, custom, and date-range windows.
- Freqtrade health monitoring and automated operational alerts.
- Interactive Freqtrade restart scheduling (presets or custom duration).
- Service/API health dashboard information in status and alerts.

## Architecture

- `cmd/trade-ops-sentinel`: main application runtime and integrations.
- `internal/domain`: state and domain models.
- `internal/services`: business utilities (report scheduling, time windows, abnormal-move checks, chart helpers).
- `internal/interfaces/telegram`: Telegram command and callback parsing.
- `internal/infra/worldtime`: timezone/IP-based world time helper.

## Requirements

- Binance API key/secret with trading permission.
- Telegram bot token and target chat ID.
- Docker + Docker Compose (recommended).
- Optional:
  - Redis (for API caching).
  - SQLite file volume (for incremental trade storage).
  - Freqtrade REST API with basic auth.

## Quick Start (Docker)

1. Copy config:

```bash
cp .env.example .env
```

2. Fill required values in `.env`:

- `BINANCE_API_KEY`
- `BINANCE_API_SECRET`
- `TELEGRAM_BOT_TOKEN`
- `TELEGRAM_CHAT_ID`

3. Start from GitHub Container Registry image (default):

```bash
docker compose pull
docker compose up -d
```

Optional: set a custom image in `.env`:

```env
TRADE_OPS_IMAGE=ghcr.io/naoufalelbani/trade-ops-sentinel-freqtrade:v0.2.11
```

For local rebuild from source, use the local override file:

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build
```

4. See logs:

```bash
docker compose logs -f trade-ops-sentinel
```

5. Stop:

```bash
docker compose down
```

State files are stored in `./data` and mounted into the container as `/app/data`.

## Multi-Arch Build (AMD64 + ARM64)

The Dockerfile supports cross-compile targets via BuildKit (`TARGETOS`/`TARGETARCH`), so you can publish a single multi-arch image for both Intel/AMD and ARM machines.

Build and push multi-arch image:

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t ghcr.io/naoufalelbani/trade-ops-sentinel-freqtrade:v0.2.11 \
  --push .
```

Build a local ARM64 image only:

```bash
docker buildx build \
  --platform linux/arm64 \
  -t trade-ops-sentinel:arm64-local \
  --load .
```

## Local Run (Go)

```bash
go mod download
go run ./cmd/trade-ops-sentinel
```

## Configuration Guide

### Symbol Tracking Modes

- Explicit symbols: `TRACKED_SYMBOLS=BNBUSDT,BTCUSDT,ETHUSDT`
- Auto by quote asset: `TRACKED_SYMBOLS=ALL`
- Freqtrade mode: `TRACKED_SYMBOLS=FREQTRADE`

When using `ALL`, the app fetches tradable symbols by `QUOTE_ASSET` and caps results with `MAX_AUTO_TRACKED_SYMBOLS`.

When using `FREQTRADE`, the app requires:

- `FREQTRADE_API_URL`
- `FREQTRADE_USERNAME`
- `FREQTRADE_PASSWORD`

### Threshold Strategy Priority

1. Ratio mode if `BNB_RATIO_MODE=true`
2. Quote threshold mode if quote thresholds are set (> 0)
3. Plain BNB threshold mode

### Environment Variables

#### Required

| Variable | Description |
|---|---|
| `BINANCE_API_KEY` | Binance API key |
| `BINANCE_API_SECRET` | Binance API secret |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token |
| `TELEGRAM_CHAT_ID` | Allowed chat ID for bot interactions |

#### Trading and Symbols

| Variable | Default | Description |
|---|---:|---|
| `SYMBOL` | `BNBUSDT` | Trading pair used for BNB refill buys |
| `TRACKED_SYMBOLS` | `BNBUSDT` | CSV list, `ALL`, or `FREQTRADE` |
| `MAX_AUTO_TRACKED_SYMBOLS` | `40` | Cap when `TRACKED_SYMBOLS=ALL` |
| `BNB_ASSET` | `BNB` | Fee asset symbol |
| `QUOTE_ASSET` | `USDT` | Quote currency |

#### Thresholds and Buying

| Variable | Default | Description |
|---|---:|---|
| `MIN_BNB_THRESHOLD` | `0` | Minimum BNB (BNB mode) |
| `TARGET_BNB` | `0` | Target BNB after refill |
| `MIN_BNB_THRESHOLD_USDT` | `0` | Min fee reserve in quote currency |
| `TARGET_BNB_USDT` | `0` | Target fee reserve in quote currency |
| `BNB_RATIO_MODE` | `false` | Enable ratio threshold mode |
| `BNB_RATIO_MIN` | `0` | Min portfolio ratio in BNB |
| `BNB_RATIO_TARGET` | `0` | Target portfolio ratio in BNB |
| `MAX_BUY_QUOTE` | `25` | Max quote spent per buy |
| `MIN_BUY_QUOTE` | `5` | Min quote spent per buy |
| `BUY_COOLDOWN` | `15m` | Cooldown between buys |
| `ACCOUNT_RESERVE_RATIO` | `0.98` | Portion of quote balance allowed for buy |

#### Runtime and API

| Variable | Default | Description |
|---|---:|---|
| `CHECK_INTERVAL` | `2m` | Main monitor cycle |
| `RECV_WINDOW_MS` | `5000` | Binance recvWindow |
| `SUMMARY_EVERY_CHECKS` | `10` | Status summary cadence |
| `NOTIFY_ON_EVERY_CHECK` | `false` | Send update each cycle |
| `BINANCE_BASE_URL` | `https://api.binance.com` | Binance API base URL (must be `https`) |
| `TELEGRAM_BASE_URL` | `https://api.telegram.org` | Telegram API base URL (must be `https`) |
| `APP_VERSION` | `v0.2.11` | Build/version label shown on startup and `/version` |
| `APP_COMMIT` | `local` | Build commit shown on startup and `/version` |
| `APP_BUILD_DATE` | `unknown` | Build timestamp shown on startup and `/version` |
| `FREQTRADE_ALERT_ON_STOPPED` | `true` | Alert when Freqtrade bot is in `stopped` state |

#### Reporting

| Variable | Default | Description |
|---|---:|---|
| `DAILY_REPORT_ENABLED` | `true` | Enable daily report loop |
| `DAILY_REPORT_TIME_UTC` | `00:05` | Daily trigger hour/minute |
| `DAILY_REPORT_TIMEZONE` | `UTC` | IANA timezone or `AUTO`/`AUTO_IP` |
| `DAILY_REPORT_MODE` | `full` | `full` or `digest` |
| `DAILY_DIGEST_TRADES` | `3` | Number of last trades in digest |
| `FEE_MAIN_CURRENCY` | `BNB` | Display unit: `BNB` or `USDT` |

#### Heartbeat and Alerts

| Variable | Default | Description |
|---|---:|---|
| `HEARTBEAT_ENABLED` | `true` | Enable stale-check watchdog |
| `HEARTBEAT_ALERT_ENABLED` | `true` | Send Telegram heartbeat watchdog alerts |
| `HEARTBEAT_STALE_AFTER` | `10m` | Stale threshold |
| `HEARTBEAT_CHECK_INTERVAL` | `1m` | Watchdog polling interval |
| `HEARTBEAT_PING_URL` | empty | External heartbeat ping URL |
| `API_FAILURE_ALERT_ENABLED` | `true` | Enable API failure tracking |
| `API_FAILURE_THRESHOLD` | `3` | Consecutive failures before alert |
| `API_FAILURE_ALERT_COOLDOWN` | `15m` | Alert cooldown per source |
| `API_LATENCY_THRESHOLD` | `8s` | Slow-call threshold |
| `API_LATENCY_SPIKE_THRESHOLD` | `3` | Slow-call spike threshold |
| `ABNORMAL_MOVE_ALERT_ENABLED` | `true` | Enable abnormal-move alerts |
| `ABNORMAL_MOVE_DROP_1H_PCT` | `3` | 1h drop alert threshold (%) |
| `ABNORMAL_MOVE_DROP_24H_PCT` | `8` | 24h drop alert threshold (%) |
| `ABNORMAL_MOVE_ALERT_COOLDOWN` | `2h` | Abnormal-move alert cooldown |

#### Persistence and Cache

| Variable | Default | Description |
|---|---:|---|
| `STATE_FILE` | `./data/state.json` | Snapshot/event state file |
| `MAX_SNAPSHOTS` | `3000` | Max in-memory snapshots persisted |
| `REDIS_ENABLED` | `false` | Enable Redis cache |
| `REDIS_ADDR` | `redis:6379` | Redis address |
| `REDIS_PASSWORD` | empty | Redis password |
| `REDIS_DB` | `0` | Redis DB index |
| `REDIS_TRADE_TTL` | `10m` | Trade cache TTL |
| `REDIS_PRICES_TTL` | `15s` | Prices cache TTL |
| `REDIS_KEY_PREFIX` | `bnbfm:` | Redis key prefix |
| `MYTRADES_MAX_CONCURRENCY` | `1` | Max concurrent Binance trades requests |
| `MYTRADES_MIN_INTERVAL` | `400ms` | Min interval between Binance trades requests |
| `SQLITE_ENABLED` | `false` | Enable SQLite trade store |
| `SQLITE_PATH` | `./data/trades.db` | SQLite DB path |
| `SQLITE_INITIAL_LOOKBACK_DAYS` | `7` | First sync lookback window |
| `SQLITE_SYNC_INTERVAL` | `5m` | Incremental sync interval |
| `SQLITE_MAX_LOOKBACK_DAYS` | `30` | Max sync lookback cap |

#### Freqtrade Integration

| Variable | Default | Description |
|---|---:|---|
| `FREQTRADE_API_URL` | empty | Freqtrade API URL |
| `FREQTRADE_USERNAME` | empty | Basic auth username |
| `FREQTRADE_PASSWORD` | empty | Basic auth password |
| `FREQTRADE_TRADES_LIMIT` | `500` | Page size for trades endpoint (max 500) |
| `FREQTRADE_MAX_PAGES` | `20` | Max pages fetched per request |

## Telegram Reference

### Commands

- `/start` or `/menu`: open menu and reply keyboard.
- `/status`: account snapshot, fees, pnl, system metrics, watchdog.
- `/daily`: send full daily report immediately.
- `/version`: show app version, commit, and build date.
- `/help`: show help text.

### Reply Keyboard

- `Status`
- `Daily Report`
- `Menu`
- `Help`

### Inline Menus

- Actions: refill now, force buy, report now.
- Reports: daily/weekly/monthly reports, fees, pnl, trades, leaders, PnL table, and <b>PnL History</b>.
- Charts: fee/pnl charts, cumulative views, custom windows, date/range tools.
- Settings: fee currency, chart theme (dark/light), alert toggles, and Freqtrade health.

### Date/Range Input Rules

- Manual format accepted: `YYYY-MM-DD HH:MM`, `YYYY-MM-DD HH`, `YYYY-MM-DD`.
- Inputs are interpreted in UTC.
- Type `cancel` or `back` to exit input mode.

## Freqtrade Mode

Set:

```env
TRACKED_SYMBOLS=FREQTRADE
FREQTRADE_API_URL=http://freqtrade:8080
FREQTRADE_USERNAME=...
FREQTRADE_PASSWORD=...
```

Behavior:

- Pair list resolves from Freqtrade status/trades endpoints.
- Fee and PnL analytics use Freqtrade trade history.
- Telegram `/status` reports real-time Freqtrade operational state (`running`, `stopped`).
- Automatic alerts trigger when the bot enters a `stopped` state.
- Interactive restart keyboard allows scheduling a restart (10m, 30m, 1h, or Custom duration).
- Watchdog automatically executes restarts via the Freqtrade API `/api/v1/start`.

## Docker Compose Networking Note

`docker-compose.yml` includes external network `ft_userdata_default`. Ensure it exists before startup:

```bash
docker network create ft_userdata_default || true
```

If you do not need this shared network, remove it from `docker-compose.yml`.

## Operational Notes

- This project can place real market orders.
- Start with small `MAX_BUY_QUOTE` and strict `MIN_BUY_QUOTE`.
- Use API keys with minimum required permissions.
- Keep `.env` out of version control.

## Troubleshooting

- Bot does not respond in Telegram:
  - Verify `TELEGRAM_BOT_TOKEN` and `TELEGRAM_CHAT_ID`.
  - Send `/start` to initialize chat with the bot.
- Frequent API errors:
  - Check Binance/Freqtrade credentials, network reachability, and rate limits.
  - Review API alert messages for exact failing source.
- No charts/history data:
  - Wait for snapshots/trades to accumulate.
  - Confirm `TRACKED_SYMBOLS` mode and source connectivity.
- Compose fails on missing network:
  - Create `ft_userdata_default` or remove external network binding.

## Development

```bash
go test ./...
go vet ./...
```

Build binary:

```bash
go build -o trade-ops-sentinel ./cmd/trade-ops-sentinel
```

## License

No license file is currently included. Add a `LICENSE` file before public open-source distribution.
