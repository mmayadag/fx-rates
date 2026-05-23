package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_NAME", "")
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_PORT", "")
	t.Setenv("SYNC_MODE", "")
	t.Setenv("DEBUG", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required, or set DB_USER, DB_PASSWORD, DB_NAME, DB_HOST, and DB_PORT") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadNormalizesSyncMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	t.Setenv("SYNC_MODE", " Daily_Sync ")
	t.Setenv("DEBUG", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.SyncMode != "daily_sync" {
		t.Fatalf("SyncMode = %q, want daily_sync", cfg.SyncMode)
	}
}

func TestSlogLevel(t *testing.T) {
	tests := []struct {
		name     string
		logLevel string
		debug    bool
		want     slog.Level
	}{
		{"empty + debug=true → debug", "", true, slog.LevelDebug},
		{"empty + debug=false → info", "", false, slog.LevelInfo},
		{"debug overrides debug=false", "debug", false, slog.LevelDebug},
		{"info overrides debug=true", "info", true, slog.LevelInfo},
		{"warn", "warn", false, slog.LevelWarn},
		{"warning alias", "warning", false, slog.LevelWarn},
		{"error", "error", false, slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{LogLevel: tt.logLevel, Debug: tt.debug}
			if got := cfg.SlogLevel(); got != tt.want {
				t.Fatalf("SlogLevel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	t.Setenv("LOG_LEVEL", "verbose")
	t.Setenv("DEBUG", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL must be one of") {
		t.Fatalf("expected LOG_LEVEL error, got %v", err)
	}
}

func TestRunMigrationsDefaultsToTrue(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	t.Setenv("DEBUG", "false")
	old, hadOld := os.LookupEnv("RUN_MIGRATIONS")
	os.Unsetenv("RUN_MIGRATIONS")
	t.Cleanup(func() {
		if hadOld {
			os.Setenv("RUN_MIGRATIONS", old)
		} else {
			os.Unsetenv("RUN_MIGRATIONS")
		}
	})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.RunMigrations {
		t.Fatalf("RunMigrations = false, want true (default)")
	}
}

func TestRunMigrationsCanBeDisabled(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	t.Setenv("RUN_MIGRATIONS", "false")
	t.Setenv("DEBUG", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.RunMigrations {
		t.Fatalf("RunMigrations = true, want false")
	}
}

func TestLoadRejectsInvalidSyncMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	t.Setenv("SYNC_MODE", "weekly")
	t.Setenv("DEBUG", "false")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SYNC_MODE must be one of") {
		t.Fatalf("expected sync mode error, got %v", err)
	}
}

func TestLogValueOmitsSensitiveFields(t *testing.T) {
	cfg := Config{
		DatabaseURL:         "postgres://secret",
		DBPassword:          "topsecret",
		DBMaxConns:          12,
		BackfillConcurrency: 4,
		Debug:               true,
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("config", "cfg", cfg)

	out := buf.String()
	for _, want := range []string{`"max_conns":12`, `"concurrency":4`, `"debug":true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in log output %s", want, out)
		}
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "topsecret") || strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("sensitive data leaked in log output: %s", out)
	}
}

func TestLoadBuildsDatabaseURLFromSplitEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_USER", "alice")
	t.Setenv("DB_PASSWORD", "s3cr3t")
	t.Setenv("DB_NAME", "fx_rates")
	t.Setenv("DB_HOST", "db.example.com")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_SSLMODE", "require")
	t.Setenv("DEBUG", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	want := "postgres://alice:s3cr3t@db.example.com:5432/fx_rates?sslmode=require"
	if cfg.DatabaseURL != want {
		t.Fatalf("DatabaseURL = %q, want %q", cfg.DatabaseURL, want)
	}
}

func TestLoadBuildsDatabaseURLWithDefaultSSLMode(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_USER", "alice")
	t.Setenv("DB_PASSWORD", "s3cr3t")
	t.Setenv("DB_NAME", "fx_rates")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_SSLMODE", "")
	t.Setenv("DEBUG", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !strings.Contains(cfg.DatabaseURL, "sslmode=disable") {
		t.Fatalf("expected default sslmode=disable in %q", cfg.DatabaseURL)
	}
}

func TestDailySyncTimeoutDuration(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want time.Duration
	}{
		{
			name: "daily default",
			cfg:  Config{SyncMode: "daily_sync"},
			want: defaultDailySyncTimeout,
		},
		{
			name: "full default",
			cfg:  Config{SyncMode: "full"},
			want: defaultFullSyncTimeout,
		},
		{
			name: "custom",
			cfg:  Config{DailySyncTimeout: "45m"},
			want: 45 * time.Minute,
		},
	}

	for _, tt := range tests {
		got, err := tt.cfg.DailySyncTimeoutDuration()
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.name, err)
		}
		if got != tt.want {
			t.Fatalf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestDailySyncTimeoutDurationRejectsInvalidValue(t *testing.T) {
	_, err := (Config{DailySyncTimeout: "later"}).DailySyncTimeoutDuration()
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestDebugHeartbeatDuration(t *testing.T) {
	got, err := (Config{}).DebugHeartbeatDuration()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 20*time.Second {
		t.Fatalf("got %v, want 20s", got)
	}

	got, err = (Config{DebugHeartbeat: "2m"}).DebugHeartbeatDuration()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 2*time.Minute {
		t.Fatalf("got %v, want 2m", got)
	}
}

func TestIsDailySync(t *testing.T) {
	if !(Config{SyncMode: "daily_sync"}).IsDailySync() {
		t.Fatal("expected daily sync mode")
	}
	if (Config{SyncMode: "full"}).IsDailySync() {
		t.Fatal("did not expect full mode to be daily sync")
	}
}

func TestLoadAppliesRuntimeDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_USER", "alice")
	t.Setenv("DB_PASSWORD", "s3cr3t")
	t.Setenv("DB_NAME", "fx_rates")
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_PORT", "5432")
	t.Setenv("DB_SSLMODE", "disable")
	unsetEnvForTest(t, "DB_MAX_CONNECTIONS")
	unsetEnvForTest(t, "DB_MIN_CONNECTIONS")
	unsetEnvForTest(t, "DB_MAX_CONN_LIFETIME")
	unsetEnvForTest(t, "DB_MAX_CONN_IDLE_TIME")
	unsetEnvForTest(t, "DB_HEALTH_CHECK_PERIOD")
	unsetEnvForTest(t, "BACKFILL_CONCURRENCY")
	unsetEnvForTest(t, "DEBUG")
	unsetEnvForTest(t, "SYNC_MODE")
	unsetEnvForTest(t, "DAILY_SYNC_TIMEOUT")
	unsetEnvForTest(t, "DEBUG_HEARTBEAT_INTERVAL")
	unsetEnvForTest(t, "DAILY_SYNC_LOOKBACK_DAYS")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.DBMaxConns != 10 {
		t.Fatalf("DBMaxConns = %d, want 10", cfg.DBMaxConns)
	}
	if cfg.DBMinConns != 5 {
		t.Fatalf("DBMinConns = %d, want 5", cfg.DBMinConns)
	}
	if cfg.DBMaxConnLifetime != 30*time.Minute {
		t.Fatalf("DBMaxConnLifetime = %v, want 30m", cfg.DBMaxConnLifetime)
	}
	if cfg.DBMaxConnIdleTime != 5*time.Minute {
		t.Fatalf("DBMaxConnIdleTime = %v, want 5m", cfg.DBMaxConnIdleTime)
	}
	if cfg.DBHealthCheckPeriod != 30*time.Second {
		t.Fatalf("DBHealthCheckPeriod = %v, want 30s", cfg.DBHealthCheckPeriod)
	}
	if cfg.DailySyncLookback != 7 {
		t.Fatalf("DailySyncLookback = %d, want 7", cfg.DailySyncLookback)
	}
	if cfg.BackfillConcurrency != 10 {
		t.Fatalf("BackfillConcurrency = %d, want 10", cfg.BackfillConcurrency)
	}
	if !cfg.Debug {
		t.Fatal("expected Debug default to be true")
	}
	if cfg.SyncMode != "daily_sync" {
		t.Fatalf("SyncMode = %q, want daily_sync", cfg.SyncMode)
	}
	if cfg.DebugHeartbeat != "20s" {
		t.Fatalf("DebugHeartbeat = %q, want 20s", cfg.DebugHeartbeat)
	}
	timeout, err := cfg.DailySyncTimeoutDuration()
	if err != nil {
		t.Fatalf("DailySyncTimeoutDuration returned error: %v", err)
	}
	if timeout != 30*time.Minute {
		t.Fatalf("DailySyncTimeoutDuration = %v, want 30m", timeout)
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := strings.Join([]string{
		"# comment",
		"",
		"FOO=bar",
		`QUOTED=" hello "`,
		"EXISTING=from_file",
		"INVALID_LINE",
		" =ignored",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv("EXISTING", "from_env")
	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}

	if got := os.Getenv("FOO"); got != "bar" {
		t.Fatalf("FOO = %q, want bar", got)
	}
	if got := os.Getenv("QUOTED"); got != " hello " {
		t.Fatalf("QUOTED = %q, want \" hello \"", got)
	}
	if got := os.Getenv("EXISTING"); got != "from_env" {
		t.Fatalf("EXISTING = %q, want from_env", got)
	}
}

func TestLoadDotEnvMissingFileIsIgnored(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Fatalf("expected nil for missing file, got %v", err)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()

	value, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%q): %v", key, err)
	}
	t.Cleanup(func() {
		if !existed {
			_ = os.Unsetenv(key)
			return
		}
		_ = os.Setenv(key, value)
	})
}
