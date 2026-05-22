# FX Rates

`fx-rates` is a one-shot Go job that syncs European Central Bank reference rates into PostgreSQL.

On each run it:

- applies database migrations
- seeds the `providers` table with ECB metadata and preserves external `CURAPI` metadata
- backfills ECB rates into `rates`
- updates `currencies` and `currency_coverages`
- exits when the sync finishes

The database schema stays unchanged. The `providers` table still exists, and `rates.provider` remains part of the primary key, but this runtime writes ECB rows only.

## Configuration

For production daily sync, these variables are sufficient:

```env
DB_USER=...
DB_PASSWORD=...
DB_NAME=...
DB_HOST=...
DB_PORT=...
DB_SSLMODE=...

DB_MAX_CONNECTIONS=10
BACKFILL_CONCURRENCY=10
DEBUG=true
SYNC_MODE=daily_sync
DAILY_SYNC_TIMEOUT=30m
DEBUG_HEARTBEAT_INTERVAL=20s
```

Copy one of the templates to `.env` if you want a starting point.

- [`.env.template`](./.env.template)
- [`.env.daily_sync.template`](./.env.daily_sync.template)
- [`.env.backfill.template`](./.env.backfill.template)

`DB_SSLMODE` should typically be `require` in production. `DATABASE_URL` is still accepted as a backward-compatible alternative, but the split `DB_*` variables are the primary configuration path. No provider credentials are needed in ECB-only mode. `currencyapi.com` credentials are also not used here because `CURAPI` rows are written by another API, not by this service.

`SYNC_MODE=full` is for intentional historical backfills. `SYNC_MODE=daily_sync` is for recurring ECB refresh runs. If `DAILY_SYNC_TIMEOUT` is unset, the default is `6h` for `full` and `30m` for `daily_sync`.

## Local Run

Typical local flow:

```bash
cp .env.daily_sync.template .env
make local-db-up
make local-run
```

Useful commands:

```bash
make test
make coverage
make run
make run-daily
make validate-fx
make local-db-up
make local-db-down
make local-run
make local-run-daily
make local-smoke
make local-smoke-daily
```

`make run`, `make run-daily`, `make local-run`, `make local-run-daily`, `make local-smoke`, and `make local-smoke-daily` load values from `.env`.

## Runtime Behavior

The job resumes from the last synced ECB date already stored in `rates`. If no ECB rows exist yet, it starts from `providers.coverage_start`. Re-running does not duplicate rows because inserts use `ON CONFLICT DO NOTHING`.

Startup seeding keeps `providers` aligned to the supported metadata set:

- `ECB` is the only provider this service syncs, backfills, and validates
- `CURAPI` metadata may also exist in `providers` for rows written by another API
- other stale provider rows are removed during seed

Expected startup logs include:

- `providers seeded` with `providers=["ECB","CURAPI"]` when `CURAPI` already exists
- `backfill: providers queued` with `total=1`

## Validation CLI

The repository includes an ECB-only validation CLI that reads DB rows from `rates` and compares them against online ECB reference data.

Examples:

```bash
make validate-fx ARGS='-date-from 2024-10-01 -date-to 2024-10-11 -provider ECB -output validation.csv'
env GOCACHE=$(pwd)/.gocache go run ./cmd/fx-validate -date-from 2024-10-01 -date-to 2024-10-11 -provider ECB
```

`-provider` is retained for compatibility, but only `ECB` is accepted. `CURAPI` is metadata-only in this repo and is not used by daily sync, full backfill, or validation.

## Scheduling

This repository does not include an internal scheduler. Run the container from your scheduler of choice.

For recurring production runs, schedule `SYNC_MODE=daily_sync` shortly after the ECB publication window. A common pattern is every working day around `16:45 CET`, with a small buffer after the ECB's usual `around 16:00 CET` publication time.

## Reference Docs

- [docs/database-schema.md](./docs/database-schema.md)
- [docs/providers.md](./docs/providers.md)
- [docs/provider-credentials.md](./docs/provider-credentials.md)
- [docs/currencies.md](./docs/currencies.md)
