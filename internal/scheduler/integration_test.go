package scheduler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mmayadag/fx-rates/internal/db"
	"github.com/mmayadag/fx-rates/internal/provider"
)

// testPool returns a pgxpool connected to TEST_DATABASE_URL.
// Skips the test if the env var is not set.
func testPool(t *testing.T) interface{ Close() } {
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

func TestBulkUpsertRatesIntegration(t *testing.T) {
	rawPool := testPool(t)
	pool, ok := rawPool.(interface {
		Close()
		Begin(context.Context) (interface{ Rollback(context.Context) error }, error)
	})
	_ = pool
	_ = ok

	// Re-obtain as pgxpool.Pool via the db package
	dsn := os.Getenv("TEST_DATABASE_URL")
	pgpool, err := db.NewPool(context.Background(), dsn, db.PoolOptions{MaxConns: 5})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pgpool.Close()

	ctx := context.Background()

	// Clean slate for this provider
	_, _ = pgpool.Exec(ctx, `DELETE FROM rates WHERE provider = 'TEST'`)

	records := []provider.Record{
		{Provider: "TEST", Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Base: "EUR", Quote: "USD", Rate: 1.09},
		{Provider: "TEST", Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Base: "EUR", Quote: "GBP", Rate: 0.86},
	}

	inserted, err := bulkUpsertRates(ctx, pgpool, records)
	if err != nil {
		t.Fatalf("bulkUpsertRates: %v", err)
	}
	if inserted != 2 {
		t.Errorf("expected 2 inserted, got %d", inserted)
	}

	// Second insert of same records — conflicts, nothing new
	inserted2, err := bulkUpsertRates(ctx, pgpool, records)
	if err != nil {
		t.Fatalf("bulkUpsertRates (2nd): %v", err)
	}
	if inserted2 != 0 {
		t.Errorf("expected 0 on conflict, got %d", inserted2)
	}

	// Empty input
	inserted3, err := bulkUpsertRates(ctx, pgpool, nil)
	if err != nil {
		t.Fatalf("bulkUpsertRates (empty): %v", err)
	}
	if inserted3 != 0 {
		t.Errorf("expected 0 for empty input, got %d", inserted3)
	}

	// Cleanup
	_, _ = pgpool.Exec(ctx, `DELETE FROM rates WHERE provider = 'TEST'`)
}

func TestBackfillProviderIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	if err := db.RunMigrations(dsn); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	pgpool, err := db.NewPool(context.Background(), dsn, db.PoolOptions{MaxConns: 5})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pgpool.Close()

	ctx := context.Background()
	_, _ = pgpool.Exec(ctx, `DELETE FROM rates WHERE provider = 'TEST'`)

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -2)

	adapter := &staticAdapter{records: []provider.Record{
		{Date: start.AddDate(0, 0, 1), Base: "EUR", Quote: "USD", Rate: 1.09},
	}}

	result := BackfillProvider(ctx, pgpool, "TEST", adapter, &start, nil, false, nil)

	if result.Status != "success" {
		t.Errorf("status = %q, want success; error: %s", result.Status, result.Error)
	}
	if result.Inserted != 1 {
		t.Errorf("inserted = %d, want 1", result.Inserted)
	}

	// Run again — already up to date
	result2 := BackfillProvider(ctx, pgpool, "TEST", adapter, &start, &today, false, nil)
	if result2.Status != "up_to_date" {
		t.Errorf("2nd run status = %q, want up_to_date", result2.Status)
	}

	_, _ = pgpool.Exec(ctx, `DELETE FROM rates WHERE provider = 'TEST'`)
}

// staticAdapter returns a fixed set of records regardless of date range.
type staticAdapter struct {
	records []provider.Record
}

func (a *staticAdapter) BackfillRange() int { return 7 }
func (a *staticAdapter) Fetch(after, upto time.Time) ([]provider.Record, error) {
	return a.records, nil
}
