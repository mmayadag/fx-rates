package db

import (
	"context"
	"os"
	"testing"
)

func testDBPool(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}
	if err := RunMigrations(dsn); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}
	return dsn
}

func TestSeedPreservesCURAPIAndRemovesStaleProviders(t *testing.T) {
	dsn := testDBPool(t)

	pool, err := NewPool(context.Background(), dsn, PoolOptions{MaxConns: 5})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	ctx := context.Background()
	_, err = pool.Exec(ctx, `DELETE FROM providers`)
	if err != nil {
		t.Fatalf("clear providers: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO providers (key, name, pivot_currency, data_url)
		VALUES
			('CURAPI', 'CurrencyAPI', 'USD', 'https://currencyapi.com/docs'),
			('STALE', 'Stale Provider', 'USD', 'https://example.test/stale')
	`)
	if err != nil {
		t.Fatalf("insert fixtures: %v", err)
	}

	if err := Seed(ctx, pool); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT key, name, pivot_currency, data_url
		FROM providers
		ORDER BY key
	`)
	if err != nil {
		t.Fatalf("query providers: %v", err)
	}
	defer rows.Close()

	type providerRow struct {
		key           string
		name          string
		pivotCurrency *string
		dataURL       *string
	}
	var got []providerRow
	for rows.Next() {
		var row providerRow
		if err := rows.Scan(&row.key, &row.name, &row.pivotCurrency, &row.dataURL); err != nil {
			t.Fatalf("scan provider: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate providers: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 providers after seed, got %d: %#v", len(got), got)
	}

	if got[0].key != "CURAPI" {
		t.Fatalf("expected CURAPI row first, got %#v", got[0])
	}
	if got[0].name != "CurrencyAPI" {
		t.Fatalf("expected CURAPI name preserved, got %q", got[0].name)
	}
	if got[0].pivotCurrency == nil || *got[0].pivotCurrency != "USD" {
		t.Fatalf("expected CURAPI pivot preserved, got %#v", got[0].pivotCurrency)
	}
	if got[0].dataURL == nil || *got[0].dataURL != "https://currencyapi.com/docs" {
		t.Fatalf("expected CURAPI data_url preserved, got %#v", got[0].dataURL)
	}

	if got[1].key != "ECB" {
		t.Fatalf("expected ECB row second, got %#v", got[1])
	}
}
