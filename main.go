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

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
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

	level := slog.LevelInfo
	if cfg.Debug {
		level = slog.LevelDebug
	}
	runID := fmt.Sprintf("%d", time.Now().UnixMilli())
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	logger = logger.With("run_id", runID)
	slog.SetDefault(logger)

	baseCtx := context.Background()
	ctx, cancel := context.WithTimeout(baseCtx, syncTimeout)
	defer cancel()
	slog.Info("sync timeout configured", "timeout", syncTimeout.String())

	exitCode := 0
	stopSignals := make(chan os.Signal, 1)
	signal.Notify(stopSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stopSignals)

	go func() {
		sig := <-stopSignals
		slog.Warn("shutdown requested", "signal", sig.String())
		exitCode = 130
		cancel()
	}()

	if err := db.RunMigrations(cfg.DatabaseURL); err != nil {
		slog.Error("migrations failed", "error", err)
		return 1
	}
	slog.Info("migrations applied")

	pool, err := db.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		slog.Error("pool init failed", "error", err)
		return 1
	}
	defer pool.Close()

	if err := db.Seed(ctx, pool); err != nil {
		slog.Error("seed failed", "error", err)
		return 1
	}
	slog.Info("seed complete")

	slog.Info("sync job started")
	if err := scheduler.BackfillAll(ctx, pool, scheduler.Options{
		Concurrency:       cfg.BackfillConcurrency,
		Debug:             cfg.Debug,
		HeartbeatInterval: heartbeatInterval,
		DailySync:         cfg.IsDailySync(),
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
