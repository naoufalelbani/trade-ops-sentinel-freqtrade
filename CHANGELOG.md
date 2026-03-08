# Changelog

All notable changes to this project are documented in this file.

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
