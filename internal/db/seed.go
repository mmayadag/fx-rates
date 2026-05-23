package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmayadag/fx-rates/internal/db/sqlcgen"
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
	q := sqlcgen.New(pool)

	entries, err := seed.FS.ReadDir("data/providers")
	if err != nil {
		return fmt.Errorf("read seed dir: %w", err)
	}

	staleKeys, err := q.DeleteStaleProviders(ctx)
	if err != nil {
		return fmt.Errorf("delete stale providers: %w", err)
	}
	if len(staleKeys) > 0 {
		slices.Sort(staleKeys)
		slog.Warn("seed: stale providers removed", "count", len(staleKeys), "keys", staleKeys)
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

		coverageStart, err := parseCoverageStart(p.CoverageStart)
		if err != nil {
			return fmt.Errorf("parse coverage_start for %s: %w", p.Key, err)
		}

		if err := q.UpsertProvider(ctx, sqlcgen.UpsertProviderParams{
			Key:           p.Key,
			Name:          p.Name,
			CountryCode:   p.CountryCode,
			RateType:      p.RateType,
			PivotCurrency: p.PivotCurrency,
			DataUrl:       p.DataURL,
			TermsUrl:      p.TermsURL,
			PublishTime:   intPtrToInt32Ptr(p.PublishTime),
			PublishDays:   p.PublishDays,
			CoverageStart: coverageStart,
		}); err != nil {
			return fmt.Errorf("upsert provider %s: %w", p.Key, err)
		}
	}

	keys, err := q.ListProviderKeys(ctx)
	if err != nil {
		return fmt.Errorf("load provider keys: %w", err)
	}

	count, err := q.CountProviders(ctx)
	if err != nil {
		return fmt.Errorf("count providers: %w", err)
	}
	slog.Info("providers seeded", "count", count, "providers", keys)
	return nil
}

func parseCoverageStart(s *string) (pgtype.Date, error) {
	if s == nil || *s == "" {
		return pgtype.Date{Valid: false}, nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return pgtype.Date{}, err
	}
	return sqlcgen.TimeToDate(t), nil
}

func intPtrToInt32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	i := int32(*v)
	return &i
}
