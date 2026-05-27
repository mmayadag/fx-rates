package scheduler

import (
	"bytes"
	"context"
	"errors"
	"io"
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

	got, note := adjustStartForMode(start, today, true, 7)
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

	got, note := adjustStartForMode(start, today, true, 7)
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

	got, note := adjustStartForMode(time.Time{}, today, true, 7)
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

	got, note := adjustStartForMode(start, today, true, 0)
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
		got := getStartDate(coverageStart, lastSynced)
		if !got.Equal(*lastSynced) {
			t.Fatalf("got %v, want %v", got, *lastSynced)
		}
	})

	t.Run("falls back to coverageStart when no lastSynced", func(t *testing.T) {
		got := getStartDate(coverageStart, nil)
		if !got.Equal(*coverageStart) {
			t.Fatalf("got %v, want %v", got, *coverageStart)
		}
	})

	t.Run("returns nil when both nil", func(t *testing.T) {
		if got := getStartDate(nil, nil); got != nil {
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

func TestIsUpToDate(t *testing.T) {
	today := time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC)
	yesterday := today.AddDate(0, 0, -1)
	tomorrow := today.AddDate(0, 0, 1)

	tests := []struct {
		name  string
		after *time.Time
		want  bool
	}{
		{"nil after → false", nil, false},
		{"after = today → true", &today, true},
		{"after = tomorrow → true", &tomorrow, true},
		{"after = yesterday → false", &yesterday, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUpToDate(tt.after, today); got != tt.want {
				t.Fatalf("isUpToDate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStartFrom(t *testing.T) {
	if got := startFrom(nil); !got.IsZero() {
		t.Fatalf("startFrom(nil) = %v, want zero", got)
	}
	d := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	if got := startFrom(&d); !got.Equal(d) {
		t.Fatalf("startFrom(&d) = %v, want %v", got, d)
	}
}

func TestFilterValidRecords(t *testing.T) {
	d1 := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 6, 5, 0, 0, 0, 0, time.UTC)
	records := []provider.Record{
		{Date: d1, Base: "EUR", Quote: "USD", Rate: 1.1},
		{Date: d2, Base: "EUR", Quote: "XDR", Rate: 0.8}, // excluded
		{Date: d1, Base: "XDR", Quote: "GBP", Rate: 0.9}, // excluded
		{Date: d2, Base: "EUR", Quote: "GBP", Rate: -1},  // invalid
		{Date: d2, Base: "EUR", Quote: "JPY", Rate: 160}, // valid
	}
	valid, maxSeen := filterValidRecords(records, "ECB")

	if len(valid) != 2 {
		t.Fatalf("expected 2 valid records, got %d: %#v", len(valid), valid)
	}
	for _, v := range valid {
		if v.Provider != "ECB" {
			t.Errorf("expected Provider=ECB, got %q", v.Provider)
		}
	}
	if maxSeen == nil || !maxSeen.Equal(d2) {
		t.Fatalf("maxSeen = %v, want %v", maxSeen, d2)
	}
}

func TestFilterValidRecords_AllInvalidReturnsNil(t *testing.T) {
	d := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	records := []provider.Record{
		{Date: d, Base: "EUR", Quote: "XDR", Rate: 1},
		{Date: d, Base: "EUR", Quote: "USD", Rate: 0},
	}
	valid, maxSeen := filterValidRecords(records, "ECB")
	if len(valid) != 0 {
		t.Fatalf("expected 0 valid, got %d", len(valid))
	}
	if maxSeen != nil {
		t.Fatalf("expected nil maxSeen, got %v", maxSeen)
	}
}

func TestClassifyFetchError_Unavailable(t *testing.T) {
	res := classifyFetchError(
		&provider.Unavailable{Msg: "ECB returned no data"},
		BackfillResult{Provider: "ECB"},
		backfillProgress{fetched: 3, inserted: 2, skipped: 1},
		"ECB",
	)
	if res.Status != "unavailable" {
		t.Fatalf("Status = %q, want unavailable", res.Status)
	}
	if !res.Unavailable {
		t.Fatal("expected Unavailable=true")
	}
	if res.Error != "ECB returned no data" {
		t.Fatalf("Error = %q", res.Error)
	}
	if res.Fetched != 3 || res.Inserted != 2 || res.Skipped != 1 {
		t.Fatalf("progress not propagated: %+v", res)
	}
}

func TestClassifyFetchError_TransientAndPermanentBothErrorStatus(t *testing.T) {
	transient := classifyFetchError(io.EOF, BackfillResult{Provider: "ECB"}, backfillProgress{fetched: 10}, "ECB")
	if transient.Status != "error" {
		t.Fatalf("transient Status = %q, want error", transient.Status)
	}
	if transient.Fetched != 10 {
		t.Fatalf("Fetched = %d, want 10", transient.Fetched)
	}

	permanent := classifyFetchError(errors.New("malformed CSV"), BackfillResult{Provider: "ECB"}, backfillProgress{fetched: 5}, "ECB")
	if permanent.Status != "error" {
		t.Fatalf("permanent Status = %q, want error", permanent.Status)
	}
	if !strings.Contains(permanent.Error, "malformed CSV") {
		t.Fatalf("Error = %q, want 'malformed CSV'", permanent.Error)
	}
}

func TestFinalizeSuccess_Empty(t *testing.T) {
	res := finalizeSuccess(
		BackfillResult{Provider: "ECB"}, // LastSyncedBefore nil → matches empty branch
		backfillProgress{},
		time.Now(),
		"ECB",
	)
	if res.Status != "empty" {
		t.Fatalf("Status = %q, want empty", res.Status)
	}
}

func TestFinalizeSuccess_BumpsLastSyncedAfter(t *testing.T) {
	prev := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	res := finalizeSuccess(
		BackfillResult{Provider: "ECB", LastSyncedBefore: &prev, LastSyncedAfter: &prev},
		backfillProgress{fetched: 5, inserted: 5, maxSeenDate: &newer},
		time.Now(),
		"ECB",
	)
	if res.Status != "success" {
		t.Fatalf("Status = %q, want success", res.Status)
	}
	if res.LastSyncedAfter == nil || !res.LastSyncedAfter.Equal(newer) {
		t.Fatalf("LastSyncedAfter = %v, want %v", res.LastSyncedAfter, newer)
	}
	if res.Inserted != 5 {
		t.Fatalf("Inserted = %d, want 5", res.Inserted)
	}
}

func TestAdjustStartForMode_NotDailySync(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -30)
	got, note := adjustStartForMode(start, today, false, 7)
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

	finished := make(chan struct{})
	go func() {
		emitHeartbeat(ctx, done, &mu, running, 3, &completed, 10*time.Millisecond)
		close(finished)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-finished // wait for the goroutine to fully return before reading buf

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
