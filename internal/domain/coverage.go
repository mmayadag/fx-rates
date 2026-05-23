package domain

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmayadag/fx-rates/internal/db/sqlcgen"
)

// UpsertCurrencySummary updates currency_coverages and currencies tables
// after a backfill. Mirrors Ruby Provider#refresh_currency_summaries.
func UpsertCurrencySummary(ctx context.Context, pool *pgxpool.Pool, providerKey string, isoCodes []string) error {
	q := sqlcgen.New(pool)
	for _, code := range isoCodes {
		rng, err := q.GetCurrencyDateRange(ctx, sqlcgen.GetCurrencyDateRangeParams{
			Provider: providerKey,
			Quote:    code,
		})
		if err != nil || !rng.StartDate.Valid {
			continue
		}

		if err := q.UpsertCurrencyCoverage(ctx, sqlcgen.UpsertCurrencyCoverageParams{
			ProviderKey: providerKey,
			IsoCode:     code,
			StartDate:   rng.StartDate,
			EndDate:     rng.EndDate,
		}); err != nil {
			return err
		}

		if err := q.UpsertCurrency(ctx, sqlcgen.UpsertCurrencyParams{
			IsoCode:   code,
			StartDate: rng.StartDate,
			EndDate:   rng.EndDate,
		}); err != nil {
			return err
		}
	}
	return nil
}
