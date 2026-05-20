package main

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
