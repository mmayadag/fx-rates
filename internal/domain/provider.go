package domain

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmayadag/fx-rates/internal/db/sqlcgen"
)

type Provider struct {
	Key           string     `db:"key"            json:"key"`
	Name          string     `db:"name"           json:"name"`
	CountryCode   *string    `db:"country_code"   json:"country_code"`
	RateType      *string    `db:"rate_type"      json:"rate_type"`
	PivotCurrency *string    `db:"pivot_currency" json:"pivot_currency"`
	DataURL       *string    `db:"data_url"       json:"data_url"`
	TermsURL      *string    `db:"terms_url"      json:"terms_url"`
	PublishTime   *int       `db:"publish_time"   json:"-"`
	PublishDays   *string    `db:"publish_days"   json:"-"`
	CoverageStart *time.Time `db:"coverage_start" json:"-"`
}

// LoadAll returns all providers from the database.
func LoadAll(ctx context.Context, pool *pgxpool.Pool) ([]Provider, error) {
	rows, err := sqlcgen.New(pool).ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	providers := make([]Provider, 0, len(rows))
	for _, r := range rows {
		providers = append(providers, Provider{
			Key:           r.Key,
			Name:          r.Name,
			CountryCode:   r.CountryCode,
			RateType:      r.RateType,
			PivotCurrency: r.PivotCurrency,
			DataURL:       r.DataUrl,
			TermsURL:      r.TermsUrl,
			PublishTime:   int32PtrToIntPtr(r.PublishTime),
			PublishDays:   r.PublishDays,
			CoverageStart: sqlcgen.DateToTimePtr(r.CoverageStart),
		})
	}
	return providers, nil
}

// GetLastSynced returns the most recent date for which rates exist for this provider.
// Returns nil if no rates have been imported yet.
func GetLastSynced(ctx context.Context, pool *pgxpool.Pool, providerKey string) (*time.Time, error) {
	d, err := sqlcgen.New(pool).GetLastSyncedForProvider(ctx, providerKey)
	if err != nil {
		return nil, err
	}
	return sqlcgen.DateToTimePtr(d), nil
}

// GetAllLastSynced returns the most recent synced date for every provider in a
// single query instead of one query per provider. Providers with no rates are
// omitted from the returned map.
func GetAllLastSynced(ctx context.Context, pool *pgxpool.Pool) (map[string]*time.Time, error) {
	rows, err := sqlcgen.New(pool).GetAllLastSynced(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*time.Time, len(rows))
	for _, r := range rows {
		result[r.Provider] = sqlcgen.DateToTimePtr(r.LastSynced)
	}
	return result, nil
}

func int32PtrToIntPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	i := int(*v)
	return &i
}
