package adapters

import (
	"strings"
	"testing"
	"time"
)

const ecbSampleCSV = `FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE,OBS_STATUS,OBS_CONF,OBS_PRE_BREAK,OBS_COM,TIME_FORMAT,BREAKS,COLLECTION,DISS_ORG,DOM_SER_IDS,PUBL_ECB,PUBL_MU,PUBL_PUBLIC,UNIT_INDEX_BASE,COMPILATION,COVERAGE,DECIMALS,NAT_TITLE,SOURCE_AGENCY,SOURCE_PUB,TITLE,TITLE_COMPL,UNIT,UNIT_MULT
D,USD,EUR,SP00,A,2024-01-02,1.094,A,F,,,,,,,,,,,,,,,,,,,,,,
D,GBP,EUR,SP00,A,2024-01-02,0.866,A,F,,,,,,,,,,,,,,,,,,,,,,
D,JPY,EUR,SP00,A,2024-01-02,157.04,A,F,,,,,,,,,,,,,,,,,,,,,,
D,USD,EUR,SP00,A,2024-01-03,1.096,A,F,,,,,,,,,,,,,,,,,,,,,,
`

func TestParseECBStream(t *testing.T) {
	records, err := parseECBStream(strings.NewReader(ecbSampleCSV))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 4 {
		t.Fatalf("expected 4 records, got %d", len(records))
	}

	// First record
	r := records[0]
	if r.Base != "EUR" {
		t.Errorf("Base = %q, want EUR", r.Base)
	}
	if r.Quote != "USD" {
		t.Errorf("Quote = %q, want USD", r.Quote)
	}
	if r.Rate != 1.094 {
		t.Errorf("Rate = %v, want 1.094", r.Rate)
	}
	wantDate := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	if !r.Date.Equal(wantDate) {
		t.Errorf("Date = %v, want %v", r.Date, wantDate)
	}
}

func TestParseECBStreamSkipsZeroRate(t *testing.T) {
	csv := `FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE,OBS_STATUS,OBS_CONF,OBS_PRE_BREAK,OBS_COM,TIME_FORMAT,BREAKS,COLLECTION,DISS_ORG,DOM_SER_IDS,PUBL_ECB,PUBL_MU,PUBL_PUBLIC,UNIT_INDEX_BASE,COMPILATION,COVERAGE,DECIMALS,NAT_TITLE,SOURCE_AGENCY,SOURCE_PUB,TITLE,TITLE_COMPL,UNIT,UNIT_MULT
D,USD,EUR,SP00,A,2024-01-02,0,A,F,,,,,,,,,,,,,,,,,,,,,,
`
	records, err := parseECBStream(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected 0 records for zero rate, got %d", len(records))
	}
}

func TestParseECBStreamSkipsNonDaily(t *testing.T) {
	csv := `FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE,OBS_STATUS,OBS_CONF,OBS_PRE_BREAK,OBS_COM,TIME_FORMAT,BREAKS,COLLECTION,DISS_ORG,DOM_SER_IDS,PUBL_ECB,PUBL_MU,PUBL_PUBLIC,UNIT_INDEX_BASE,COMPILATION,COVERAGE,DECIMALS,NAT_TITLE,SOURCE_AGENCY,SOURCE_PUB,TITLE,TITLE_COMPL,UNIT,UNIT_MULT
M,USD,EUR,SP00,A,2024-01,1.09,A,F,,,,,,,,,,,,,,,,,,,,,,
`
	records, err := parseECBStream(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("expected 0 records for monthly freq, got %d", len(records))
	}
}

func TestECBParseRowEdgeCases(t *testing.T) {
	headers := []string{"FREQ", "CURRENCY", "TIME_PERIOD", "OBS_VALUE"}

	t.Run("currency not 3 chars", func(t *testing.T) {
		row := []string{"D", "US", "2024-01-02", "1.09"}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for short currency code")
		}
	})
	t.Run("empty obs value", func(t *testing.T) {
		row := []string{"D", "USD", "2024-01-02", ""}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for empty obs value")
		}
	})
	t.Run("invalid date", func(t *testing.T) {
		row := []string{"D", "USD", "not-a-date", "1.09"}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for invalid date")
		}
	})
	t.Run("unparseable rate", func(t *testing.T) {
		row := []string{"D", "USD", "2024-01-02", "abc"}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for unparseable rate")
		}
	})
	t.Run("zero rate", func(t *testing.T) {
		row := []string{"D", "USD", "2024-01-02", "0"}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for zero rate")
		}
	})
	t.Run("negative rate", func(t *testing.T) {
		row := []string{"D", "USD", "2024-01-02", "-1.5"}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for negative rate")
		}
	})
	t.Run("NaN rate", func(t *testing.T) {
		row := []string{"D", "USD", "2024-01-02", "NaN"}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for NaN rate")
		}
	})
	t.Run("Inf rate", func(t *testing.T) {
		row := []string{"D", "USD", "2024-01-02", "+Inf"}
		if ecbParseRow(headers, row) != nil {
			t.Error("expected nil for +Inf rate")
		}
	})
}

func TestECBBufferCap(t *testing.T) {
	t.Run("default when env unset", func(t *testing.T) {
		t.Setenv("ECB_MAX_RESPONSE_BYTES", "")
		if got := ecbBufferCap(); got != defaultECBMaxBufferSize {
			t.Fatalf("ecbBufferCap() = %d, want %d", got, defaultECBMaxBufferSize)
		}
	})
	t.Run("env override", func(t *testing.T) {
		t.Setenv("ECB_MAX_RESPONSE_BYTES", "2048")
		if got := ecbBufferCap(); got != 2048 {
			t.Fatalf("ecbBufferCap() = %d, want 2048", got)
		}
	})
	t.Run("invalid env falls back to default", func(t *testing.T) {
		t.Setenv("ECB_MAX_RESPONSE_BYTES", "abc")
		if got := ecbBufferCap(); got != defaultECBMaxBufferSize {
			t.Fatalf("ecbBufferCap() = %d, want %d", got, defaultECBMaxBufferSize)
		}
	})
	t.Run("zero or negative env falls back to default", func(t *testing.T) {
		t.Setenv("ECB_MAX_RESPONSE_BYTES", "0")
		if got := ecbBufferCap(); got != defaultECBMaxBufferSize {
			t.Fatalf("ecbBufferCap() = %d, want %d", got, defaultECBMaxBufferSize)
		}
	})
}
