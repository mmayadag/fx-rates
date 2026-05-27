package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mmayadag/fx-rates/internal/config"
	"github.com/mmayadag/fx-rates/internal/db"
	"github.com/mmayadag/fx-rates/internal/db/sqlcgen"
	"github.com/mmayadag/fx-rates/internal/validator"

	"github.com/jackc/pgx/v5/pgxpool"
)

type filters struct {
	dateFrom string
	dateTo   string
	base     string
	quote    string
	provider string
	limit    int
	output   string
	envFile  string
}

func main() {
	var cfg filters
	flag.StringVar(&cfg.dateFrom, "date-from", "", "inclusive start date (YYYY-MM-DD)")
	flag.StringVar(&cfg.dateTo, "date-to", "", "inclusive end date (YYYY-MM-DD)")
	flag.StringVar(&cfg.base, "base", "", "filter by base currency")
	flag.StringVar(&cfg.quote, "quote", "", "filter by quote currency")
	flag.StringVar(&cfg.provider, "provider", "ECB", "filter by provider (ECB only)")
	flag.IntVar(&cfg.limit, "limit", 100, "maximum number of rows to validate")
	flag.StringVar(&cfg.output, "output", "", "write CSV output to file instead of stdout")
	flag.StringVar(&cfg.envFile, "env-file", ".env", "path to env file")
	flag.Parse()

	if cfg.limit <= 0 {
		exitErr(fmt.Errorf("limit must be greater than zero"))
	}
	if err := validateDateFlag("date-from", cfg.dateFrom); err != nil {
		exitErr(err)
	}
	if err := validateDateFlag("date-to", cfg.dateTo); err != nil {
		exitErr(err)
	}
	providerFilter, err := normalizeProviderFilter(cfg.provider)
	if err != nil {
		exitErr(fmt.Errorf("only provider=ECB is supported"))
	}
	cfg.provider = providerFilter

	appCfg, err := config.Bootstrap(cfg.envFile)
	if err != nil {
		exitErr(err)
	}

	ctx := context.Background()
	pool, err := db.NewPool(ctx, appCfg.DatabaseURL, db.PoolOptions{
		MaxConns:          appCfg.DBMaxConns,
		MinConns:          appCfg.DBMinConns,
		MaxConnLifetime:   appCfg.DBMaxConnLifetime,
		MaxConnIdleTime:   appCfg.DBMaxConnIdleTime,
		HealthCheckPeriod: appCfg.DBHealthCheckPeriod,
	})
	if err != nil {
		exitErr(err)
	}
	defer pool.Close()

	records, err := fetchRecords(ctx, pool, cfg)
	if err != nil {
		exitErr(err)
	}
	if len(records) == 0 {
		fmt.Fprintln(os.Stderr, "no rows matched the requested filters")
		return
	}

	refClient := validator.NewReferenceClient(validator.CollectSymbols(records))
	results, summary, err := validator.Validate(ctx, refClient, records)
	if err != nil {
		exitErr(err)
	}

	writer, closer, err := outputWriter(cfg.output)
	if err != nil {
		exitErr(err)
	}
	if closer != nil {
		defer closer.Close()
	}

	if err := writeCSV(writer, results); err != nil {
		exitErr(err)
	}

	fmt.Fprintf(os.Stderr, "total=%d ok=%d warning=%d invalid=%d missing=%d\n",
		summary.Total, summary.OK, summary.Warning, summary.Invalid, summary.Missing)
}

func fetchRecords(ctx context.Context, pool *pgxpool.Pool, cfg filters) ([]validator.DBRecord, error) {
	rows, err := sqlcgen.New(pool).FetchRatesForValidation(ctx, sqlcgen.FetchRatesForValidationParams{
		DateFrom:    cfg.dateFrom,
		DateTo:      cfg.dateTo,
		BaseCode:    strings.ToUpper(strings.TrimSpace(cfg.base)),
		QuoteCode:   strings.ToUpper(strings.TrimSpace(cfg.quote)),
		ProviderKey: cfg.provider,
		RowLimit:    int32(cfg.limit),
	})
	if err != nil {
		return nil, err
	}

	records := make([]validator.DBRecord, 0, len(rows))
	for _, r := range rows {
		records = append(records, validator.DBRecord{
			Date:     r.Date.Time,
			Base:     strings.ToUpper(r.Base),
			Quote:    strings.ToUpper(r.Quote),
			Rate:     r.Rate,
			Provider: strings.ToUpper(r.Provider),
		})
	}
	return records, nil
}

// validateDateFlag rejects a non-empty date flag that isn't YYYY-MM-DD, giving
// a clear message up front instead of an opaque database cast error. An empty
// value means "no filter" and is allowed.
func validateDateFlag(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("-%s must be in YYYY-MM-DD format: %q", name, value)
	}
	return nil
}

func normalizeProviderFilter(value string) (string, error) {
	provider := strings.ToUpper(strings.TrimSpace(value))
	if provider == "" {
		return "ECB", nil
	}
	if provider != "ECB" {
		return "", fmt.Errorf("unsupported provider")
	}
	return provider, nil
}

func writeCSV(out io.Writer, results []validator.Result) error {
	w := csv.NewWriter(out)

	if err := w.Write([]string{
		"date",
		"base",
		"quote",
		"db_rate",
		"online_rate",
		"absolute_diff",
		"relative_diff_pct",
		"status",
		"notes",
	}); err != nil {
		return err
	}

	for _, result := range results {
		record := []string{
			result.Date,
			result.Base,
			result.Quote,
			floatString(result.DBRate),
			nullableFloatString(result.OnlineRate),
			nullableFloatString(result.AbsoluteDiff),
			nullableFloatString(result.RelativeDiffPct),
			result.Status,
			result.Notes,
		}
		if err := w.Write(record); err != nil {
			return err
		}
	}

	w.Flush()
	return w.Error()
}

func outputWriter(path string) (io.Writer, io.Closer, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return os.Stdout, nil, nil
	}

	clean := filepath.Clean(trimmed)
	for _, seg := range strings.Split(clean, string(os.PathSeparator)) {
		if seg == ".." {
			return nil, nil, fmt.Errorf("invalid -output path %q: must not contain '..' segments", path)
		}
	}

	file, err := os.Create(clean)
	if err != nil {
		return nil, nil, err
	}
	return file, file, nil
}

func floatString(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func nullableFloatString(v *float64) string {
	if v == nil {
		return ""
	}
	return floatString(*v)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
