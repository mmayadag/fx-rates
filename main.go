package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mmayadag/fx-rates/internal/config"
	"github.com/mmayadag/fx-rates/internal/db"
	"github.com/mmayadag/fx-rates/internal/scheduler"
)

// dsnCredentialsRe matches the "user:password@" portion of a postgres/pgx DSN
// so it can be masked before an error string reaches the logs.
var dsnCredentialsRe = regexp.MustCompile(`(?i)(postgres(?:ql)?|pgx5?)://[^\s:/@]+:[^\s@/]+@`)

// redactDSN masks credentials in any DSN embedded in s. Connection errors from
// pgx / golang-migrate can carry the full DSN, including the password.
func redactDSN(s string) string {
	return dsnCredentialsRe.ReplaceAllString(s, "$1://***:***@")
}

const setupTimeout = 2 * time.Minute

// version is injected at build time via -ldflags="-X main.version=...".
// Defaults to "dev" for unstamped local builds.
var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	os.Exit(run())
}

func run() int {
	cfg, err := config.Bootstrap("")
	if err != nil {
		slog.Error("config load failed", "error", err)
		return 1
	}
	syncTimeout, err := cfg.DailySyncTimeoutDuration()
	if err != nil {
		slog.Error("invalid sync timeout", "error", err)
		return 1
	}
	heartbeatInterval, err := cfg.DebugHeartbeatDuration()
	if err != nil {
		slog.Error("invalid heartbeat interval", "error", err)
		return 1
	}

	runID := fmt.Sprintf("%d", time.Now().UnixMilli())
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	logger = logger.With("run_id", runID)
	slog.SetDefault(logger)

	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()

	slog.Info("starting fx-rates", "version", version, "sync_mode", cfg.SyncMode)
	slog.Info("timeouts configured", "setup", setupTimeout.String(), "sync", syncTimeout.String())

	var signalled atomic.Bool
	stopSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopSignals)

	go func() {
		sig := <-stopSignals
		slog.Warn("shutdown requested", "signal", sig.String())
		signalled.Store(true)
		baseCancel()
	}()

	setupCtx, setupCancel := context.WithTimeout(baseCtx, setupTimeout)
	defer setupCancel()

	if cfg.RunMigrations {
		if err := db.RunMigrations(cfg.DatabaseURL); err != nil {
			slog.Error("migrations failed", "error", redactDSN(err.Error()))
			return 1
		}
		slog.Info("migrations applied")
	} else {
		slog.Info("migrations skipped", "reason", "RUN_MIGRATIONS=false")
	}

	pool, err := db.NewPool(setupCtx, cfg.DatabaseURL, db.PoolOptions{
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   cfg.DBMaxConnLifetime,
		MaxConnIdleTime:   cfg.DBMaxConnIdleTime,
		HealthCheckPeriod: cfg.DBHealthCheckPeriod,
	})
	if err != nil {
		slog.Error("pool init failed", "error", redactDSN(err.Error()))
		return 1
	}
	defer func() {
		stat := pool.Stat()
		slog.Info("pool closing",
			"acquired_conns", stat.AcquiredConns(),
			"idle_conns", stat.IdleConns(),
			"total_conns", stat.TotalConns(),
			"max_conns", stat.MaxConns(),
		)
		pool.Close()
	}()

	if err := db.Seed(setupCtx, pool); err != nil {
		slog.Error("seed failed", "error", err)
		return 1
	}
	slog.Info("seed complete")
	setupCancel()

	syncCtx, syncCancel := context.WithTimeout(baseCtx, syncTimeout)
	defer syncCancel()

	slog.Info("sync job started")
	lookback := 0
	if cfg.IsDailySync() {
		lookback = cfg.DailySyncLookback
	}
	var syncErr error
	if err := scheduler.BackfillAll(syncCtx, pool, scheduler.Options{
		Debug:             cfg.Debug,
		HeartbeatInterval: heartbeatInterval,
		DailySync:         cfg.IsDailySync(),
		DailySyncLookback: lookback,
	}); err != nil {
		slog.Error("sync job failed", "error", err)
		syncErr = err
	} else {
		slog.Info("sync job completed")
	}

	return resolveExitCode(signalled.Load(), syncErr)
}

// resolveExitCode maps the run outcome to a process exit code. A received
// shutdown signal takes precedence (130), then any run error (1), else 0.
func resolveExitCode(signalled bool, runErr error) int {
	switch {
	case signalled:
		return 130
	case runErr != nil:
		return 1
	default:
		return 0
	}
}
