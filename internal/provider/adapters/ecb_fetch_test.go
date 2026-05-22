package adapters

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mmayadag/fx-rates/internal/provider"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockTransport(body string, code int) http.RoundTripper {
	return rtFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: code,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})
}

func TestECB_BackfillRange(t *testing.T) {
	a := &ECB{}
	if a.BackfillRange() != 365 {
		t.Fatalf("expected 365, got %d", a.BackfillRange())
	}
}

func TestECB_Fetch_OK(t *testing.T) {
	orig := provider.DefaultClient.Transport
	defer func() { provider.DefaultClient.Transport = orig }()

	body := `FREQ,CURRENCY,CURRENCY_DENOM,EXR_TYPE,EXR_SUFFIX,TIME_PERIOD,OBS_VALUE,OBS_STATUS,OBS_CONF,OBS_PRE_BREAK,OBS_COM,TIME_FORMAT,BREAKS,COLLECTION,DISS_ORG,DOM_SER_IDS,PUBL_ECB,PUBL_MU,PUBL_PUBLIC,UNIT_INDEX_BASE,COMPILATION,COVERAGE,DECIMALS,NAT_TITLE,SOURCE_AGENCY,SOURCE_PUB,TITLE,TITLE_COMPL,UNIT,UNIT_MULT
D,USD,EUR,SP00,A,2024-01-02,1.094,A,F,,,,,,,,,,,,,,,,,,,,,,
`
	provider.DefaultClient.Transport = mockTransport(body, http.StatusOK)

	a := &ECB{}
	after := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	upto := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	recs, err := a.Fetch(after, upto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Base != "EUR" || recs[0].Quote != "USD" {
		t.Errorf("unexpected record: %+v", recs[0])
	}
}

func TestECB_Fetch_ZeroDates(t *testing.T) {
	orig := provider.DefaultClient.Transport
	defer func() { provider.DefaultClient.Transport = orig }()

	provider.DefaultClient.Transport = mockTransport(`FREQ,CURRENCY,TIME_PERIOD,OBS_VALUE
`, http.StatusOK)

	a := &ECB{}
	recs, err := a.Fetch(time.Time{}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	_ = recs
}

func TestECB_Fetch_HTTPError(t *testing.T) {
	orig := provider.DefaultClient.Transport
	defer func() { provider.DefaultClient.Transport = orig }()

	provider.DefaultClient.Transport = mockTransport("service unavailable", http.StatusServiceUnavailable)

	a := &ECB{}
	_, err := a.Fetch(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))
	if err == nil {
		t.Fatal("expected error on non-200 status")
	}
}
