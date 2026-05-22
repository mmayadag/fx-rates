package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Record is a single exchange rate fetched by an adapter.
type Record struct {
	Date     time.Time
	Base     string
	Quote    string
	Rate     float64
	Provider string // populated by the scheduler after fetch
}

// Unavailable is returned when a provider is temporarily down or returns no data.
type Unavailable struct{ Msg string }

func (e *Unavailable) Error() string { return e.Msg }

// Adapter is the interface every provider adapter must implement.
// Fetch returns all records from after (exclusive) up to upto (inclusive).
// Passing zero values means "fetch all available".
type Adapter interface {
	// Fetch retrieves exchange rate records.
	// after: start date (exclusive), zero means fetch from beginning.
	// upto:  end date (inclusive), zero means fetch up to today.
	Fetch(after, upto time.Time) ([]Record, error)

	// BackfillRange returns how many days per chunk to use when backfilling.
	// Return 0 to fetch all data in a single request (no chunking).
	BackfillRange() int
}

type FetchEvent struct {
	Stage      string
	FetchAfter time.Time
	Upto       time.Time
	Retry      int
	LastError  string
}

type FetchObserver func(FetchEvent)

// transientErrors is the set of errors that warrant retrying.
var transientErrors = []error{
	syscall.ECONNRESET,
	syscall.EPIPE,
	io.EOF,
}

// HTTPStatusError represents a non-2xx HTTP response from an upstream provider.
// Adapters should return this (rather than fmt.Errorf) so the retry layer can
// distinguish transient (5xx, 429) from permanent (4xx) statuses.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("upstream returned http %d", e.StatusCode)
	}
	return fmt.Sprintf("upstream returned http %d: %s", e.StatusCode, e.Body)
}

// IsTransient returns true for errors that should be retried.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	for _, te := range transientErrors {
		if errors.Is(err, te) {
			return true
		}
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == 429 || (statusErr.StatusCode >= 500 && statusErr.StatusCode < 600)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// FetchEach calls fn for each batch of records, chunking by BackfillRange days.
// Mirrors Ruby's Adapter.fetch_each exactly.
func FetchEach(ctx context.Context, a Adapter, after time.Time, fn func([]Record) error) error {
	return FetchEachObserved(ctx, a, after, nil, fn)
}

// FetchEachObserved behaves like FetchEach and emits lifecycle events for the
// current fetch window, retry state, and last transient error.
func FetchEachObserved(ctx context.Context, a Adapter, after time.Time, observe FetchObserver, fn func([]Record) error) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if !after.IsZero() && !after.Before(today) {
		return nil // up to date
	}

	backfillRange := a.BackfillRange()
	const maxRetries = 5
	cursor := after

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		fetchAfter := cursor
		if !fetchAfter.IsZero() {
			fetchAfter = fetchAfter.AddDate(0, 0, 1)
		}

		var upto time.Time
		if !fetchAfter.IsZero() && backfillRange > 0 {
			upto = fetchAfter.AddDate(0, 0, backfillRange-1)
			if !upto.Before(today) {
				upto = time.Time{} // let adapter decide (fetch up to today)
			}
		}
		emitFetchEvent(observe, FetchEvent{
			Stage:      "fetching",
			FetchAfter: fetchAfter,
			Upto:       upto,
		})

		var records []Record
		var err error
		retries := 0
		for {
			records, err = a.Fetch(fetchAfter, upto)
			if err == nil {
				emitFetchEvent(observe, FetchEvent{
					Stage:      "fetched",
					FetchAfter: fetchAfter,
					Upto:       upto,
					Retry:      retries,
				})
				break
			}
			if !IsTransient(err) {
				emitFetchEvent(observe, FetchEvent{
					Stage:      "failed",
					FetchAfter: fetchAfter,
					Upto:       upto,
					Retry:      retries,
					LastError:  err.Error(),
				})
				return err
			}
			retries++
			if retries > maxRetries {
				emitFetchEvent(observe, FetchEvent{
					Stage:      "failed",
					FetchAfter: fetchAfter,
					Upto:       upto,
					Retry:      retries,
					LastError:  err.Error(),
				})
				return err
			}
			emitFetchEvent(observe, FetchEvent{
				Stage:      "retrying",
				FetchAfter: fetchAfter,
				Upto:       upto,
				Retry:      retries,
				LastError:  err.Error(),
			})
			select {
			case <-time.After(time.Duration(1<<retries) * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if len(records) > 0 {
			if err := fn(records); err != nil {
				return err
			}
		}
		emitFetchEvent(observe, FetchEvent{
			Stage:      "processed",
			FetchAfter: fetchAfter,
			Upto:       upto,
			Retry:      retries,
		})

		if upto.IsZero() {
			break // single-shot fetch complete
		}
		cursor = upto
		if !cursor.Before(today) {
			break
		}
	}
	return nil
}

func emitFetchEvent(observe FetchObserver, event FetchEvent) {
	if observe != nil {
		observe(event)
	}
}

// DefaultClient is the shared HTTP client for all adapters.
var DefaultClient = &http.Client{
	Timeout: 60 * time.Second,
}
