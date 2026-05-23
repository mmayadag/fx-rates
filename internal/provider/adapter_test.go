package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
	"time"
)

type fetchEachStub struct {
	ranges    [][2]time.Time
	fetchErr  error
	callLimit int // stop returning error after this many calls
}

func (s *fetchEachStub) BackfillRange() int { return 7 }

func (s *fetchEachStub) Fetch(after, upto time.Time) ([]Record, error) {
	s.ranges = append(s.ranges, [2]time.Time{after, upto})
	if s.fetchErr != nil && (s.callLimit == 0 || len(s.ranges) <= s.callLimit) {
		return nil, s.fetchErr
	}
	return nil, nil
}

func TestFetchEachStartsAfterExclusiveDate(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -20)
	stub := &fetchEachStub{}

	if err := FetchEach(context.Background(), stub, start, func([]Record) error { return nil }); err != nil {
		t.Fatalf("FetchEach returned error: %v", err)
	}
	if len(stub.ranges) == 0 {
		t.Fatal("expected at least one fetch call")
	}

	got := stub.ranges[0][0]
	want := start.AddDate(0, 0, 1)
	if !got.Equal(want) {
		t.Fatalf("expected first fetch start %s, got %s", want.Format(time.DateOnly), got.Format(time.DateOnly))
	}
}

func TestFetchEachUpToDate(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	stub := &fetchEachStub{}

	if err := FetchEach(context.Background(), stub, today, func([]Record) error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(stub.ranges) != 0 {
		t.Fatalf("expected no fetch calls when up to date, got %d", len(stub.ranges))
	}
}

func TestFetchEachContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	stub := &fetchEachStub{}

	err := FetchEach(ctx, stub, start, func([]Record) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestFetchEachSingleShot(t *testing.T) {
	type singleShot struct{}
	type singleShotAdapter struct{ called int }
	_ = singleShot{}

	adapter := &struct {
		fetchEachStub
	}{}
	adapter.fetchEachStub.ranges = nil

	// BackfillRange=0 means single shot — override via wrapper
	singleShotStub := &zeroRangeStub{}
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -5)

	if err := FetchEach(context.Background(), singleShotStub, start, func([]Record) error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(singleShotStub.ranges) != 1 {
		t.Fatalf("expected exactly 1 fetch call for single-shot adapter, got %d", len(singleShotStub.ranges))
	}
}

type zeroRangeStub struct {
	ranges [][2]time.Time
}

func (z *zeroRangeStub) BackfillRange() int { return 0 }
func (z *zeroRangeStub) Fetch(after, upto time.Time) ([]Record, error) {
	z.ranges = append(z.ranges, [2]time.Time{after, upto})
	return nil, nil
}

func TestFetchEachNonTransientErrorAborts(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -10)
	want := errors.New("fatal error")
	stub := &fetchEachStub{fetchErr: want, callLimit: 1}

	err := FetchEach(context.Background(), stub, start, func([]Record) error { return nil })
	if !errors.Is(err, want) {
		t.Fatalf("expected fatal error, got %v", err)
	}
}

func TestFetchEachMaxRetriesExceeded(t *testing.T) {
	// Always returns transient error → should exhaust maxRetries and return error.
	// Use a cancelled context to short-circuit the backoff sleeps.
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	stub := &fetchEachStub{fetchErr: syscall.ECONNRESET} // callLimit=0 means always fail

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after the first retry attempt begins
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := FetchEach(ctx, stub, start, func([]Record) error { return nil })
	if err == nil {
		t.Fatal("expected error when retries exhausted or context cancelled")
	}
}

func TestFetchEachObservedEmitsLifecycleEvents(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3)
	stub := &fetchEachStub{callLimit: 1}

	var events []FetchEvent
	err := FetchEachObserved(context.Background(), stub, start, func(event FetchEvent) {
		events = append(events, event)
	}, func([]Record) error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("expected lifecycle events, got %d", len(events))
	}
	if events[0].Stage != "fetching" {
		t.Fatalf("first event = %q, want fetching", events[0].Stage)
	}
	if events[1].Stage != "fetched" {
		t.Fatalf("second event = %q, want fetched", events[1].Stage)
	}
	last := events[len(events)-1]
	if last.Stage != "processed" {
		t.Fatalf("last event = %q, want processed", last.Stage)
	}
}

func TestFetchEachObservedEmitsRetryEvent(t *testing.T) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	stub := &fetchEachStub{fetchErr: syscall.ECONNRESET, callLimit: 1}

	var events []FetchEvent
	err := FetchEachObserved(context.Background(), stub, start, func(event FetchEvent) {
		events = append(events, event)
	}, func([]Record) error { return nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundRetry := false
	for _, event := range events {
		if event.Stage == "retrying" {
			foundRetry = true
			if event.Retry != 1 {
				t.Fatalf("retry event Retry = %d, want 1", event.Retry)
			}
			if event.LastError == "" {
				t.Fatal("expected retry event to include last error")
			}
		}
	}
	if !foundRetry {
		t.Fatal("expected retrying event")
	}
}

func TestIsTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"ECONNRESET", syscall.ECONNRESET, true},
		{"EPIPE", syscall.EPIPE, true},
		{"EOF", io.EOF, true},
		{"other", errors.New("other"), false},
		{"net.Error timeout", &mockNetError{timeout: true}, true},
		{"net.Error non-timeout", &mockNetError{timeout: false}, false},
		{"http 200", &HTTPStatusError{StatusCode: 200}, false},
		{"http 400", &HTTPStatusError{StatusCode: 400}, false},
		{"http 404", &HTTPStatusError{StatusCode: 404}, false},
		{"http 429", &HTTPStatusError{StatusCode: 429}, true},
		{"http 500", &HTTPStatusError{StatusCode: 500}, true},
		{"http 502", &HTTPStatusError{StatusCode: 502}, true},
		{"http 503", &HTTPStatusError{StatusCode: 503}, true},
		{"http 504", &HTTPStatusError{StatusCode: 504}, true},
		{"http 599", &HTTPStatusError{StatusCode: 599}, true},
		{"http 600", &HTTPStatusError{StatusCode: 600}, false},
		{"wrapped http 503", fmt.Errorf("fetch failed: %w", &HTTPStatusError{StatusCode: 503}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTransient(tt.err); got != tt.want {
				t.Fatalf("IsTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type mockNetError struct{ timeout bool }

func (e *mockNetError) Error() string   { return "mock net error" }
func (e *mockNetError) Timeout() bool   { return e.timeout }
func (e *mockNetError) Temporary() bool { return false }

func TestRetryBackoffStaysWithinJitterEnvelope(t *testing.T) {
	for retry := 0; retry < 6; retry++ {
		base := time.Duration(1<<retry) * time.Second
		minD := base - base/10
		maxD := base + base/10
		for i := 0; i < 20; i++ {
			got := retryBackoff(retry)
			if got < minD || got > maxD {
				t.Errorf("retry=%d got %v, expected within [%v, %v]", retry, got, minD, maxD)
			}
		}
	}
}
