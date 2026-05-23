# Metrics

This service is a one-shot job, so a Prometheus pull-model scrape doesn't apply: the binary exits before any scraper can reach it. Instead, the scheduler emits **structured metric events** to stdout as JSON log lines. A log shipper (Loki + Promtail, Datadog, Vector, Fluent Bit) can parse them and derive time-series metrics from there.

## Contract

A metric event is any structured log line where:

- `level == "INFO"`
- `msg == "metric"`
- `name` is one of the documented metric names below

These three fields together identify the event and stay stable across releases. Field names not in the table are non-contract and may change.

## `fx_rates_provider_run`

Emitted once per provider after its backfill returns, regardless of outcome.

| Field | Type | Description |
|---|---|---|
| `name` | string | constant: `"fx_rates_provider_run"` |
| `provider` | string | provider key (e.g. `"ECB"`) |
| `mode` | string | `"daily_sync"` or `"full"` |
| `status` | string | `"success"`, `"empty"`, `"error"`, `"unavailable"`, `"up_to_date"` |
| `rows_fetched` | int | rows pulled from upstream during this run |
| `rows_inserted` | int | new rows inserted into `rates` |
| `rows_skipped` | int | rows discarded by `ON CONFLICT DO NOTHING` |
| `duration_ms` | int | wall-clock duration of the provider's backfill |
| `last_synced_unix` | int | Unix seconds of the latest `rates.date` after the run, or `0` if none |

Example:
```json
{"time":"2026-05-22T16:00:00Z","level":"INFO","msg":"metric","run_id":"...","name":"fx_rates_provider_run","provider":"ECB","mode":"daily_sync","status":"success","rows_fetched":31,"rows_inserted":31,"rows_skipped":0,"duration_ms":842,"last_synced_unix":1716422400}
```

## `fx_rates_run`

Emitted once at the end of the job (including interruption paths).

| Field | Type | Description |
|---|---|---|
| `name` | string | constant: `"fx_rates_run"` |
| `mode` | string | `"daily_sync"` or `"full"` |
| `status` | string | `"success"`, `"failed"`, `"interrupted"` |
| `providers_total` | int | total providers attempted |
| `providers_succeeded` | int | providers that completed with status `success` |
| `providers_failed` | int | providers with status `error` |
| `providers_unavailable` | int | providers with status `unavailable` |
| `providers_up_to_date` | int | providers that were already current |
| `rows_inserted` | int | new rows across the whole run |
| `rows_skipped` | int | duplicates discarded across the whole run |
| `duration_ms` | int | wall-clock duration of the entire run |

Example:
```json
{"time":"2026-05-22T16:00:01Z","level":"INFO","msg":"metric","run_id":"...","name":"fx_rates_run","mode":"daily_sync","status":"success","providers_total":1,"providers_succeeded":1,"providers_failed":0,"providers_unavailable":0,"providers_up_to_date":0,"rows_inserted":31,"rows_skipped":0,"duration_ms":1024}
```

## Suggested derived Prometheus metrics

If you're piping logs into a Prometheus stack (e.g. Loki + recording rules, or Datadog log-based metrics):

- `fx_rates_provider_runs_total{provider, mode, status}` — counter incremented from each `fx_rates_provider_run` event
- `fx_rates_rows_inserted_total{provider, mode}` — counter, sum `rows_inserted` from `fx_rates_provider_run`
- `fx_rates_provider_duration_seconds{provider, mode}` — histogram of `duration_ms / 1000`
- `fx_rates_last_synced_timestamp{provider}` — gauge fed from `last_synced_unix`
- `fx_rates_run_duration_seconds{mode, status}` — histogram of `duration_ms / 1000` from `fx_rates_run`

## Suggested alerts

- **Backfill stale**: `time() - fx_rates_last_synced_timestamp{provider="ECB"} > 60*60*24*2` — alert if no successful sync for >2 days.
- **Repeated failures**: `increase(fx_rates_provider_runs_total{status="error"}[1d]) > 3` — escalate after 3 daily failures.
- **Sync regression**: `fx_rates_rows_inserted_total{provider="ECB",mode="daily_sync"}` plateaued — daily refresh stopped pulling new rates.

## Non-metric logs

The existing human-readable logs (`backfill: starting`, `backfill: completed`, etc.) stay as-is. They are intentionally *not* metrics — they carry richer context (last-synced timestamps, fetch ranges, retry counts) at the cost of unstable field names. Use them for debugging, use `msg == "metric"` events for dashboards and alerts.
