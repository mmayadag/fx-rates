package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmayadag/fx-rates/internal/seed"
)

type providerSeed struct {
	Key           string  `json:"key"`
	Name          string  `json:"name"`
	CountryCode   *string `json:"country_code"`
	RateType      *string `json:"rate_type"`
	PivotCurrency *string `json:"pivot_currency"`
	DataURL       *string `json:"data_url"`
	TermsURL      *string `json:"terms_url"`
	PublishTime   *int    `json:"publish_time"`
	PublishDays   *string `json:"publish_days"`
	CoverageStart *string `json:"coverage_start"`
}

// Seed loads providers from embedded seed data into the database.
// Idempotent — uses INSERT ... ON CONFLICT DO UPDATE.
func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	if err := seedProviders(ctx, pool); err != nil {
		return fmt.Errorf("seed providers: %w", err)
	}
	return nil
}

func seedProviders(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := seed.FS.ReadDir("data/providers")
	if err != nil {
		return fmt.Errorf("read seed dir: %w", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM providers WHERE key NOT IN ('ECB', 'CURAPI')`); err != nil {
		return fmt.Errorf("delete stale providers: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := seed.FS.ReadFile("data/providers/" + entry.Name())
		if err != nil {
			return err
		}

		var p providerSeed
		if err := json.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("parse %s: %w", entry.Name(), err)
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO providers (key, name, country_code, rate_type, pivot_currency, data_url, terms_url, publish_time, publish_days, coverage_start)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::date)
			ON CONFLICT (key) DO UPDATE SET
				name           = EXCLUDED.name,
				country_code   = EXCLUDED.country_code,
				rate_type      = EXCLUDED.rate_type,
				pivot_currency = EXCLUDED.pivot_currency,
				data_url       = EXCLUDED.data_url,
				terms_url      = EXCLUDED.terms_url,
				publish_time   = EXCLUDED.publish_time,
				publish_days   = EXCLUDED.publish_days,
				coverage_start = EXCLUDED.coverage_start
		`, p.Key, p.Name, p.CountryCode, p.RateType, p.PivotCurrency,
			p.DataURL, p.TermsURL, p.PublishTime, p.PublishDays, p.CoverageStart)
		if err != nil {
			return fmt.Errorf("upsert provider %s: %w", p.Key, err)
		}
	}

	keys, err := loadProviderKeys(ctx, pool)
	if err != nil {
		return fmt.Errorf("load provider keys: %w", err)
	}

	var count int
	pool.QueryRow(ctx, "SELECT COUNT(*) FROM providers").Scan(&count)
	slog.Info("providers seeded", "count", count, "providers", keys)
	return nil
}

func loadProviderKeys(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT key FROM providers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(keys)
	return keys, nil
}
