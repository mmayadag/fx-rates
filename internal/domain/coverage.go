package domain

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UpsertCurrencySummary updates currency_coverages and currencies tables
// after a backfill. Mirrors Ruby Provider#refresh_currency_summaries.
func UpsertCurrencySummary(ctx context.Context, pool *pgxpool.Pool, providerKey string, isoCodes []string) error {
	for _, code := range isoCodes {
		var startDate, endDate *time.Time
		err := pool.QueryRow(ctx, `
			SELECT MIN(date), MAX(date)
			FROM rates
			WHERE provider = $1 AND (quote = $2 OR base = $2)
		`, providerKey, code).Scan(&startDate, &endDate)
		if err != nil || startDate == nil {
			continue
		}

		// Upsert currency_coverages (per-provider range)
		_, err = pool.Exec(ctx, `
			INSERT INTO currency_coverages (provider_key, iso_code, start_date, end_date)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (provider_key, iso_code) DO UPDATE SET
				start_date = LEAST(currency_coverages.start_date, EXCLUDED.start_date),
				end_date   = GREATEST(currency_coverages.end_date, EXCLUDED.end_date)
		`, providerKey, code, startDate, endDate)
		if err != nil {
			return err
		}

		// Upsert global currencies (cross-provider range)
		_, err = pool.Exec(ctx, `
			INSERT INTO currencies (iso_code, start_date, end_date)
			VALUES ($1, $2, $3)
			ON CONFLICT (iso_code) DO UPDATE SET
				start_date = LEAST(currencies.start_date, EXCLUDED.start_date),
				end_date   = GREATEST(currencies.end_date, EXCLUDED.end_date)
		`, code, startDate, endDate)
		if err != nil {
			return err
		}
	}
	return nil
}
