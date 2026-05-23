package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmayadag/fx-rates/internal/db"
	"github.com/mmayadag/fx-rates/internal/validator"
)

func TestWriteCSV(t *testing.T) {
	onlineRate := 1.11
	absoluteDiff := 0.01
	relativeDiff := 0.9

	var buf bytes.Buffer
	err := writeCSV(&buf, []validator.Result{
		{
			Date:            "2024-01-02",
			Base:            "EUR",
			Quote:           "USD",
			DBRate:          1.10,
			OnlineRate:      &onlineRate,
			AbsoluteDiff:    &absoluteDiff,
			RelativeDiffPct: &relativeDiff,
			Status:          "OK",
			Notes:           "matched",
		},
	})
	if err != nil {
		t.Fatalf("writeCSV returned error: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv output: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if got := rows[0][0]; got != "date" {
		t.Fatalf("header[0] = %q, want date", got)
	}
	if got := rows[1][3]; got != "1.1" {
		t.Fatalf("db_rate = %q, want 1.1", got)
	}
	if got := rows[1][8]; got != "matched" {
		t.Fatalf("notes = %q, want matched", got)
	}
}

func TestOutputWriterStdout(t *testing.T) {
	writer, closer, err := outputWriter("")
	if err != nil {
		t.Fatalf("outputWriter returned error: %v", err)
	}
	if closer != nil {
		t.Fatal("expected nil closer for stdout")
	}
	if writer != os.Stdout {
		t.Fatal("expected stdout writer")
	}
}

func TestOutputWriterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")

	writer, closer, err := outputWriter(path)
	if err != nil {
		t.Fatalf("outputWriter returned error: %v", err)
	}
	t.Cleanup(func() {
		if closer != nil {
			_ = closer.Close()
		}
	})

	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := closer.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("file contents = %q, want hello", string(data))
	}
}

func TestFloatString(t *testing.T) {
	if got := floatString(12.34); got != "12.34" {
		t.Fatalf("floatString = %q", got)
	}
}

func TestNullableFloatString(t *testing.T) {
	if got := nullableFloatString(nil); got != "" {
		t.Fatalf("nullableFloatString(nil) = %q, want empty", got)
	}

	v := 2.5
	if got := nullableFloatString(&v); got != "2.5" {
		t.Fatalf("nullableFloatString = %q, want 2.5", got)
	}
}

func TestWriteCSVWithNilOptionalFields(t *testing.T) {
	var buf bytes.Buffer
	err := writeCSV(&buf, []validator.Result{
		{
			Date:   "2024-01-02",
			Base:   "EUR",
			Quote:  "GBP",
			DBRate: 0.85,
			// OnlineRate, AbsoluteDiff, RelativeDiffPct intentionally nil
			Status: "Missing",
			Notes:  "no online value",
		},
	})
	if err != nil {
		t.Fatalf("writeCSV: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	for i, idx := range []int{4, 5, 6} {
		if rows[1][idx] != "" {
			t.Errorf("expected empty at col %d (idx=%d), got %q", i, idx, rows[1][idx])
		}
	}
}

func TestWriteCSVEmptyResults(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCSV(&buf, nil); err != nil {
		t.Fatalf("writeCSV: %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(buf.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (header only), got %d", len(rows))
	}
}

// failingWriter returns an error on every Write call. Used to verify writeCSV
// surfaces underlying writer errors.
type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, fmt.Errorf("write failed") }

func TestWriteCSVPropagatesWriterError(t *testing.T) {
	err := writeCSV(failingWriter{}, []validator.Result{
		{Date: "2024-01-02", Base: "EUR", Quote: "USD", DBRate: 1.1, Status: "OK"},
	})
	if err == nil {
		t.Fatal("expected error from failing writer")
	}
}

func TestOutputWriterRejectsBadPath(t *testing.T) {
	// A nested path whose parent dir does not exist surfaces os.Create's error.
	_, _, err := outputWriter(filepath.Join(t.TempDir(), "missing-dir", "out.csv"))
	if err == nil {
		t.Fatal("expected error for invalid output path")
	}
}

func TestFloatStringFormats(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1.5, "1.5"},
		{-1.5, "-1.5"},
		{0.000001, "0.000001"},
		{1234567.89, "1234567.89"},
	}
	for _, tt := range tests {
		if got := floatString(tt.in); got != tt.want {
			t.Errorf("floatString(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFloatStringNonFinite(t *testing.T) {
	if got := floatString(math.NaN()); got != "NaN" {
		t.Errorf("floatString(NaN) = %q, want NaN", got)
	}
	if got := floatString(math.Inf(1)); got != "+Inf" {
		t.Errorf("floatString(+Inf) = %q, want +Inf", got)
	}
}

func TestNullableFloatStringFormats(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{-3.14, "-3.14"},
		{1.5, "1.5"},
	}
	for _, tt := range tests {
		v := tt.in
		if got := nullableFloatString(&v); got != tt.want {
			t.Errorf("nullableFloatString(&%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFetchRecordsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping integration test")
	}

	ctx := context.Background()
	if err := db.RunMigrations(dsn); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	pool, err := db.NewPool(ctx, dsn, db.PoolOptions{MaxConns: 2})
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	// Ensure a clean slate for the test rows we insert.
	if _, err := pool.Exec(ctx, `DELETE FROM rates WHERE provider = 'ECB' AND base = 'EUR' AND quote IN ('TST', 'TSX')`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO rates (provider, date, base, quote, rate) VALUES
			('ECB', '2024-10-01', 'EUR', 'TST', 1.10),
			('ECB', '2024-10-05', 'EUR', 'TST', 1.12),
			('ECB', '2024-10-05', 'EUR', 'TSX', 130.50)
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM rates WHERE provider = 'ECB' AND base = 'EUR' AND quote IN ('TST', 'TSX')`)
	})

	t.Run("date range filter", func(t *testing.T) {
		got, err := fetchRecords(ctx, pool, filters{
			dateFrom: "2024-10-01", dateTo: "2024-10-01",
			provider: "ECB", limit: 100,
		})
		if err != nil {
			t.Fatalf("fetchRecords: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 row in date range, got %d", len(got))
		}
		if got[0].Quote != "TST" {
			t.Errorf("Quote = %q, want TST", got[0].Quote)
		}
	})

	t.Run("quote filter", func(t *testing.T) {
		got, err := fetchRecords(ctx, pool, filters{
			quote:    "tsx", // case-insensitive normalisation
			provider: "ECB", limit: 100,
		})
		if err != nil {
			t.Fatalf("fetchRecords: %v", err)
		}
		if len(got) != 1 || got[0].Quote != "TSX" {
			t.Fatalf("expected exactly 1 TSX row, got %#v", got)
		}
	})

	t.Run("limit honoured", func(t *testing.T) {
		got, err := fetchRecords(ctx, pool, filters{
			provider: "ECB", limit: 1,
		})
		if err != nil {
			t.Fatalf("fetchRecords: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected limit=1 to return 1 row, got %d", len(got))
		}
	})
}
