package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const (
	defaultFullSyncTimeout  = 6 * time.Hour
	defaultDailySyncTimeout = 30 * time.Minute
)

type Config struct {
	DatabaseURL         string        `envconfig:"DATABASE_URL"`
	DBUser              string        `envconfig:"DB_USER"`
	DBPassword          string        `envconfig:"DB_PASSWORD"`
	DBName              string        `envconfig:"DB_NAME"`
	DBHost              string        `envconfig:"DB_HOST"`
	DBPort              string        `envconfig:"DB_PORT"`
	DBSSLMode           string        `envconfig:"DB_SSLMODE" default:"disable"`
	DBMaxConns          int32         `envconfig:"DB_MAX_CONNECTIONS" default:"10"`
	DBMinConns          int32         `envconfig:"DB_MIN_CONNECTIONS" default:"5"`
	DBMaxConnLifetime   time.Duration `envconfig:"DB_MAX_CONN_LIFETIME" default:"30m"`
	DBMaxConnIdleTime   time.Duration `envconfig:"DB_MAX_CONN_IDLE_TIME" default:"5m"`
	DBHealthCheckPeriod time.Duration `envconfig:"DB_HEALTH_CHECK_PERIOD" default:"30s"`
	BackfillConcurrency int           `envconfig:"BACKFILL_CONCURRENCY" default:"10"`
	Debug               bool          `envconfig:"DEBUG" default:"true"`
	SyncMode            string        `envconfig:"SYNC_MODE" default:"daily_sync"`
	DailySyncTimeout    string        `envconfig:"DAILY_SYNC_TIMEOUT"`
	DebugHeartbeat      string        `envconfig:"DEBUG_HEARTBEAT_INTERVAL" default:"20s"`
	RunMigrations       bool          `envconfig:"RUN_MIGRATIONS" default:"true"`
	LogLevel            string        `envconfig:"LOG_LEVEL"`
	DailySyncLookback   int           `envconfig:"DAILY_SYNC_LOOKBACK_DAYS" default:"7"`
}

func Load() (Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return cfg, err
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		databaseURL, err := cfg.buildDatabaseURL()
		if err != nil {
			return cfg, err
		}
		cfg.DatabaseURL = databaseURL
	}
	cfg.SyncMode = strings.ToLower(strings.TrimSpace(cfg.SyncMode))
	switch cfg.SyncMode {
	case "", "full", "daily_sync":
	default:
		return cfg, fmt.Errorf("SYNC_MODE must be one of: full, daily_sync")
	}
	cfg.LogLevel = strings.ToLower(strings.TrimSpace(cfg.LogLevel))
	switch cfg.LogLevel {
	case "", "debug", "info", "warn", "warning", "error":
	default:
		return cfg, fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error")
	}
	if strings.Contains(cfg.DatabaseURL, "sslmode=disable") {
		fmt.Fprintln(os.Stderr, "warning: database SSL is disabled — not recommended for production")
	}
	return cfg, nil
}

// LogValue implements slog.LogValuer so that Config can be safely logged
// without leaking DATABASE_URL or API keys.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("max_conns", int(c.DBMaxConns)),
		slog.Int("concurrency", c.BackfillConcurrency),
		slog.Bool("debug", c.Debug),
		slog.Bool("run_migrations", c.RunMigrations),
	)
}

func (c Config) DailySyncTimeoutDuration() (time.Duration, error) {
	if strings.TrimSpace(c.DailySyncTimeout) == "" {
		if c.IsDailySync() {
			return defaultDailySyncTimeout, nil
		}
		return defaultFullSyncTimeout, nil
	}
	return time.ParseDuration(c.DailySyncTimeout)
}

func (c Config) DebugHeartbeatDuration() (time.Duration, error) {
	if strings.TrimSpace(c.DebugHeartbeat) == "" {
		return 20 * time.Second, nil
	}
	return time.ParseDuration(c.DebugHeartbeat)
}

func (c Config) IsDailySync() bool {
	return c.SyncMode == "daily_sync"
}

// SlogLevel returns the configured logging level.
// Falls back to Debug-derived level (debug if Debug, else info) when LOG_LEVEL is unset.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	if c.Debug {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func (c Config) buildDatabaseURL() (string, error) {
	required := map[string]string{
		"DB_USER":     c.DBUser,
		"DB_PASSWORD": c.DBPassword,
		"DB_NAME":     c.DBName,
		"DB_HOST":     c.DBHost,
		"DB_PORT":     c.DBPort,
	}

	missing := make([]string, 0)
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("DATABASE_URL is required, or set DB_USER, DB_PASSWORD, DB_NAME, DB_HOST, and DB_PORT")
	}

	sslMode := strings.TrimSpace(c.DBSSLMode)
	if sslMode == "" {
		sslMode = "disable"
	}

	return (&url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.DBUser, c.DBPassword),
		Host:   fmt.Sprintf("%s:%s", c.DBHost, c.DBPort),
		Path:   c.DBName,
		RawQuery: url.Values{
			"sslmode": []string{sslMode},
		}.Encode(),
	}).String(), nil
}
