package scheduler

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmayadag/fx-rates/internal/db"
	"github.com/mmayadag/fx-rates/internal/provider"
	"github.com/mmayadag/fx-rates/internal/registry"
)

// testPool returns a pgxpool connected to TEST_DATABASE_URL with migrations
// applied. Skips the test if the env var is not set.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	if err := db.RunMigrations(dsn); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	pool, err := db.NewPool(context.Background(), dsn, db.PoolOptions{MaxConns: 5})
	if err != nil {
		t.Fatalf("connecting to test DB: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedProvider inserts a providers row for key and registers cleanup of all
// rows the backfill may create for it (rates, sync_runs, currency_coverages
// via ON DELETE CASCADE).
func seedProvider(t *testing.T, pool *pgxpool.Pool, key string, coverageStart time.Time) {
	t.Helper()
	ctx := context.Background()
	cleanup := func() {
		_, _ = pool.Exec(ctx, `DELETE FROM rates WHERE provider = $1`, key)
		_, _ = pool.Exec(ctx, `DELETE FROM sync_runs WHERE provider = $1`, key)
		_, _ = pool.Exec(ctx, `DELETE FROM providers WHERE key = $1`, key)
	}
	cleanup()
	if _, err := pool.Exec(ctx, `INSERT INTO providers (key, name, coverage_start) VALUES ($1, $2, $3)`, key, key, coverageStart); err != nil {
		t.Fatalf("seeding provider %s: %v", key, err)
	}
	t.Cleanup(cleanup)
}

// syncRunRow is the subset of a sync_runs row the tests assert on.
type syncRunRow struct {
	Mode         string
	Status       string
	RowsFetched  int
	RowsInserted int
	ErrorMessage *string
}

// lastSyncRun fetches the most recent sync_runs row for a provider.
func lastSyncRun(t *testing.T, pool *pgxpool.Pool, key string) syncRunRow {
	t.Helper()
	var row syncRunRow
	err := pool.QueryRow(context.Background(), `
		SELECT mode, status, rows_fetched, rows_inserted, error_message
		FROM sync_runs WHERE provider = $1
		ORDER BY finished_at DESC LIMIT 1
	`, key).Scan(&row.Mode, &row.Status, &row.RowsFetched, &row.RowsInserted, &row.ErrorMessage)
	if err != nil {
		t.Fatalf("reading sync_runs for %s: %v", key, err)
	}
	return row
}

func TestBulkUpsertRatesIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	seedProvider(t, pool, "TESTBULK", time.Now().UTC())

	records := []provider.Record{
		{Provider: "TESTBULK", Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Base: "EUR", Quote: "USD", Rate: 1.09},
		{Provider: "TESTBULK", Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Base: "EUR", Quote: "GBP", Rate: 0.86},
	}

	inserted, err := bulkUpsertRates(ctx, pool, records)
	if err != nil {
		t.Fatalf("bulkUpsertRates: %v", err)
	}
	if inserted != 2 {
		t.Errorf("expected 2 inserted, got %d", inserted)
	}

	// Second insert of same records — conflicts, nothing new
	inserted2, err := bulkUpsertRates(ctx, pool, records)
	if err != nil {
		t.Fatalf("bulkUpsertRates (2nd): %v", err)
	}
	if inserted2 != 0 {
		t.Errorf("expected 0 on conflict, got %d", inserted2)
	}

	// Empty input
	inserted3, err := bulkUpsertRates(ctx, pool, nil)
	if err != nil {
		t.Fatalf("bulkUpsertRates (empty): %v", err)
	}
	if inserted3 != 0 {
		t.Errorf("expected 0 for empty input, got %d", inserted3)
	}
}

func TestBackfillProviderIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -2)
	seedProvider(t, pool, "TESTBP", start)

	adapter := &staticAdapter{records: []provider.Record{
		{Date: start.AddDate(0, 0, 1), Base: "EUR", Quote: "USD", Rate: 1.09},
	}}

	result := BackfillProvider(ctx, pool, "TESTBP", adapter, &start, nil, false, 0, nil)

	if result.Status != statusSuccess {
		t.Errorf("status = %q, want success; error: %s", result.Status, result.Error)
	}
	if result.Inserted != 1 {
		t.Errorf("inserted = %d, want 1", result.Inserted)
	}

	// Run again — already up to date
	result2 := BackfillProvider(ctx, pool, "TESTBP", adapter, &start, &today, false, 0, nil)
	if result2.Status != statusUpToDate {
		t.Errorf("2nd run status = %q, want up_to_date", result2.Status)
	}
}

func TestBackfillEntrySuccessIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -2)
	seedProvider(t, pool, "TESTOK", start)

	entry := registry.Entry{Key: "TESTOK", Adapter: &staticAdapter{records: []provider.Record{
		{Date: start.AddDate(0, 0, 1), Base: "EUR", Quote: "USD", Rate: 1.09},
	}}}

	// Debug mode also exercises the heartbeat goroutine and debug progress log.
	err := backfillEntry(ctx, pool, Options{Debug: true, HeartbeatInterval: 10 * time.Millisecond}, entry)
	if err != nil {
		t.Fatalf("backfillEntry: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM rates WHERE provider = 'TESTOK'`).Scan(&count); err != nil {
		t.Fatalf("counting rates: %v", err)
	}
	if count != 1 {
		t.Errorf("rates count = %d, want 1", count)
	}

	run := lastSyncRun(t, pool, "TESTOK")
	if run.Status != statusSuccess {
		t.Errorf("sync_runs status = %q, want success", run.Status)
	}
	if run.Mode != "full" {
		t.Errorf("sync_runs mode = %q, want full", run.Mode)
	}
	if run.RowsInserted != 1 {
		t.Errorf("sync_runs rows_inserted = %d, want 1", run.RowsInserted)
	}
}

func TestBackfillEntryEmptyDailySyncIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	seedProvider(t, pool, "TESTEMPTY", today.AddDate(0, 0, -2))

	entry := registry.Entry{Key: "TESTEMPTY", Adapter: &staticAdapter{}}

	err := backfillEntry(ctx, pool, Options{DailySync: true}, entry)
	if err != nil {
		t.Fatalf("backfillEntry: %v", err)
	}

	run := lastSyncRun(t, pool, "TESTEMPTY")
	if run.Status != statusEmpty {
		t.Errorf("sync_runs status = %q, want empty", run.Status)
	}
	if run.Mode != "daily_sync" {
		t.Errorf("sync_runs mode = %q, want daily_sync", run.Mode)
	}
}

func TestBackfillEntryUpToDateIntegration(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	seedProvider(t, pool, "TESTUTD", today.AddDate(0, 0, -2))

	// An existing rate dated today makes lastSynced >= today — nothing to do.
	if _, err := pool.Exec(ctx, `
		INSERT INTO rates (provider, date, base, quote, rate) VALUES ('TESTUTD', $1, 'EUR', 'USD', 1.09)
	`, today); err != nil {
		t.Fatalf("seeding rate: %v", err)
	}

	entry := registry.Entry{Key: "TESTUTD", Adapter: &staticAdapter{}}

	err := backfillEntry(ctx, pool, Options{}, entry)
	if err != nil {
		t.Fatalf("backfillEntry: %v", err)
	}

	run := lastSyncRun(t, pool, "TESTUTD")
	if run.Status != statusUpToDate {
		t.Errorf("sync_runs status = %q, want up_to_date", run.Status)
	}
}

func TestBackfillEntryErrorIntegration(t *testing.T) {
	pool := testPool(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	seedProvider(t, pool, "TESTERR", today.AddDate(0, 0, -2))

	entry := registry.Entry{Key: "TESTERR", Adapter: &failingAdapter{}}

	err := backfillEntry(context.Background(), pool, Options{}, entry)
	if err == nil {
		t.Fatal("expected error for failing provider, got nil")
	}
	if !strings.Contains(err.Error(), "TESTERR") {
		t.Errorf("error %q should name the failed provider", err)
	}

	run := lastSyncRun(t, pool, "TESTERR")
	if run.Status != statusError {
		t.Errorf("sync_runs status = %q, want error", run.Status)
	}
	if run.ErrorMessage == nil || *run.ErrorMessage == "" {
		t.Error("sync_runs error_message should be set")
	}
}

func TestBackfillEntryUnavailableIntegration(t *testing.T) {
	pool := testPool(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	seedProvider(t, pool, "TESTUNAV", today.AddDate(0, 0, -2))

	entry := registry.Entry{Key: "TESTUNAV", Adapter: &unavailableAdapter{}}

	// Unavailable is a degraded-but-expected outcome — the run still succeeds.
	err := backfillEntry(context.Background(), pool, Options{}, entry)
	if err != nil {
		t.Fatalf("backfillEntry: %v", err)
	}

	run := lastSyncRun(t, pool, "TESTUNAV")
	if run.Status != statusUnavailable {
		t.Errorf("sync_runs status = %q, want unavailable", run.Status)
	}
}

func TestBackfillEntryPreCancelledIntegration(t *testing.T) {
	pool := testPool(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	seedProvider(t, pool, "TESTPRE", today.AddDate(0, 0, -2))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	entry := registry.Entry{Key: "TESTPRE", Adapter: &staticAdapter{}}

	err := backfillEntry(ctx, pool, Options{}, entry)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// The audit row must exist even though the provider never ran.
	run := lastSyncRun(t, pool, "TESTPRE")
	if run.Status != statusInterrupted {
		t.Errorf("sync_runs status = %q, want interrupted", run.Status)
	}
}

func TestBackfillEntryCancelledMidRunIntegration(t *testing.T) {
	pool := testPool(t)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -2)
	seedProvider(t, pool, "TESTMID", start)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The adapter cancels the context during its fetch — the batch insert
	// then fails on the cancelled ctx, and the overall run reports the
	// cancellation rather than a provider failure.
	entry := registry.Entry{Key: "TESTMID", Adapter: &cancellingAdapter{
		cancel: cancel,
		records: []provider.Record{
			{Date: start.AddDate(0, 0, 1), Base: "EUR", Quote: "USD", Rate: 1.09},
		},
	}}

	err := backfillEntry(ctx, pool, Options{}, entry)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	// The audit row still lands (recordSyncRun uses a fresh background ctx)
	// with the provider's terminal error state.
	run := lastSyncRun(t, pool, "TESTMID")
	if run.Status != statusError {
		t.Errorf("sync_runs status = %q, want error", run.Status)
	}
}

func TestRecordSyncRunIntegration(t *testing.T) {
	pool := testPool(t)
	seedProvider(t, pool, "TESTREC", time.Now().UTC())

	started := time.Now().UTC().Add(-time.Minute)
	finished := time.Now().UTC()
	recordSyncRun(pool, BackfillResult{
		Provider: "TESTREC",
		Status:   statusError,
		Fetched:  10,
		Inserted: 7,
		Skipped:  3,
		Error:    "upstream 503",
	}, true, started, finished)

	run := lastSyncRun(t, pool, "TESTREC")
	if run.Status != statusError {
		t.Errorf("status = %q, want error", run.Status)
	}
	if run.Mode != "daily_sync" {
		t.Errorf("mode = %q, want daily_sync", run.Mode)
	}
	if run.RowsFetched != 10 || run.RowsInserted != 7 {
		t.Errorf("rows = %d/%d, want 10/7", run.RowsFetched, run.RowsInserted)
	}
	if run.ErrorMessage == nil || *run.ErrorMessage != "upstream 503" {
		t.Errorf("error_message = %v, want \"upstream 503\"", run.ErrorMessage)
	}
}

// staticAdapter returns a fixed set of records regardless of date range.
type staticAdapter struct {
	records []provider.Record
}

func (a *staticAdapter) BackfillRange() int { return 7 }
func (a *staticAdapter) Fetch(after, upto time.Time) ([]provider.Record, error) {
	return a.records, nil
}

// failingAdapter always returns a non-transient error.
type failingAdapter struct{}

func (a *failingAdapter) BackfillRange() int { return 7 }
func (a *failingAdapter) Fetch(after, upto time.Time) ([]provider.Record, error) {
	return nil, errors.New("upstream exploded")
}

// unavailableAdapter reports the provider as unavailable.
type unavailableAdapter struct{}

func (a *unavailableAdapter) BackfillRange() int { return 7 }
func (a *unavailableAdapter) Fetch(after, upto time.Time) ([]provider.Record, error) {
	return nil, &provider.Unavailable{Msg: "host unreachable"}
}

// cancellingAdapter cancels the run's context during fetch, then returns
// records normally — simulating a shutdown signal arriving mid-run.
type cancellingAdapter struct {
	cancel  context.CancelFunc
	records []provider.Record
}

func (a *cancellingAdapter) BackfillRange() int { return 7 }
func (a *cancellingAdapter) Fetch(after, upto time.Time) ([]provider.Record, error) {
	a.cancel()
	return a.records, nil
}
