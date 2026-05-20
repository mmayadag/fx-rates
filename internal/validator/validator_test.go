package validator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestCrossRate(t *testing.T) {
	rates := map[string]float64{"USD": 1.1, "TRY": 38.5}

	tests := []struct {
		base, quote string
		wantOK      bool
		wantRate    float64
	}{
		{"USD", "TRY", true, 35.0},
		{"EUR", "USD", true, 1.1},
		{"USD", "EUR", true, 1 / 1.1},
		{"EUR", "XXX", false, 0},
		{"XXX", "USD", false, 0},
	}
	for _, tt := range tests {
		got, ok := crossRate(rates, tt.base, tt.quote)
		if ok != tt.wantOK {
			t.Errorf("crossRate(%s/%s) ok=%v want %v", tt.base, tt.quote, ok, tt.wantOK)
			continue
		}
		if tt.wantOK && got != tt.wantRate {
			t.Errorf("crossRate(%s/%s) = %v, want %v", tt.base, tt.quote, got, tt.wantRate)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		value float64
		want  string
	}{
		{0.0, statusOK},
		{0.1, statusOK},
		{0.10001, statusWarning},
		{1.0, statusWarning},
		{1.00001, statusInvalid},
		{50.0, statusInvalid},
	}
	for _, tt := range tests {
		if got := classify(tt.value); got != tt.want {
			t.Errorf("classify(%v) = %s, want %s", tt.value, got, tt.want)
		}
	}
}

func TestNoteFor(t *testing.T) {
	tests := []struct {
		requested, effective, source string
		want                         string
	}{
		{"2024-01-02", "2024-01-02", "src", "src"},
		{"2024-01-02", "2024-01-01", "src", "src; used previous business day 2024-01-01"},
		{"2024-01-02", "2024-01-01", "", "used previous business day 2024-01-01"},
		{"2024-01-02", "", "no data", "no data"},
	}
	for _, tt := range tests {
		got := noteFor(tt.requested, tt.effective, tt.source)
		if got != tt.want {
			t.Errorf("noteFor(%q,%q,%q) = %q, want %q", tt.requested, tt.effective, tt.source, got, tt.want)
		}
	}
}

func TestFloatPtr(t *testing.T) {
	v := 3.14
	p := floatPtr(v)
	if p == nil {
		t.Fatal("expected non-nil pointer")
	}
	if *p != v {
		t.Errorf("*floatPtr(%v) = %v", v, *p)
	}
}

func TestCollectSymbols(t *testing.T) {
	records := []DBRecord{
		{Base: "EUR", Quote: "USD"},
		{Base: "USD", Quote: "GBP"},
	}
	syms := CollectSymbols(records)
	if len(syms) != 4 {
		t.Errorf("expected 4 symbols, got %d: %v", len(syms), syms)
	}
}

func TestNewReferenceClient(t *testing.T) {
	c := NewReferenceClient([]string{"usd", "EUR", "GBP", "usd", "  ", "TOOLONG"})
	// EUR excluded, duplicates deduped, blanks/invalid stripped
	if len(c.symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d: %v", len(c.symbols), c.symbols)
	}
}

func TestValidateWithMockServer(t *testing.T) {
	payload := frankfurterResponse{
		Data: []struct {
			Date  string  `json:"date"`
			Base  string  `json:"base"`
			Quote string  `json:"quote"`
			Rate  float64 `json:"rate"`
		}{
			{Date: "2024-01-02", Base: "EUR", Quote: "USD", Rate: 1.094},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	c := &ReferenceClient{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			}),
		},
		symbols: []string{"USD"},
		cache:   map[string]referenceSnapshot{},
	}

	records := []DBRecord{
		{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Base: "EUR", Quote: "USD", Rate: 1.094},
	}
	results, summary, err := Validate(context.Background(), c, records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Total != 1 {
		t.Errorf("total = %d, want 1", summary.Total)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != statusOK {
		t.Errorf("status = %q, want OK", results[0].Status)
	}
}

func TestLookupRateSameCurrency(t *testing.T) {
	c := &ReferenceClient{cache: map[string]referenceSnapshot{}}
	rate, _, _, err := c.LookupRate(context.Background(), time.Now(), "USD", "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate == nil || *rate != 1.0 {
		t.Errorf("expected rate 1.0 for same currency, got %v", rate)
	}
}

func TestLookupRateInvalidCode(t *testing.T) {
	c := &ReferenceClient{cache: map[string]referenceSnapshot{}}
	rate, _, note, err := c.LookupRate(context.Background(), time.Now(), "US", "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != nil {
		t.Errorf("expected nil rate for invalid code, got %v", rate)
	}
	if note == "" {
		t.Error("expected non-empty note for invalid code")
	}
}

func TestLookupRateNoSymbols(t *testing.T) {
	// Empty symbols list → snapshotForDate returns empty rates → pair unsupported
	c := &ReferenceClient{
		httpClient: http.DefaultClient,
		symbols:    []string{},
		cache:      map[string]referenceSnapshot{},
	}
	rate, _, note, err := c.LookupRate(context.Background(), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), "EUR", "USD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rate != nil {
		t.Errorf("expected nil rate with no symbols, got %v", rate)
	}
	if note == "" {
		t.Error("expected note explaining missing pair")
	}
}

func TestSnapshotForDateCacheHit(t *testing.T) {
	cached := referenceSnapshot{EffectiveDate: "2024-01-02", Rates: map[string]float64{"USD": 1.09}}
	c := &ReferenceClient{
		cache: map[string]referenceSnapshot{"2024-01-02": cached},
	}
	snap, err := c.snapshotForDate(context.Background(), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.EffectiveDate != "2024-01-02" {
		t.Errorf("got %q, want 2024-01-02", snap.EffectiveDate)
	}
}

func TestValidateServerError(t *testing.T) {
	c := &ReferenceClient{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader("server error")),
					Header:     make(http.Header),
				}, nil
			}),
		},
		symbols: []string{"USD"},
		cache:   map[string]referenceSnapshot{},
	}
	records := []DBRecord{
		{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Base: "EUR", Quote: "USD", Rate: 1.09},
	}
	_, _, err := Validate(context.Background(), c, records)
	if err == nil {
		t.Fatal("expected error from server 500, got nil")
	}
}

func TestCrossRateZeroBaseRate(t *testing.T) {
	rates := map[string]float64{"USD": 0, "GBP": 0.87}
	_, ok := crossRate(rates, "USD", "EUR")
	if ok {
		t.Error("expected false for zero base rate in quote==EUR case")
	}
	_, ok = crossRate(rates, "USD", "GBP")
	if ok {
		t.Error("expected false for zero base rate in cross case")
	}
}

func TestValidateMissingStatus(t *testing.T) {
	// Empty symbols → snapshotForDate returns empty rates → pair unsupported → MISSING
	c := &ReferenceClient{
		httpClient: http.DefaultClient,
		symbols:    []string{},
		cache:      map[string]referenceSnapshot{},
	}
	records := []DBRecord{
		{Date: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC), Base: "EUR", Quote: "USD", Rate: 1.09},
	}
	results, summary, err := Validate(context.Background(), c, records)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Missing != 1 {
		t.Errorf("expected 1 missing, got %d", summary.Missing)
	}
	if results[0].Status != statusMissing {
		t.Errorf("status = %q, want MISSING", results[0].Status)
	}
}

func TestSnapshotForDateEmptyResponse(t *testing.T) {
	// Server returns valid JSON but with no matching dates
	payload := frankfurterResponse{}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	c := &ReferenceClient{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(string(body))),
					Header:     make(http.Header),
				}, nil
			}),
		},
		symbols: []string{"USD"},
		cache:   map[string]referenceSnapshot{},
	}
	snap, err := c.snapshotForDate(context.Background(), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.EffectiveDate != "" {
		t.Errorf("expected empty effective date for empty response, got %q", snap.EffectiveDate)
	}
}
