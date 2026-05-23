package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmayadag/fx-rates/internal/db/sqlcgen"
	"github.com/mmayadag/fx-rates/internal/domain"
	"github.com/mmayadag/fx-rates/internal/provider"
	"github.com/mmayadag/fx-rates/internal/registry"
)

const defaultConcurrency = 10

type Options struct {
	Concurrency       int
	Debug             bool
	HeartbeatInterval time.Duration
	DailySync         bool
}

type BackfillResult struct {
	Provider         string
	Fetched          int
	Inserted         int
	Skipped          int
	Status           string
	Error            string
	Unavailable      bool
	LastSyncedBefore *time.Time
	LastSyncedAfter  *time.Time
}

type runningProvider struct {
	StartedAt    time.Time
	Stage        string
	FetchAfter   time.Time
	Upto         time.Time
	Retry        int
	LastError    string
	LastActivity time.Time
}

type RunSummary struct {
	TotalProviders       int
	CompletedProviders   int
	SucceededProviders   int
	EmptyProviders       int
	FailedProviders      int
	FailedProviderNames  []string
	UnavailableProviders int
	UpToDateProviders    int
	InsertedCount        int
	SkippedCount         int
	FetchedCount         int
}

// ExcludedQuotes are currency codes that are never stored (e.g. IMF SDR).
var ExcludedQuotes = map[string]bool{"XDR": true}

// BackfillAll runs the one-shot backfill job for the single registered provider.
func BackfillAll(ctx context.Context, pool *pgxpool.Pool, opts Options) error {
	entries := registry.All()
	if len(entries) != 1 {
		return fmt.Errorf("expected exactly 1 registered provider, got %d", len(entries))
	}
	entry := entries[0]

	var mu sync.Mutex
	running := make(map[string]runningProvider)
	var completed int32
	summary := RunSummary{TotalProviders: 1}
	runStart := time.Now()

	if opts.Concurrency > 0 {
		slog.Info("backfill: concurrency configured", "concurrency", opts.Concurrency)
	} else {
		slog.Info("backfill: concurrency configured", "concurrency", defaultConcurrency)
	}
	slog.Info("backfill: providers queued", "total", 1)

	allProviders, err := domain.LoadAll(ctx, pool)
	if err != nil {
		return fmt.Errorf("loading providers: %w", err)
	}
	coverageStartMap := make(map[string]*time.Time, len(allProviders))
	for _, p := range allProviders {
		coverageStartMap[p.Key] = p.CoverageStart
	}

	lastSyncedMap, err := domain.GetAllLastSynced(ctx, pool)
	if err != nil {
		return fmt.Errorf("loading last synced dates: %w", err)
	}

	done := make(chan struct{})
	if opts.Debug {
		interval := opts.HeartbeatInterval
		if interval <= 0 {
			interval = 30 * time.Second
		}
		go emitHeartbeat(ctx, done, &mu, running, 1, &completed, interval)
	}

	if ctx.Err() != nil {
		close(done)
		summarySnapshot := snapshotSummary(&mu, summary)
		logCancellation(ctx, &mu, running, 1, completed)
		logRunSummary(summarySnapshot, true)
		emitRunMetric(summarySnapshot, opts.DailySync, time.Since(runStart), "interrupted")
		return ctx.Err()
	}

	mu.Lock()
	now := time.Now().UTC()
	running[entry.Key] = runningProvider{
		StartedAt:    now,
		Stage:        "starting",
		LastActivity: now,
	}
	mu.Unlock()

	providerStart := time.Now().UTC()
	result := BackfillProvider(ctx, pool, entry.Key, entry.Adapter, coverageStartMap[entry.Key], lastSyncedMap[entry.Key], opts.DailySync, func(event provider.FetchEvent) {
		mu.Lock()
		state := running[entry.Key]
		state.Stage = event.Stage
		state.FetchAfter = event.FetchAfter
		state.Upto = event.Upto
		state.Retry = event.Retry
		state.LastError = event.LastError
		state.LastActivity = time.Now().UTC()
		running[entry.Key] = state
		mu.Unlock()
	})

	completed = 1
	mu.Lock()
	delete(running, entry.Key)
	summary.CompletedProviders = 1
	summary.FetchedCount = result.Fetched
	summary.InsertedCount = result.Inserted
	summary.SkippedCount = result.Skipped
	switch result.Status {
	case "success":
		summary.SucceededProviders = 1
	case "empty":
		summary.EmptyProviders = 1
	case "error":
		summary.FailedProviders = 1
		summary.FailedProviderNames = append(summary.FailedProviderNames, entry.Key)
	case "unavailable":
		summary.UnavailableProviders = 1
	case "up_to_date":
		summary.UpToDateProviders = 1
	}
	snapshot := summary
	mu.Unlock()
	close(done)

	if opts.Debug {
		slog.Info(
			"backfill: progress",
			"provider", entry.Key,
			"progress", "1/1",
			"completed", 1,
			"error_count", snapshot.FailedProviders,
			"total", 1,
			"status", result.Status,
			"fetched", result.Fetched,
			"inserted", result.Inserted,
			"last_synced_before", result.LastSyncedBefore,
			"last_synced_after", result.LastSyncedAfter,
			"error", result.Error,
		)
	} else {
		switch result.Status {
		case "error":
			slog.Error("backfill: provider failed", "provider", entry.Key, "progress", "1/1", "error", result.Error, "last_synced_before", result.LastSyncedBefore, "last_synced_after", result.LastSyncedAfter)
		case "unavailable":
			slog.Warn("backfill: provider unavailable", "provider", entry.Key, "progress", "1/1", "msg", result.Error, "last_synced_before", result.LastSyncedBefore, "last_synced_after", result.LastSyncedAfter)
		default:
			slog.Info("backfill: progress", "provider", entry.Key, "progress", "1/1", "completed", 1, "total", 1, "status", result.Status, "last_synced_before", result.LastSyncedBefore, "last_synced_after", result.LastSyncedAfter)
		}
	}

	providerEnd := time.Now().UTC()
	emitProviderMetric(result, opts.DailySync, providerEnd.Sub(providerStart))
	recordSyncRun(pool, result, opts.DailySync, providerStart, providerEnd)
	logRunSummary(summary, false)
	if ctx.Err() != nil {
		emitRunMetric(snapshot, opts.DailySync, time.Since(runStart), "interrupted")
		logCancellation(ctx, &mu, running, 1, completed)
		return ctx.Err()
	}
	if snapshot.FailedProviders > 0 {
		emitRunMetric(snapshot, opts.DailySync, time.Since(runStart), "failed")
		return fmt.Errorf("backfill failed for provider %s", entry.Key)
	}
	emitRunMetric(snapshot, opts.DailySync, time.Since(runStart), "success")
	return nil
}

func emitHeartbeat(ctx context.Context, done <-chan struct{}, mu *sync.Mutex, running map[string]runningProvider, total int, completed *int32, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			mu.Lock()
			providers := make([]string, 0, len(running))
			for key, state := range running {
				providers = append(providers, formatRunningProvider(key, state, now))
			}
			mu.Unlock()
			slices.Sort(providers)

			slog.Info(
				"backfill: heartbeat",
				"completed", *completed,
				"total", total,
				"running_count", len(providers),
				"running_providers", providers,
			)
		}
	}
}

func logCancellation(ctx context.Context, mu *sync.Mutex, running map[string]runningProvider, total int, completed int32) {
	now := time.Now().UTC()
	mu.Lock()
	providers := make([]string, 0, len(running))
	for key, state := range running {
		providers = append(providers, formatRunningProvider(key, state, now))
	}
	mu.Unlock()
	slices.Sort(providers)

	msg := "backfill: cancelled"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		msg = "backfill: timed out"
	}

	slog.Warn(
		msg,
		"completed", completed,
		"total", total,
		"running_count", len(providers),
		"running_providers", providers,
	)
}

func snapshotSummary(mu *sync.Mutex, summary RunSummary) RunSummary {
	mu.Lock()
	defer mu.Unlock()

	snapshot := summary
	snapshot.FailedProviderNames = append([]string(nil), summary.FailedProviderNames...)
	return snapshot
}

// recordSyncRun persists a per-provider run row to the sync_runs audit table.
// Failure is logged but does not affect the backfill outcome — the audit trail
// is best-effort. Uses a fresh background context so it survives a parent
// ctx that has just been cancelled (timeout/interrupt).
func recordSyncRun(pool *pgxpool.Pool, result BackfillResult, dailySync bool, startedAt, finishedAt time.Time) {
	mode := "full"
	if dailySync {
		mode = "daily_sync"
	}
	var errMsg *string
	if result.Error != "" {
		s := result.Error
		errMsg = &s
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := sqlcgen.New(pool).RecordSyncRun(ctx, sqlcgen.RecordSyncRunParams{
		Provider:     result.Provider,
		Mode:         mode,
		Status:       result.Status,
		StartedAt:    sqlcgen.TimeToTimestamptz(startedAt),
		FinishedAt:   sqlcgen.TimeToTimestamptz(finishedAt),
		RowsFetched:  int32(result.Fetched),
		RowsInserted: int32(result.Inserted),
		RowsSkipped:  int32(result.Skipped),
		ErrorMessage: errMsg,
	})
	if err != nil {
		slog.Warn("sync_runs: record failed (audit only)", "provider", result.Provider, "err", err)
	}
}

func logRunSummary(summary RunSummary, interrupted bool) {
	msg := "backfill: summary"
	if interrupted {
		msg = "backfill: partial summary"
	}

	slog.Info(
		msg,
		"providers_total", summary.TotalProviders,
		"providers_completed", summary.CompletedProviders,
		"providers_succeeded", summary.SucceededProviders,
		"providers_empty", summary.EmptyProviders,
		"providers_failed", summary.FailedProviders,
		"providers_unavailable", summary.UnavailableProviders,
		"providers_up_to_date", summary.UpToDateProviders,
		"fetched_count", summary.FetchedCount,
		"inserted_count", summary.InsertedCount,
		"skipped_count", summary.SkippedCount,
		"failed_providers", summary.FailedProviderNames,
	)
}

// emitProviderMetric emits a structured metric event for a single provider
// after its backfill returns. The "metric" msg + "name" field form a stable
// contract for log-based metric extractors (Loki, Datadog, Vector, etc.).
func emitProviderMetric(result BackfillResult, dailySync bool, duration time.Duration) {
	mode := "full"
	if dailySync {
		mode = "daily_sync"
	}
	var lastSyncedUnix int64
	if result.LastSyncedAfter != nil {
		lastSyncedUnix = result.LastSyncedAfter.Unix()
	}
	slog.Info("metric",
		"name", "fx_rates_provider_run",
		"provider", result.Provider,
		"mode", mode,
		"status", result.Status,
		"rows_fetched", result.Fetched,
		"rows_inserted", result.Inserted,
		"rows_skipped", result.Skipped,
		"duration_ms", duration.Milliseconds(),
		"last_synced_unix", lastSyncedUnix,
	)
}

// emitRunMetric emits a structured metric event for the overall backfill run.
func emitRunMetric(summary RunSummary, dailySync bool, duration time.Duration, status string) {
	mode := "full"
	if dailySync {
		mode = "daily_sync"
	}
	slog.Info("metric",
		"name", "fx_rates_run",
		"mode", mode,
		"status", status,
		"providers_total", summary.TotalProviders,
		"providers_succeeded", summary.SucceededProviders,
		"providers_failed", summary.FailedProviders,
		"providers_unavailable", summary.UnavailableProviders,
		"providers_up_to_date", summary.UpToDateProviders,
		"rows_inserted", summary.InsertedCount,
		"rows_skipped", summary.SkippedCount,
		"duration_ms", duration.Milliseconds(),
	)
}

// BackfillProvider fetches and stores all missing rates for one provider.
func BackfillProvider(ctx context.Context, pool *pgxpool.Pool, key string, a provider.Adapter, coverageStart *time.Time, lastSynced *time.Time, dailySync bool, observe provider.FetchObserver) BackfillResult {
	result := BackfillResult{Provider: key}
	startedAt := time.Now()
	result.LastSyncedBefore = cloneTimePtr(lastSynced)
	result.LastSyncedAfter = cloneTimePtr(lastSynced)

	after := getStartDate(key, coverageStart, lastSynced)
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if after != nil && !after.Before(today) {
		slog.Info("backfill: up to date", "provider", key, "last_synced_before", result.LastSyncedBefore, "last_synced_after", result.LastSyncedAfter)
		result.Status = "up_to_date"
		return result
	}

	start := time.Time{}
	if after != nil {
		start = *after
	}

	start, startNote := adjustStartForMode(key, start, today, dailySync)
	if startNote != "" {
		slog.Info("backfill: adjusted start", "provider", key, "after", start, "note", startNote)
	}

	slog.Info("backfill: starting", "provider", key, "after", start)

	var fetchedTotal int
	var insertedTotal int
	var skippedTotal int
	var maxSeenDate *time.Time

	err := provider.FetchEachObserved(ctx, a, start, observe, func(records []provider.Record) error {
		fetchedTotal += len(records)
		if len(records) == 0 {
			return nil
		}

		var valid []provider.Record
		for _, r := range records {
			if ExcludedQuotes[r.Quote] || ExcludedQuotes[r.Base] {
				continue
			}
			if r.Rate <= 0 || math.IsNaN(r.Rate) || math.IsInf(r.Rate, 0) {
				slog.Warn("invalid rate skipped", "provider", key, "base", r.Base, "quote", r.Quote, "rate", r.Rate)
				continue
			}
			r.Provider = key
			valid = append(valid, r)
			maxSeenDate = maxTimePtr(maxSeenDate, &r.Date)
		}
		if len(valid) == 0 {
			return nil
		}

		inserted, err := bulkUpsertRates(ctx, pool, valid)
		if err != nil {
			return err
		}
		insertedTotal += inserted
		skippedTotal += len(valid) - inserted
		slog.Debug("backfill: inserted rates", "provider", key, "count", inserted)

		if inserted > 0 {
			currencies := uniqueCurrencies(valid)
			if err := domain.UpsertCurrencySummary(ctx, pool, key, currencies); err != nil {
				slog.Warn("backfill: currency summary failed", "provider", key, "err", err)
			}
		}
		return nil
	})

	if err != nil {
		var unavail *provider.Unavailable
		if ok := isUnavailable(err, &unavail); ok {
			slog.Warn("backfill: provider unavailable", "provider", key, "msg", unavail.Msg)
			result.Status = "unavailable"
			result.Unavailable = true
			result.Error = unavail.Msg
			result.Fetched = fetchedTotal
			result.Inserted = insertedTotal
			result.Skipped = skippedTotal
			return result
		}
		if provider.IsTransient(err) {
			slog.Error("backfill: transient error", "provider", key, "err", err)
			result.Status = "error"
			result.Error = err.Error()
			result.Fetched = fetchedTotal
			result.Inserted = insertedTotal
			result.Skipped = skippedTotal
			return result
		}
		slog.Error("backfill: error", "provider", key, "err", err)
		result.Status = "error"
		result.Error = err.Error()
		result.Fetched = fetchedTotal
		result.Inserted = insertedTotal
		result.Skipped = skippedTotal
		return result
	}

	if maxSeenDate != nil {
		result.LastSyncedAfter = maxTimePtr(result.LastSyncedAfter, maxSeenDate)
	}

	if fetchedTotal == 0 && insertedTotal == 0 && result.LastSyncedBefore == nil {
		slog.Warn("backfill: empty result", "provider", key, "fetched", fetchedTotal, "inserted", insertedTotal, "skipped", skippedTotal, "duration_ms", time.Since(startedAt).Milliseconds(), "last_synced_before", result.LastSyncedBefore, "last_synced_after", result.LastSyncedAfter)
		result.Status = "empty"
		result.Fetched = fetchedTotal
		result.Inserted = insertedTotal
		result.Skipped = skippedTotal
		return result
	}

	slog.Info("backfill: completed", "provider", key, "fetched", fetchedTotal, "inserted", insertedTotal, "skipped", skippedTotal, "duration_ms", time.Since(startedAt).Milliseconds(), "last_synced_before", result.LastSyncedBefore, "last_synced_after", result.LastSyncedAfter)
	result.Status = "success"
	result.Fetched = fetchedTotal
	result.Inserted = insertedTotal
	result.Skipped = skippedTotal
	return result
}

func formatRunningProvider(key string, state runningProvider, now time.Time) string {
	parts := []string{now.Sub(state.StartedAt).Round(time.Second).String()}
	if state.Stage != "" {
		parts = append(parts, "stage="+state.Stage)
	}
	if !state.FetchAfter.IsZero() {
		window := state.FetchAfter.Format(time.DateOnly)
		if !state.Upto.IsZero() {
			window += ".." + state.Upto.Format(time.DateOnly)
		} else {
			window += "..today"
		}
		parts = append(parts, "window="+window)
	}
	if state.Retry > 0 {
		parts = append(parts, fmt.Sprintf("retry=%d", state.Retry))
	}
	if state.LastError != "" {
		parts = append(parts, "last_error="+truncateLogValue(state.LastError, 80))
	}
	if !state.LastActivity.IsZero() {
		parts = append(parts, "idle="+now.Sub(state.LastActivity).Round(time.Second).String())
	}
	return fmt.Sprintf("%s(%s)", key, strings.Join(parts, ", "))
}

func truncateLogValue(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max || max <= 3 {
		return value
	}
	return value[:max-3] + "..."
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func maxTimePtr(current *time.Time, candidate *time.Time) *time.Time {
	if candidate == nil {
		return cloneTimePtr(current)
	}
	if current == nil || candidate.After(*current) {
		return cloneTimePtr(candidate)
	}
	return cloneTimePtr(current)
}

func adjustStartForMode(key string, start, today time.Time, dailySync bool) (time.Time, string) {
	_ = key
	_ = today
	if !dailySync {
		return start, ""
	}
	return start, ""
}

// getStartDate returns lastSynced if rates exist, otherwise coverageStart.
func getStartDate(_ string, coverageStart *time.Time, lastSynced *time.Time) *time.Time {
	if lastSynced != nil {
		return lastSynced
	}
	return coverageStart
}

// bulkUpsertRates inserts rates, ignoring conflicts (same provider+date+base+quote).
// Returns number of newly inserted rows.
func bulkUpsertRates(ctx context.Context, pool *pgxpool.Pool, records []provider.Record) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE rates_stage (
			provider VARCHAR(10) NOT NULL,
			date     DATE        NOT NULL,
			base     VARCHAR(3)  NOT NULL,
			quote    VARCHAR(3)  NOT NULL,
			rate     DOUBLE PRECISION NOT NULL
		) ON COMMIT DROP
	`)
	if err != nil {
		return 0, err
	}

	rows := make([][]any, 0, len(records))
	for _, r := range records {
		rows = append(rows, []any{r.Provider, r.Date, r.Base, r.Quote, r.Rate})
	}

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"rates_stage"},
		[]string{"provider", "date", "base", "quote", "rate"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, err
	}

	var inserted int
	err = tx.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO rates (provider, date, base, quote, rate)
			SELECT provider, date, base, quote, rate
			FROM rates_stage
			ON CONFLICT (provider, date, base, quote) DO NOTHING
			RETURNING 1
		)
		SELECT COUNT(*) FROM inserted
	`).Scan(&inserted)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	return inserted, nil
}

func uniqueCurrencies(records []provider.Record) []string {
	seen := map[string]bool{}
	var out []string
	add := func(c string) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	for _, r := range records {
		add(r.Base)
		add(r.Quote)
	}
	return out
}

func isUnavailable(err error, target **provider.Unavailable) bool {
	u, ok := err.(*provider.Unavailable)
	if ok {
		*target = u
	}
	return ok
}
