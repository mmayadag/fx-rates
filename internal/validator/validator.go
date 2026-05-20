package validator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mmayadag/fx-rates/internal/provider"
)

const (
	statusOK      = "OK"
	statusWarning = "WARNING"
	statusInvalid = "INVALID"
	statusMissing = "MISSING"
)

const frankfurterRatesURL = "https://api.frankfurter.dev/v2/rates"

type DBRecord struct {
	Date     time.Time
	Base     string
	Quote    string
	Rate     float64
	Provider string
}

type Result struct {
	Date            string
	Base            string
	Quote           string
	DBRate          float64
	OnlineRate      *float64
	AbsoluteDiff    *float64
	RelativeDiffPct *float64
	Status          string
	Notes           string
}

type Summary struct {
	Total   int
	OK      int
	Warning int
	Invalid int
	Missing int
}

type ReferenceClient struct {
	httpClient *http.Client
	symbols    []string
	cache      map[string]referenceSnapshot
}

type referenceSnapshot struct {
	EffectiveDate string
	Rates         map[string]float64
}

type frankfurterResponse struct {
	Data []struct {
		Date  string  `json:"date"`
		Base  string  `json:"base"`
		Quote string  `json:"quote"`
		Rate  float64 `json:"rate"`
	} `json:"data"`
}

func NewReferenceClient(symbols []string) *ReferenceClient {
	uniq := make(map[string]struct{}, len(symbols))
	for _, symbol := range symbols {
		symbol = strings.ToUpper(strings.TrimSpace(symbol))
		if len(symbol) != 3 || symbol == "EUR" {
			continue
		}
		uniq[symbol] = struct{}{}
	}

	out := make([]string, 0, len(uniq))
	for symbol := range uniq {
		out = append(out, symbol)
	}
	sort.Strings(out)

	return &ReferenceClient{
		httpClient: provider.DefaultClient,
		symbols:    out,
		cache:      make(map[string]referenceSnapshot),
	}
}

func Validate(ctx context.Context, client *ReferenceClient, records []DBRecord) ([]Result, Summary, error) {
	results := make([]Result, 0, len(records))
	var summary Summary

	for _, record := range records {
		result, err := validateOne(ctx, client, record)
		if err != nil {
			return nil, summary, err
		}
		results = append(results, result)

		summary.Total++
		switch result.Status {
		case statusOK:
			summary.OK++
		case statusWarning:
			summary.Warning++
		case statusInvalid:
			summary.Invalid++
		default:
			summary.Missing++
		}
	}

	return results, summary, nil
}

func validateOne(ctx context.Context, client *ReferenceClient, record DBRecord) (Result, error) {
	result := Result{
		Date:   record.Date.Format("2006-01-02"),
		Base:   record.Base,
		Quote:  record.Quote,
		DBRate: record.Rate,
		Status: statusMissing,
	}

	rate, effectiveDate, note, err := client.LookupRate(ctx, record.Date, record.Base, record.Quote)
	if err != nil {
		return result, err
	}
	if rate == nil {
		result.Notes = note
		return result, nil
	}

	absDiff := math.Abs(record.Rate - *rate)
	relDiffPct := 0.0
	if *rate != 0 {
		relDiffPct = (absDiff / *rate) * 100
	}

	result.OnlineRate = rate
	result.AbsoluteDiff = floatPtr(absDiff)
	result.RelativeDiffPct = floatPtr(relDiffPct)
	result.Status = classify(relDiffPct)
	result.Notes = noteFor(record.Date.Format("2006-01-02"), effectiveDate, note)

	return result, nil
}

func (c *ReferenceClient) LookupRate(ctx context.Context, date time.Time, base, quote string) (*float64, string, string, error) {
	base = strings.ToUpper(strings.TrimSpace(base))
	quote = strings.ToUpper(strings.TrimSpace(quote))

	if len(base) != 3 || len(quote) != 3 {
		return nil, "", "invalid currency code", nil
	}
	if base == quote {
		rate := 1.0
		return &rate, date.Format("2006-01-02"), "", nil
	}

	snapshot, err := c.snapshotForDate(ctx, date)
	if err != nil {
		return nil, "", "", err
	}
	if snapshot.EffectiveDate == "" {
		return nil, "", "no ECB reference found in lookback window", nil
	}

	rate, ok := crossRate(snapshot.Rates, base, quote)
	if !ok {
		return nil, snapshot.EffectiveDate, "pair unsupported by ECB reference set", nil
	}

	note := "reference source: Frankfurter v2 providers=ECB"
	return &rate, snapshot.EffectiveDate, note, nil
}

func (c *ReferenceClient) snapshotForDate(ctx context.Context, date time.Time) (referenceSnapshot, error) {
	key := date.Format("2006-01-02")
	if cached, ok := c.cache[key]; ok {
		return cached, nil
	}

	if len(c.symbols) == 0 {
		snapshot := referenceSnapshot{
			EffectiveDate: key,
			Rates:         map[string]float64{},
		}
		c.cache[key] = snapshot
		return snapshot, nil
	}

	from := date.AddDate(0, 0, -7).Format("2006-01-02")
	params := url.Values{}
	params.Set("from", from)
	params.Set("to", key)
	params.Set("base", "EUR")
	params.Set("quotes", strings.Join(c.symbols, ","))
	params.Set("providers", "ECB")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, frankfurterRatesURL+"?"+params.Encode(), nil)
	if err != nil {
		return referenceSnapshot{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return referenceSnapshot{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return referenceSnapshot{}, fmt.Errorf("reference API returned status %d", resp.StatusCode)
	}

	var payload frankfurterResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return referenceSnapshot{}, err
	}

	latestDate := ""
	rates := make(map[string]float64)
	for _, item := range payload.Data {
		if item.Base != "EUR" || item.Date > key {
			continue
		}
		if latestDate == "" || item.Date > latestDate {
			latestDate = item.Date
			rates = map[string]float64{}
		}
		if item.Date == latestDate {
			rates[strings.ToUpper(item.Quote)] = item.Rate
		}
	}

	snapshot := referenceSnapshot{
		EffectiveDate: latestDate,
		Rates:         rates,
	}
	c.cache[key] = snapshot
	return snapshot, nil
}

func crossRate(eurRates map[string]float64, base, quote string) (float64, bool) {
	switch {
	case base == "EUR":
		rate, ok := eurRates[quote]
		return rate, ok
	case quote == "EUR":
		baseRate, ok := eurRates[base]
		if !ok || baseRate == 0 {
			return 0, false
		}
		return 1 / baseRate, true
	default:
		baseRate, okBase := eurRates[base]
		quoteRate, okQuote := eurRates[quote]
		if !okBase || !okQuote || baseRate == 0 {
			return 0, false
		}
		return quoteRate / baseRate, true
	}
}

func classify(relativeDiffPct float64) string {
	switch {
	case relativeDiffPct <= 0.1:
		return statusOK
	case relativeDiffPct <= 1:
		return statusWarning
	default:
		return statusInvalid
	}
}

func noteFor(requestedDate, effectiveDate, source string) string {
	switch {
	case effectiveDate == "":
		return source
	case effectiveDate != requestedDate && source != "":
		return fmt.Sprintf("%s; used previous business day %s", source, effectiveDate)
	case effectiveDate != requestedDate:
		return fmt.Sprintf("used previous business day %s", effectiveDate)
	default:
		return source
	}
}

func CollectSymbols(records []DBRecord) []string {
	symbols := make([]string, 0, len(records)*2)
	for _, record := range records {
		symbols = append(symbols, record.Base, record.Quote)
	}
	return symbols
}

func floatPtr(v float64) *float64 {
	return &v
}
