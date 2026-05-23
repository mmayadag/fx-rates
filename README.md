# FX Rates

One-shot Go job that syncs European Central Bank reference rates into PostgreSQL. Designed to run from an external scheduler (k8s `CronJob`, ECS task, cron) — the binary runs to completion and exits.

## Quick start

```bash
cp .env.template .env       # edit DB_* values
make local-db-up            # start a local Postgres in Docker
make local-run              # full backfill against the local DB
```

For a quick daily refresh instead of a full backfill, set `SYNC_MODE=daily_sync` in `.env` (or use `make local-run-daily`).

## How it works

Each invocation:

1. Applies pending database migrations (skippable via `RUN_MIGRATIONS=false`).
2. Seeds the `providers` table with ECB metadata; preserves external `CURAPI` rows; removes any stale provider keys (logged at WARN).
3. Backfills missing ECB rates into `rates`, resuming from `MAX(date)` per provider.
4. Updates `currencies` and `currency_coverages` for any newly observed ISO codes.
5. Records the run in `sync_runs` and emits a structured `metric` log event.
6. Exits with `0` on success, `1` on failure, `130` on signal-initiated shutdown.

Inserts use `ON CONFLICT DO NOTHING`, so re-running over already-stored dates is safe — there is no duplicate risk.

## Sync modes

| `SYNC_MODE` | Use case | Default timeout | Lookback cap |
|---|---|---|---|
| `full` | Initial seeding or recovery — walks the whole history from `providers.coverage_start`. | 6h | none |
| `daily_sync` | Recurring ECB refresh (production cron). | 30m | `DAILY_SYNC_LOOKBACK_DAYS` (default 7) |

In `daily_sync`, if `last_synced` is older than the lookback cap, the run still only fetches the last N days. This keeps recurring jobs bounded after an outage.

## Configuration

Copy [`.env.template`](./.env.template) to `.env`. Every variable below is documented inline in the template too.

### Required (database connection)

| Variable | Notes |
|---|---|
| `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_HOST`, `DB_PORT` | Standard Postgres connection. |
| `DB_SSLMODE` | Use `require` in production. `disable` is fine for local Docker. |
| `DATABASE_URL` (alt.) | Single DSN alternative. Takes precedence over the split `DB_*` vars when set. |

### Sync behaviour

| Variable | Default | Notes |
|---|---|---|
| `SYNC_MODE` | `daily_sync` | `full` or `daily_sync` |
| `DAILY_SYNC_TIMEOUT` | mode-dependent | Override only when needed. |
| `DAILY_SYNC_LOOKBACK_DAYS` | `7` | Cap on how far back a `daily_sync` run reaches. `0` disables. |
| `RUN_MIGRATIONS` | `true` | Set `false` in production when migrations are run out-of-band. |
| `BACKFILL_CONCURRENCY` | `10` | Reserved; the current code runs a single provider. |

### Connection pool

| Variable | Default |
|---|---|
| `DB_MAX_CONNECTIONS` | `10` |
| `DB_MIN_CONNECTIONS` | `5` |
| `DB_MAX_CONN_LIFETIME` | `30m` |
| `DB_MAX_CONN_IDLE_TIME` | `5m` |
| `DB_HEALTH_CHECK_PERIOD` | `30s` |

### Logging

| Variable | Default | Notes |
|---|---|---|
| `LOG_LEVEL` | derived from `DEBUG` | `debug`, `info`, `warn`, `error` |
| `DEBUG` | `true` | When `LOG_LEVEL` is unset: `true` → debug, `false` → info. Also gates scheduler heartbeats. |
| `DEBUG_HEARTBEAT_INTERVAL` | `20s` | Interval for heartbeat logs when `DEBUG=true`. |

## Make targets

| Target | What it does |
|---|---|
| `make build` | Build all packages with version stamping. |
| `make release-build VERSION=v0.1.0` | Build a stripped static linux binary into `dist/`. |
| `make test` | Run unit tests (no DB required). |
| `make test-integration` | Start local Postgres and run all tests including integration. |
| `make coverage` | Show per-package coverage. |
| `make sqlc-generate` | Regenerate typed query code from `internal/db/queries.sql`. |
| `make run` / `make run-daily` | Load `.env` and run the sync job (full / daily_sync). |
| `make local-db-up` / `make local-db-down` | Start / stop the local Docker Postgres. |
| `make local-run` / `make local-run-daily` | Run the sync against the local DB. |
| `make local-smoke` / `make local-smoke-daily` | `local-db-up` + run, in one command. |
| `make validate-fx ARGS='...'` | Run the FX validation CLI (see below). |

## Validation CLI

`cmd/fx-validate` reads rows from `rates` and compares them against ECB reference data.

```bash
make validate-fx ARGS='-date-from 2024-10-01 -date-to 2024-10-11 -output validation.csv'
```

Output is a CSV with one row per checked observation, including absolute and relative diffs against the upstream value. Only `-provider ECB` is supported.

## Scheduling

There is no internal scheduler — drive the binary from your scheduler of choice (k8s `CronJob`, ECS scheduled task, ordinary cron). A typical production schedule is `daily_sync` at **16:45 CET on weekdays**, a few minutes after the ECB's daily publication window.

The `fx_rates_run` and `fx_rates_provider_run` log events (see [`docs/metrics.md`](./docs/metrics.md)) can drive alerting via Loki/Datadog/Vector.

## Versioning

```bash
fx-rates -version           # prints the stamped build version
```

`make build` injects `git describe --tags --always --dirty`. `make release-build` requires an explicit semver tag (e.g. `VERSION=v0.1.0`) and produces a reproducible static binary.

## Documentation

- [`docs/database-schema.md`](./docs/database-schema.md) — table layouts, idempotency contract, useful audit queries
- [`docs/metrics.md`](./docs/metrics.md) — structured log event contract for dashboards and alerts
- [`docs/providers.md`](./docs/providers.md) — provider catalogue
- [`docs/provider-credentials.md`](./docs/provider-credentials.md) — credential expectations per provider
- [`docs/currencies.md`](./docs/currencies.md) — supported ISO code list
