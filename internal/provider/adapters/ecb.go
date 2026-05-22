package adapters

import (
	"bufio"
	"encoding/csv"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmayadag/fx-rates/internal/provider"
)

const ecbSDMXURL = "https://data-api.ecb.europa.eu/service/data/EXR/D..EUR.SP00.A"

type ECB struct{}

func (a *ECB) BackfillRange() int { return 365 }

func (a *ECB) Fetch(after, upto time.Time) ([]provider.Record, error) {
	params := url.Values{"format": {"csvdata"}}
	if !after.IsZero() {
		params.Set("startPeriod", after.Format("2006-01-02"))
	}
	if !upto.IsZero() {
		params.Set("endPeriod", upto.Format("2006-01-02"))
	}
	fullURL := ecbSDMXURL + "?" + params.Encode()

	resp, err := provider.DefaultClient.Get(fullURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &provider.HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
		}
	}

	return parseECBStream(resp.Body)
}

func parseECBStream(r io.Reader) ([]provider.Record, error) {
	var records []provider.Record
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var headers []string
	for scanner.Scan() {
		line := scanner.Text()
		if headers == nil {
			cr := csv.NewReader(strings.NewReader(line))
			cr.LazyQuotes = true
			row, err := cr.Read()
			if err == nil {
				headers = row
			}
			continue
		}
		cr := csv.NewReader(strings.NewReader(line))
		cr.LazyQuotes = true
		row, err := cr.Read()
		if err != nil {
			continue
		}
		rec := ecbParseRow(headers, row)
		if rec != nil {
			records = append(records, *rec)
		}
	}
	return records, scanner.Err()
}

func ecbParseRow(headers, row []string) *provider.Record {
	idx := func(name string) string {
		for i, h := range headers {
			if h == name && i < len(row) {
				return row[i]
			}
		}
		return ""
	}
	if idx("FREQ") != "D" {
		return nil
	}
	quote := idx("CURRENCY")
	if len(quote) != 3 {
		return nil
	}
	obsVal := idx("OBS_VALUE")
	if obsVal == "" {
		return nil
	}
	rate, err := strconv.ParseFloat(obsVal, 64)
	if err != nil || rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return nil
	}
	dateStr := idx("TIME_PERIOD")
	d, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil
	}
	return &provider.Record{Date: d, Base: "EUR", Quote: quote, Rate: rate}
}

// Ensure interface
var _ provider.Adapter = (*ECB)(nil)
