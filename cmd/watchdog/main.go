package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/German4341374/service-reliability-watchdog/internal/checker"
	"github.com/German4341374/service-reliability-watchdog/internal/config"
	"github.com/German4341374/service-reliability-watchdog/internal/monitor"
	"github.com/German4341374/service-reliability-watchdog/internal/store"
	"github.com/German4341374/service-reliability-watchdog/internal/telemetry"
	"github.com/German4341374/service-reliability-watchdog/internal/web"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML configuration")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(*configPath, logger); err != nil {
		logger.Error("watchdog stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath string, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	connectCtx, cancel := context.WithTimeout(rootCtx, cfg.Database.ConnectTimeout.Duration)
	repository, err := openDatabaseWithRetry(connectCtx, cfg, logger)
	cancel()
	if err != nil {
		return err
	}
	defer repository.Close()
	migrationCtx, cancelMigration := context.WithTimeout(rootCtx, 30*time.Second)
	err = repository.Migrate(migrationCtx)
	cancelMigration()
	if err != nil {
		return fmt.Errorf("apply database migrations: %w", err)
	}

	metrics := telemetry.New()
	metrics.DatabaseReady(true)
	service := monitor.New(
		cfg.DomainEndpoints(), repository, checker.New(), metrics, logger,
		monitor.OptionsFromConfig(cfg),
	)
	webServer, err := web.New(service, repository, metrics.Handler(), logger)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr: cfg.Server.Address, Handler: webServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- service.Run(rootCtx) }()
	go func() {
		logger.Info("HTTP server started", "address", cfg.Server.Address, "version", version)
		errCh <- httpServer.ListenAndServe()
	}()
	go databaseReadiness(rootCtx, repository, metrics)

	select {
	case <-rootCtx.Done():
	case runErr := <-errCh:
		if runErr != nil && !errors.Is(runErr, http.ErrServerClosed) {
			err = runErr
		}
	}
	stop()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout.Duration)
	defer shutdownCancel()
	if shutdownErr := httpServer.Shutdown(shutdownCtx); shutdownErr != nil && err == nil {
		err = shutdownErr
	}
	logger.Info("graceful shutdown complete")
	return err
}

func openDatabaseWithRetry(ctx context.Context, cfg config.Config, logger *slog.Logger) (*store.Postgres, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		repository, err := store.OpenPostgres(ctx, cfg.Database.URL, cfg.Database.MaxConnections)
		if err == nil {
			return repository, nil
		}
		logger.Warn("waiting for PostgreSQL", "error", err)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("PostgreSQL startup deadline: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func databaseReadiness(ctx context.Context, repository store.Repository, metrics *telemetry.Metrics) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			err := repository.Ping(pingCtx)
			cancel()
			metrics.DatabaseReady(err == nil)
		}
	}
}
