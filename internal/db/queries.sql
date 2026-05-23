-- name: ListProviders :many
SELECT key, name, country_code, rate_type, pivot_currency,
       data_url, terms_url, publish_time, publish_days, coverage_start
FROM providers
ORDER BY key;

-- name: ListProviderKeys :many
SELECT key FROM providers ORDER BY key;

-- name: CountProviders :one
SELECT COUNT(*)::int FROM providers;

-- name: DeleteStaleProviders :many
DELETE FROM providers WHERE key NOT IN ('ECB', 'CURAPI') RETURNING key;

-- name: UpsertProvider :exec
INSERT INTO providers (
    key, name, country_code, rate_type, pivot_currency,
    data_url, terms_url, publish_time, publish_days, coverage_start
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (key) DO UPDATE SET
    name           = EXCLUDED.name,
    country_code   = EXCLUDED.country_code,
    rate_type      = EXCLUDED.rate_type,
    pivot_currency = EXCLUDED.pivot_currency,
    data_url       = EXCLUDED.data_url,
    terms_url      = EXCLUDED.terms_url,
    publish_time   = EXCLUDED.publish_time,
    publish_days   = EXCLUDED.publish_days,
    coverage_start = EXCLUDED.coverage_start;

-- name: GetLastSyncedForProvider :one
SELECT MAX(date)::date AS last_synced
FROM rates
WHERE provider = $1;

-- name: GetAllLastSynced :many
SELECT provider, MAX(date)::date AS last_synced
FROM rates
GROUP BY provider;

-- name: GetCurrencyDateRange :one
SELECT MIN(date)::date AS start_date, MAX(date)::date AS end_date
FROM rates
WHERE provider = $1 AND (quote = $2 OR base = $2);

-- name: UpsertCurrencyCoverage :exec
INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
VALUES ($1, $2, $3, $4)
ON CONFLICT (provider_key, iso_code) DO UPDATE SET
    start_date = LEAST(currency_coverages.start_date, EXCLUDED.start_date),
    end_date   = GREATEST(currency_coverages.end_date, EXCLUDED.end_date);

-- name: UpsertCurrency :exec
INSERT INTO currencies (iso_code, start_date, end_date)
VALUES ($1, $2, $3)
ON CONFLICT (iso_code) DO UPDATE SET
    start_date = LEAST(currencies.start_date, EXCLUDED.start_date),
    end_date   = GREATEST(currencies.end_date, EXCLUDED.end_date);

-- name: FetchRatesForValidation :many
SELECT date, base, quote, rate, provider
FROM rates
WHERE (sqlc.arg(date_from)::text = '' OR date >= sqlc.arg(date_from)::date)
  AND (sqlc.arg(date_to)::text   = '' OR date <= sqlc.arg(date_to)::date)
  AND (sqlc.arg(base_code)::text = '' OR base = sqlc.arg(base_code))
  AND (sqlc.arg(quote_code)::text = '' OR quote = sqlc.arg(quote_code))
  AND provider = sqlc.arg(provider_key)
ORDER BY date, provider, base, quote
LIMIT sqlc.arg(row_limit);

-- name: RecordSyncRun :exec
INSERT INTO sync_runs (
    provider, mode, status, started_at, finished_at,
    rows_fetched, rows_inserted, rows_skipped, error_message
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetLatestSyncRun :one
SELECT id, provider, mode, status, started_at, finished_at,
       rows_fetched, rows_inserted, rows_skipped, error_message
FROM sync_runs
WHERE provider = $1
ORDER BY finished_at DESC
LIMIT 1;
