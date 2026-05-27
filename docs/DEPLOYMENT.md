# Deployment

`fx-rates` is a one-shot job: it runs to completion and exits. There is no long-lived server, no health endpoint, and no internal scheduler. Drive it from an external scheduler (Kubernetes `CronJob`, ECS scheduled task, or plain cron) and treat each invocation as a self-contained unit of work.

## Exit codes

The scheduler must treat these as the success/failure contract:

| Code | Meaning | Scheduler action |
|---|---|---|
| `0` | Run completed; all providers succeeded, were empty, or already up to date. | None — success. |
| `1` | Failure — config error, migration failure, pool init, seed, or a provider error. | Alert / retry per policy. |
| `130` | Graceful shutdown after `SIGINT`/`SIGTERM` (in-flight work cancelled cleanly). | Expected on pod eviction; not an error to alert on by itself. |

## Container image

The [`Dockerfile`](../Dockerfile) produces a `scratch`-based image: static binary, CA certificates only, running as non-root UID `65534`. There is no shell, package manager, or writable filesystem.

```bash
VERSION=$(git describe --tags --always --dirty)
docker build --build-arg VERSION="$VERSION" -t <registry>/fx-rates:"$VERSION" .
docker push <registry>/fx-rates:"$VERSION"
```

`VERSION` is stamped into the binary and printed by `fx-rates -version`. Pin a real tag in production rather than `latest` so a rollback is a one-line image change.

## Timeouts

| Phase | Default | Override | Notes |
|---|---|---|---|
| Setup (migrate + pool + seed) | 2m (fixed) | — | Not configurable; fails fast if the DB is unreachable. |
| Sync — `daily_sync` | 30m | `DAILY_SYNC_TIMEOUT` | |
| Sync — `full` | 6h | `DAILY_SYNC_TIMEOUT` | Initial seeding / recovery. |

Set the scheduler's hard deadline (`activeDeadlineSeconds` in k8s) slightly above the sync timeout so the app's own context cancellation fires first and exits `130` cleanly, rather than the platform `SIGKILL`-ing it.

## Configuration in production

Full variable reference is in the [README](../README.md#configuration). Production-relevant points:

- **`DB_SSLMODE`** defaults to `require`, which encrypts traffic but does **not** verify the server certificate — it does not protect against a man-in-the-middle. For production against a managed Postgres over an untrusted network, use `verify-full` and supply the CA via `sslrootcert` in `DATABASE_URL`. Only set `disable` for a local TLS-less Postgres.
- Pass DB credentials via a secret store (k8s `Secret`, ECS secrets), never baked into the image or a committed `.env`.
- **`RUN_MIGRATIONS`**: leave `true` for the app to apply migrations on startup, or set `false` and run them out-of-band (see below) if you want migrations gated behind a separate deploy step.
- **`SYNC_MODE=daily_sync`** for the recurring production job; `full` only for initial load or recovery.
- **`LOG_LEVEL=info`** is the production default. `DEBUG` only gates heartbeat logs and does not change verbosity.

## Migrations

Migrations are embedded and applied via [golang-migrate](https://github.com/golang-migrate/migrate) when `RUN_MIGRATIONS=true`. To manage them out-of-band, set `RUN_MIGRATIONS=false` and apply the SQL in [`internal/db/migrations/`](../internal/db/migrations/) through your own migration step before the job runs.

A crashed migration can leave the `schema_migrations` table marked **dirty**, which blocks every subsequent run. Recovery:

1. Inspect: `SELECT version, dirty FROM schema_migrations;`
2. Verify whether the failed version's DDL actually applied.
3. Force the version once resolved: `migrate -path internal/db/migrations -database "$PGX_URL" force <version>`, then re-run the job.

## Kubernetes CronJob

A typical production schedule is **16:45 CET on weekdays**, a few minutes after the ECB publishes its daily reference rates.

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: fx-rates-daily
spec:
  schedule: "45 16 * * 1-5"        # 16:45, Mon–Fri (set the cluster TZ or use spec.timeZone)
  timeZone: "Europe/Berlin"
  concurrencyPolicy: Forbid         # never overlap runs
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 5
  jobTemplate:
    spec:
      activeDeadlineSeconds: 1860    # ~ sync timeout (30m) + setup headroom
      backoffLimit: 1                # one retry, then mark failed and alert
      template:
        spec:
          restartPolicy: Never
          containers:
            - name: fx-rates
              image: <registry>/fx-rates:v0.1.0
              securityContext:
                readOnlyRootFilesystem: true
                allowPrivilegeEscalation: false
                runAsNonRoot: true
                capabilities:
                  drop: ["ALL"]
              env:
                - name: SYNC_MODE
                  value: "daily_sync"
                - name: LOG_LEVEL
                  value: "info"
                - name: DB_HOST
                  value: "fx-rates-db.internal"
                - name: DB_PORT
                  value: "5432"
                - name: DB_NAME
                  value: "fx_rates"
                - name: DB_SSLMODE
                  value: "require"
              envFrom:
                - secretRef:
                    name: fx-rates-db-credentials   # DB_USER, DB_PASSWORD
              resources:
                requests: { cpu: "50m", memory: "64Mi" }
                limits:   { cpu: "500m", memory: "256Mi" }
```

Notes:
- `restartPolicy: Never` + `backoffLimit: 1` means one retry on failure, then the Job is marked failed — alert on that, not on individual pod restarts.
- `concurrencyPolicy: Forbid` prevents a slow run from overlapping the next schedule.
- The image runs as UID `65534` on a `scratch` base, so the `securityContext` above (read-only root FS, no privilege escalation, all capabilities dropped) is fully compatible — the job needs no writable filesystem.

## Initial backfill

The CronJob above only does the daily refresh. For the first load, run a one-off `full` sync (a `kubectl create job --from=cronjob/fx-rates-daily` with `SYNC_MODE=full` and a longer `activeDeadlineSeconds`, e.g. `21600` for 6h). Re-runs are safe — inserts use `ON CONFLICT DO NOTHING`, so there is no duplicate risk.

## Alerting

The job emits structured `metric` log events (`fx_rates_run`, `fx_rates_provider_run`) to stdout — see [`metrics.md`](./metrics.md) for the field contract. Wire a log shipper (Loki, Datadog, Vector) to derive alerts:

| Signal | Condition |
|---|---|
| Run failed | `fx_rates_run` with `status="failed"`, or Job exit code `1`. |
| Provider unavailable | `fx_rates_provider_run` with `status="unavailable"` (upstream 503 / fetch error). |
| Stale data | No `fx_rates_run` with `status="success"` within the last ~26h. |
| Run interrupted | `fx_rates_run` with `status="interrupted"` (deadline or eviction). |

## Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| Exit `1`, log `migrations failed` | Dirty `schema_migrations`, or unreachable DB during setup. | See [Migrations](#migrations); check DB connectivity and the 2m setup timeout. |
| Exit `1`, provider `status="unavailable"` | ECB endpoint returned 503 / network error. | Usually transient — next scheduled run recovers. Investigate if persistent. |
| Exit `130`, `status="interrupted"` | Pod evicted, or `activeDeadlineSeconds` hit before sync finished. | Raise `activeDeadlineSeconds` / `DAILY_SYNC_TIMEOUT`, or check why the run was slow. |
| `pool closing` shows `acquired_conns` > 0 at exit | A query held a connection past shutdown. | Inspect logs around the same `run_id`; check for slow upstream fetch. |
| `warning: database SSL is disabled` | `DB_SSLMODE=disable` in a production environment. | Set `DB_SSLMODE=require`. |

## See also

- [`database-schema.md`](./database-schema.md) — table layouts, idempotency contract, audit queries
- [`metrics.md`](./metrics.md) — structured log event contract
- [README](../README.md) — full configuration reference and Make targets
