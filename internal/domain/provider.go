package domain

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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
	rows, err := pool.Query(ctx, `
		SELECT key, name, country_code, rate_type, pivot_currency,
		       data_url, terms_url, publish_time, publish_days, coverage_start
		FROM providers
		ORDER BY key
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(
			&p.Key, &p.Name, &p.CountryCode, &p.RateType, &p.PivotCurrency,
			&p.DataURL, &p.TermsURL, &p.PublishTime, &p.PublishDays, &p.CoverageStart,
		); err != nil {
			return nil, err
		}
		providers = append(providers, p)
	}
	return providers, rows.Err()
}

// GetLastSynced returns the most recent date for which rates exist for this provider.
// Returns nil if no rates have been imported yet.
func GetLastSynced(ctx context.Context, pool *pgxpool.Pool, providerKey string) (*time.Time, error) {
	var maxDate *time.Time
	err := pool.QueryRow(ctx,
		`SELECT MAX(date) FROM rates WHERE provider = $1`,
		providerKey,
	).Scan(&maxDate)
	if err != nil {
		return nil, err
	}
	return maxDate, nil
}

// GetAllLastSynced returns the most recent synced date for every provider in a
// single query instead of one query per provider. Providers with no rates are
// omitted from the returned map.
func GetAllLastSynced(ctx context.Context, pool *pgxpool.Pool) (map[string]*time.Time, error) {
	rows, err := pool.Query(ctx,
		`SELECT provider, MAX(date) FROM rates GROUP BY provider`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]*time.Time)
	for rows.Next() {
		var key string
		var maxDate *time.Time
		if err := rows.Scan(&key, &maxDate); err != nil {
			return nil, err
		}
		result[key] = maxDate
	}
	return result, rows.Err()
}
