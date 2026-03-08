# Changelog

All notable changes to this project are documented in this file.

## [v0.2.10] - 2026-03-08

### Added

- Compound forecast output now explicitly includes:
  - `amount_to_trade = balance / max_open_trades`
  - `predicted_earning_pct`
  - `possible_trade_earning = amount_to_trade * predicted_earning_pct`
- Compound chart captions now show per-trade amount and per-trade possible earning in quote currency.

### Changed

- Extended custom prediction horizon limit from `90` to `365` days for:
  - daily forecast,
  - cumulative forecast,
  - compound forecast.
- Updated Telegram validation prompts and help text to reflect `3..365` day custom horizons.

## [v0.2.9] - 2026-03-08

### Added

- Added compound earnings forecast in Freqtrade mode using:
  - `/api/v1/show_config` (max open trades, tradable balance settings),
  - `/api/v1/count` (open trades),
  - `/api/v1/balance` (quote wallet balance).
- Added Telegram chart actions for compound prediction:
  - `Compound 7d`, `Compound 30d`, and `Compound Custom` (`3..90` days).
- Added refresh support for custom compound forecast callbacks.
- Added compound forecast lines to the PnL 7d report table (expected and p20/p80 range for 7d and 30d), including model/input notes.

### Changed

- Help text now documents compound forecast behavior and Freqtrade-only scope.
- Compound model uses log-return compounding with winsorized returns and a capacity cap based on `max_open_trades` and observed average trade hold time.

## [v0.2.8] - 2026-03-08

### Added

- Added advanced PnL prediction engine using recency-weighted linear trend plus weekly seasonality.
- Added new forecast chart type (history + dashed forecast) for prediction outputs.
- Added prediction controls in Telegram Charts menu:
  - `Predict 7d`, `Predict 30d`, `Predict Custom`
  - `Predict Cum 7d`, `Predict Cum 30d`, `Predict Cum Custom`
- Added custom prediction input flow for typed horizon days (`3..90`) with cancel/back handling.
- Added cumulative forecast values to the PnL 7d table output:
  - `forecast cumulative 7d`
  - `forecast cumulative 30d`
  - forecast model note

### Changed

- Prediction charts now support both daily forecast and cumulative forecast modes.
- Prediction chart rendering now skips long leading flat-zero history segments for cleaner readability.
- Telegram prediction charts support refresh callbacks, including custom horizons.

## [v0.2.7] - 2026-03-08

### Added

- Added `Refresh` inline button for cumulative profit charts (preset, custom window, relative range, and date-range modes) to quickly regenerate the same chart with latest data.
- Added `PnL Emojis` setting in Telegram `Settings` menu (`ON/OFF`), persisted in `state.json` as `pnl_emojis_enabled`.
- Added daily PnL table legend for emoji markers when enabled (`🟢 gain | 🔴 loss | ⚪ flat`).

### Changed

- `Trades Grouped` (Freqtrade) table now hides symbols with zero activity in the selected period (only traded symbols are shown).
- Daily PnL table in Freqtrade mode now computes:
  - `pnl = sell - buy - fee` (closed trades).
  - `profit % = pnl / buy * 100` (closed-trade buy notional denominator).
- Daily PnL report now includes a clarification note that Freqtrade UI percentages may differ when wallet/equity is used as denominator.
- Telegram message and photo-caption sending now use HTML parse mode by default (except preformatted `<pre>` helper path), with automatic plain-text fallback if parsing fails.
- Startup notification formatting now uses bold/italic/code for improved readability.

## [v0.2.6] - 2026-03-08

### Added

- Custom cumulative profit timeline modes now include `Minutes` and `Trades` in Telegram flow (custom window, relative range, and date range).
- Minute-level cumulative profit series support for both Freqtrade history mode and snapshot-based mode.
- Trade-sequence cumulative profit mode (`Trades`) using closed Freqtrade trades in chronological order.

### Changed

- Bumped default image/version references to `v0.2.6` in Docker Compose, `.env.example`, local build args, and README examples.

## [v0.2.5] - 2026-03-08

### Fixed

- Telegram chart image clipping on the right edge by adding chart layout padding and x-axis offset.
- Dark-mode chart grid visibility by strengthening grid contrast and adding legacy Chart.js v2 grid fallback config (`xAxes/yAxes.gridLines`) for QuickChart compatibility.

## [v0.2.4] - 2026-03-08

### Added

- Release metadata is now shown in `/status` and `/version`, including a changelog snippet for the current tag.
- Settings changes are audit-logged to `data/settings_audit.log` with timestamp, chat/user, setting name, and old/new values.
- Added `Chart Grid` setting (`ON/OFF`) to enable or disable chart grid lines, persisted in `state.json`.
- Docker build now supports multi-arch targets (`linux/amd64`, `linux/arm64`) for ARM-compatible images.
- GitHub Actions Docker workflow now builds/pushes multi-arch images (`amd64` + `arm64`) to GHCR and tags `main` as `latest`.
- Docker Compose now supports cloud-image default run plus local build override (`docker-compose.local.yml`).
- Added extra chart settings: `Chart Size` (`compact/standard/wide`) and `Chart Labels` (`on/off`), persisted in `state.json`.

### Changed

- Improved dark-mode chart rendering:
  - Stronger grid contrast and visible grid toggles.
  - Chart titles now include chart type and selected window (e.g. `Cumulative Profit (7d)`).
- GHCR publish workflow disables provenance/SBOM attestations to avoid `unknown/unknown` platform entries in package view.

## [0.2.1] - 2026-03-08

### Added

- Full GitHub documentation set:
  - Expanded `README.md`
  - `CONTRIBUTING.md`
  - `SECURITY.md`
  - `CHANGELOG.md`
- `HEARTBEAT_ALERT_ENABLED` config flag (default `true`) to mute Telegram heartbeat watchdog alert messages without disabling heartbeat checks.
- Docker Compose env passthrough for `HEARTBEAT_ALERT_ENABLED` so users can toggle it from `.env` quickly.
- Build-time version metadata support (`APP_VERSION`, `APP_COMMIT`, `APP_BUILD_DATE`) exposed in startup logs and Telegram `/version`.
- Expanded Telegram `Settings` menu with:
  - Chart theme selector (`Dark` / `Light`) for generated charts.
  - Runtime alert toggles for heartbeat and API-failure notifications.
  - Settings overview panel showing current active preferences.
- Security hardening:
  - Enforce `https` for `BINANCE_BASE_URL` and `TELEGRAM_BASE_URL`.
  - Sanitize and truncate external HTTP error bodies before logging/alerts.
  - Tighten persisted state permissions to `0700` (dir) and `0600` (file).
