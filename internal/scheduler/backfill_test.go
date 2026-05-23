package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmayadag/fx-rates/internal/provider"
)

func TestAdjustStartForMode_DailyWithinLookbackUnchanged(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -3)

	got, note := adjustStartForMode("ECB", start, today, true, 7)
	if !got.Equal(start) {
		t.Fatalf("got %v, want %v (start within 7-day floor)", got, start)
	}
	if note != "" {
		t.Fatalf("expected empty note when uncapped, got %q", note)
	}
}

func TestAdjustStartForMode_DailyCapsWhenBeyondLookback(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -30)
	floor := today.AddDate(0, 0, -7)

	got, note := adjustStartForMode("ECB", start, today, true, 7)
	if !got.Equal(floor) {
		t.Fatalf("got %v, want floor %v", got, floor)
	}
	if note == "" {
		t.Fatal("expected non-empty note when capping")
	}
}

func TestAdjustStartForMode_DailyZeroStartUsesFloor(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	floor := today.AddDate(0, 0, -7)

	got, note := adjustStartForMode("ECB", time.Time{}, today, true, 7)
	if !got.Equal(floor) {
		t.Fatalf("got %v, want floor %v", got, floor)
	}
	if note == "" {
		t.Fatal("expected non-empty note when starting from floor")
	}
}

func TestAdjustStartForMode_DailyLookbackZeroDisablesCap(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -365)

	got, note := adjustStartForMode("ECB", start, today, true, 0)
	if !got.Equal(start) {
		t.Fatalf("got %v, want %v (lookback=0 should disable cap)", got, start)
	}
	if note != "" {
		t.Fatalf("expected empty note when cap disabled, got %q", note)
	}
}

func TestGetStartDate(t *testing.T) {
	coverageStart := ptr(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	lastSynced := ptr(time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC))

	t.Run("returns lastSynced when set", func(t *testing.T) {
		got := getStartDate("ECB", coverageStart, lastSynced)
		if !got.Equal(*lastSynced) {
			t.Fatalf("got %v, want %v", got, *lastSynced)
		}
	})

	t.Run("falls back to coverageStart when no lastSynced", func(t *testing.T) {
		got := getStartDate("ECB", coverageStart, nil)
		if !got.Equal(*coverageStart) {
			t.Fatalf("got %v, want %v", got, *coverageStart)
		}
	})

	t.Run("returns nil when both nil", func(t *testing.T) {
		if got := getStartDate("ECB", nil, nil); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})
}

func TestRateValidation(t *testing.T) {
	if !ExcludedQuotes["XDR"] {
		t.Fatal("XDR should be in ExcludedQuotes")
	}

	records := []provider.Record{
		{Base: "EUR", Quote: "USD", Rate: 1.1},
		{Base: "EUR", Quote: "XDR", Rate: 1.5},
		{Base: "EUR", Quote: "GBP", Rate: -1.0},
		{Base: "EUR", Quote: "CHF", Rate: 0},
	}

	var valid []provider.Record
	for _, r := range records {
		if ExcludedQuotes[r.Quote] || ExcludedQuotes[r.Base] {
			continue
		}
		if r.Rate <= 0 {
			continue
		}
		valid = append(valid, r)
	}

	if len(valid) != 1 || valid[0].Quote != "USD" {
		t.Fatalf("unexpected valid records: %#v", valid)
	}
}

func TestUniqueCurrencies(t *testing.T) {
	records := []provider.Record{
		{Base: "EUR", Quote: "USD"},
		{Base: "EUR", Quote: "GBP"},
		{Base: "USD", Quote: "EUR"},
	}

	got := uniqueCurrencies(records)
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Fatalf("duplicate currency %q", c)
		}
		seen[c] = true
	}
	for _, want := range []string{"EUR", "USD", "GBP"} {
		if !seen[want] {
			t.Fatalf("missing currency %q in %v", want, got)
		}
	}
}

func TestIsUnavailable(t *testing.T) {
	t.Run("matches Unavailable", func(t *testing.T) {
		u := &provider.Unavailable{Msg: "down"}
		var target *provider.Unavailable
		if !isUnavailable(u, &target) {
			t.Fatal("expected true")
		}
		if target.Msg != "down" {
			t.Fatalf("got %q, want down", target.Msg)
		}
	})

	t.Run("non-Unavailable error", func(t *testing.T) {
		var target *provider.Unavailable
		if isUnavailable(errors.New("other"), &target) {
			t.Fatal("expected false")
		}
	})
}

func TestSnapshotSummary(t *testing.T) {
	var mu sync.Mutex
	orig := RunSummary{
		FailedProviders:     1,
		FailedProviderNames: []string{"ECB"},
	}
	snap := snapshotSummary(&mu, orig)

	orig.FailedProviderNames[0] = "CHANGED"
	if snap.FailedProviderNames[0] != "ECB" {
		t.Fatalf("snapshot mutated: %q", snap.FailedProviderNames[0])
	}
}

func TestCloneTimePtr(t *testing.T) {
	if cloneTimePtr(nil) != nil {
		t.Fatal("expected nil")
	}

	orig := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cloned := cloneTimePtr(&orig)
	orig = orig.AddDate(0, 0, 1)
	if cloned.Equal(orig) {
		t.Fatal("clone should be independent")
	}
}

func TestMaxTimePtr(t *testing.T) {
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		current, candidate *time.Time
		want               *time.Time
	}{
		{nil, nil, nil},
		{nil, &d2, &d2},
		{&d1, nil, &d1},
		{&d1, &d2, &d2},
		{&d2, &d1, &d2},
	}

	for _, tt := range tests {
		got := maxTimePtr(tt.current, tt.candidate)
		if tt.want == nil {
			if got != nil {
				t.Fatalf("expected nil, got %v", got)
			}
			continue
		}
		if got == nil || !got.Equal(*tt.want) {
			t.Fatalf("maxTimePtr(%v, %v) = %v, want %v", tt.current, tt.candidate, got, tt.want)
		}
	}
}

func TestFormatRunningProvider(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	state := runningProvider{
		StartedAt:    now.Add(-30 * time.Second),
		Stage:        "retrying",
		FetchAfter:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Upto:         time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC),
		Retry:        2,
		LastError:    "transient upstream failure",
		LastActivity: now.Add(-3 * time.Second),
	}

	got := formatRunningProvider("ECB", state, now)
	for _, want := range []string{
		"ECB(",
		"stage=retrying",
		"window=2026-04-01..2026-04-30",
		"retry=2",
		"last_error=transient upstream failure",
		"idle=3s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestTruncateLogValue(t *testing.T) {
	got := truncateLogValue("abcdefghijklmnopqrstuvwxyz", 10)
	if got != "abcdefg..." {
		t.Fatalf("got %q", got)
	}
}

func TestLogRunSummaryIncludesEmptyProviders(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	logRunSummary(RunSummary{
		TotalProviders: 1,
		EmptyProviders: 1,
	}, false)

	output := buf.String()
	if !strings.Contains(output, "\"providers_empty\":1") {
		t.Fatalf("expected providers_empty in summary log, got %s", output)
	}
}

func ptr(t time.Time) *time.Time { return &t }

func TestLogRunSummary_Interrupted(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	logRunSummary(RunSummary{TotalProviders: 1}, true)

	output := buf.String()
	if !strings.Contains(output, "partial summary") {
		t.Fatalf("expected 'partial summary' in output, got: %s", output)
	}
}

func TestFormatRunningProvider_NoUpto(t *testing.T) {
	now := time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	state := runningProvider{
		StartedAt:    now.Add(-10 * time.Second),
		Stage:        "fetching",
		FetchAfter:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		Upto:         time.Time{}, // zero → "..today"
		LastActivity: now.Add(-1 * time.Second),
	}
	got := formatRunningProvider("ECB", state, now)
	if !strings.Contains(got, "..today") {
		t.Fatalf("expected '..today' in output, got: %q", got)
	}
}

func TestAdjustStartForMode_NotDailySync(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -30)
	got, note := adjustStartForMode("ECB", start, today, false, 7)
	if !got.Equal(start) {
		t.Fatalf("full mode should not cap; got %v, want %v", got, start)
	}
	if note != "" {
		t.Fatalf("expected empty note in full mode, got %q", note)
	}
}

func TestLogCancellation_Timeout(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// wait for deadline
	<-ctx.Done()

	var mu sync.Mutex
	logCancellation(ctx, &mu, make(map[string]runningProvider), 5, 3)

	output := buf.String()
	if !strings.Contains(output, "timed out") {
		t.Fatalf("expected 'timed out' in log, got: %s", output)
	}
}

func TestLogCancellation_Cancelled(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var mu sync.Mutex
	logCancellation(ctx, &mu, make(map[string]runningProvider), 3, 1)

	output := buf.String()
	if !strings.Contains(output, "cancelled") {
		t.Fatalf("expected 'cancelled' in log, got: %s", output)
	}
}

func TestEmitHeartbeat_Fires(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	prev := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(prev)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var mu sync.Mutex
	running := map[string]runningProvider{}
	var completed int32 = 1

	go emitHeartbeat(ctx, done, &mu, running, 3, &completed, 10*time.Millisecond)

	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
	close(done)

	output := buf.String()
	if !strings.Contains(output, "heartbeat") {
		t.Fatalf("expected heartbeat in log, got: %s", output)
	}
}

func TestEmitHeartbeat_DoneChannel(t *testing.T) {
	ctx := context.Background()
	done := make(chan struct{})
	var mu sync.Mutex
	var completed int32

	go emitHeartbeat(ctx, done, &mu, make(map[string]runningProvider), 1, &completed, 1*time.Hour)

	// Close done immediately — goroutine should exit without firing
	close(done)
	time.Sleep(10 * time.Millisecond)
}
