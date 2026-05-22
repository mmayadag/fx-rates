# Database Schema

This service reads from and writes to a single Postgres database. Four tables hold all state. Migrations are managed by `internal/db/migrations/*.sql` and run automatically on startup unless `RUN_MIGRATIONS=false`.

## Overview

| Table | Purpose |
|---|---|
| `rates` | Exchange-rate observations, one row per `(provider, date, base, quote)` |
| `providers` | Static metadata for each upstream rate source |
| `currencies` | ISO 4217 currency lifetimes (start/end), the canonical currency list |
| `currency_coverages` | Per-provider currency support windows |

## Conventions

- **Date semantics**: every `DATE` column is interpreted in UTC. The scheduler truncates `time.Now().UTC()` to midnight before comparing or inserting dates, so a `date` of `2026-05-22` means "the ECB publication for the UTC day 2026-05-22". No timezone column is stored.
- **Idempotency**: all inserts into `rates` use `ON CONFLICT (provider, date, base, quote) DO NOTHING`. Re-running a sync over already-stored days is safe.
- **Bulk inserts**: the scheduler stages records into a `TEMP TABLE rates_stage` and `INSERT ... SELECT ... ON CONFLICT DO NOTHING RETURNING 1` to count newly inserted rows. See `internal/scheduler/backfill.go` (`bulkUpsertRates`).
- **Currency codes**: ISO 4217 three-letter codes, uppercase. Stored as `VARCHAR(3)`. Country codes are ISO 3166-1 alpha-2, `VARCHAR(2)`.
- **Provider keys**: short uppercase identifiers, `VARCHAR(10)`. Current values: `ECB`, `CURAPI`.

---

## `rates`

Primary fact table — every observed rate the service has seen.

```sql
CREATE TABLE rates (
    date     DATE             NOT NULL,
    base     VARCHAR(3)       NOT NULL,
    quote    VARCHAR(3)       NOT NULL,
    rate     DOUBLE PRECISION NOT NULL,
    provider VARCHAR(10)      NOT NULL,

    CONSTRAINT rates_pkey PRIMARY KEY (provider, date, base, quote)
);

CREATE INDEX idx_rates_date           ON rates (date);
CREATE INDEX idx_rates_provider_quote ON rates (provider, quote);
CREATE INDEX idx_rates_provider_base  ON rates (provider, base);
```

| Column | Type | Notes |
|---|---|---|
| `provider` | `VARCHAR(10)` | FK in spirit only — not enforced. Must match a `providers.key`. |
| `date` | `DATE` | UTC day of the observation. ECB publishes daily ~16:00 CET. |
| `base` | `VARCHAR(3)` | ISO code of the base currency. ECB always emits `EUR`. |
| `quote` | `VARCHAR(3)` | ISO code of the quote currency. |
| `rate` | `DOUBLE PRECISION` | `quote per 1 base`. Always strictly positive (validated at insert; see `backfill.go:333`). |

**Primary key** `(provider, date, base, quote)` enables multi-provider coexistence without conflicts and is the natural deduplication boundary.

**Index rationale**:
- `idx_rates_date` — answer "what did we sync on day X" across all providers (validator, reports).
- `idx_rates_provider_quote` / `idx_rates_provider_base` — drive provider-scoped queries like "last ECB EUR/USD rate".

---

## `providers`

Static metadata about each upstream source. Seeded from `internal/seed/data/providers/*.json` on startup.

```sql
CREATE TABLE providers (
    key            VARCHAR(10) PRIMARY KEY,
    name           TEXT        NOT NULL,
    country_code   VARCHAR(2),
    rate_type      TEXT,
    pivot_currency VARCHAR(3),
    data_url       TEXT,
    terms_url      TEXT,
    publish_time   INTEGER,
    publish_days   VARCHAR(10),
    coverage_start DATE
);
```

| Column | Notes |
|---|---|
| `key` | Stable provider identifier, used as FK target everywhere. |
| `pivot_currency` | Base currency the provider expresses rates against. ECB → `EUR`. |
| `publish_time` | Seconds-since-midnight UTC (or minutes; verify with seed data) of the daily publication. |
| `publish_days` | Comma-separated `Mon,Tue,...` or `D` for daily. |
| `coverage_start` | First date the provider has rates for; used as backfill anchor when no `lastSynced` exists. |

**Seed behaviour**: on every startup the seeder runs `DELETE FROM providers WHERE key NOT IN ('ECB','CURAPI') RETURNING key`. Removed keys are logged at `WARN`. Allow-list is currently hardcoded in `internal/db/seed.go:44`.

---

## `currencies`

Canonical ISO 4217 currency catalogue with validity windows.

```sql
CREATE TABLE currencies (
    iso_code   VARCHAR(3) PRIMARY KEY,
    start_date DATE       NOT NULL,
    end_date   DATE       NOT NULL
);
```

Populated and refined by the scheduler as new ISO codes appear in fetched records (`internal/scheduler/backfill.go` → `upsertCurrencies`). `end_date` is bumped forward as the currency continues to be observed; a stale `end_date` is the signal that an ISO code has been retired (rarely happens, e.g. EUR-zone joiners).

---

## `currency_coverages`

Per-provider currency support windows. Lets the validator answer "did ECB publish a EUR/JPY rate on 2024-06-12?" without scanning `rates`.

```sql
CREATE TABLE currency_coverages (
    provider_key VARCHAR(10) NOT NULL REFERENCES providers(key) ON DELETE CASCADE,
    iso_code     VARCHAR(3)  NOT NULL,
    start_date   DATE,
    end_date     DATE,

    PRIMARY KEY (provider_key, iso_code)
);
```

| Column | Notes |
|---|---|
| `provider_key` | Enforced FK to `providers.key`. Cascades on provider deletion. |
| `iso_code` | Currency this provider has been observed publishing. |
| `start_date` / `end_date` | Earliest / latest observation for that pair. Nullable until the first observation. |

Maintained on every successful insert batch in `bulkUpsertRates`. Used by `cmd/fx-validate` to bound its comparison window.

---

## Migrations

| File | Effect |
|---|---|
| `001_create_rates.{up,down}.sql` | `rates` + 3 indexes |
| `002_create_providers.{up,down}.sql` | `providers` |
| `003_create_currencies.{up,down}.sql` | `currencies` + `currency_coverages` |

The runner (`internal/db/migrate.go`) uses `github.com/jackc/pgx/v5` with a custom file walker — no third-party migration library. Each file is executed once; the runner keeps a tracking table to skip already-applied migrations.

**To run migrations manually** (e.g. in a CI step or release script):

```bash
RUN_MIGRATIONS=true SYNC_MODE=full make run
# or boot the binary with a short sync timeout and accept the eventual no-op
```

A dedicated `cmd/fx-migrate` binary is a follow-up; see the project improvements list.

---

## Querying notes

- The scheduler resumes from `MAX(date) WHERE provider = ?` (see `getStartDate`), so an empty `rates` table starts a full backfill from `providers.coverage_start`.
- Validator (`cmd/fx-validate`) reads `rates` with a `provider` filter and re-fetches the same window from ECB online to diff. It does not write back.
- No FK from `rates.provider` to `providers.key` — this is deliberate so historical data survives provider deletion. The seeder enforces the allow-list on `providers` only.
