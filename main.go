package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mmayadag/fx-rates/internal/config"
	"github.com/mmayadag/fx-rates/internal/db"
	"github.com/mmayadag/fx-rates/internal/scheduler"
)

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

	exitCode := 0
	stopSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopSignals)

	go func() {
		sig := <-stopSignals
		slog.Warn("shutdown requested", "signal", sig.String())
		exitCode = 130
		baseCancel()
	}()

	setupCtx, setupCancel := context.WithTimeout(baseCtx, setupTimeout)
	defer setupCancel()

	if cfg.RunMigrations {
		if err := db.RunMigrations(cfg.DatabaseURL); err != nil {
			slog.Error("migrations failed", "error", err)
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
		slog.Error("pool init failed", "error", err)
		return 1
	}
	defer pool.Close()

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
	if err := scheduler.BackfillAll(syncCtx, pool, scheduler.Options{
		Concurrency:       cfg.BackfillConcurrency,
		Debug:             cfg.Debug,
		HeartbeatInterval: heartbeatInterval,
		DailySync:         cfg.IsDailySync(),
		DailySyncLookback: lookback,
	}); err != nil {
		slog.Error("sync job failed", "error", err)
		if exitCode == 0 {
			exitCode = 1
		}
	} else {
		slog.Info("sync job completed")
	}

	return exitCode
}
