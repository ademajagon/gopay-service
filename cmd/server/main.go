package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ademajagon/gopay-service/internal/adapters/httpserver"
	pgadapter "github.com/ademajagon/gopay-service/internal/adapters/postgres"
	redisadapter "github.com/ademajagon/gopay-service/internal/adapters/redis"
	"github.com/ademajagon/gopay-service/internal/app"
	"github.com/ademajagon/gopay-service/internal/config"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
)

var (
	version   = "dev"
	commitSHA = "unknown"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "startup error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := newLogger(cfg.IsProd())
	logger.Info("gopay service starting",
		"version", version,
		"commit", commitSHA,
		"build_time", buildTime,
		"env", cfg.Env,
	)

	// NewPool() calls pool.Ping() before returning, if the DB is unreachable,
	ctx := context.Background()
	pool, err := pgadapter.NewPool(ctx, pgadapter.PoolConfig{
		DSN:               cfg.Database.DSN,
		MaxConns:          cfg.Database.MaxConns,
		MinConns:          cfg.Database.MinConns,
		MaxConnLifetime:   cfg.Database.MaxConnLifeTime,
		MaxConnIdleTime:   cfg.Database.MaxConnIdleTime,
		HealthCheckPeriod: cfg.Database.HealthPeriod,
	})

	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()
	slog.Info("postgres connected", "max_conns", cfg.Database.MaxConns)

	if err := runMigrations(cfg.Database.DSN, cfg.Database.MigrationsPath, logger); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	redisClient := redisadapter.NewClient(redisadapter.Config{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})
	defer redisClient.Close()

	if err := redisadapter.Ping(ctx, redisClient); err != nil {
		return fmt.Errorf("connect to redis: %w", err)
	}
	slog.Info("redis connected", "addr", cfg.Redis.Addr)

	repo := pgadapter.NewRepository(pool)
	idempotencyStore := redisadapter.NewIdempotencyStore(redisClient, cfg.Redis.Namespace, logger)

	// app service wire
	svc := app.NewPaymentService(
		repo,
		idempotencyStore,
		repo,
		logger,
	)

	// http handler and server
	handler := httpserver.NewHandler(svc, logger)

	checks := []httpserver.ReadinessCheck{
		func(ctx context.Context) error { return pool.Ping(ctx) },
		func(ctx context.Context) error { return redisadapter.Ping(ctx, redisClient) },
	}

	server := httpserver.NewServer(
		httpserver.ServerConfig{
			Addr:            cfg.HTTP.Addr,
			ReadTimeout:     cfg.HTTP.ReadTimeout,
			WriteTimeout:    cfg.HTTP.WriteTimeout,
			IdleTimeout:     cfg.HTTP.IdleTimeout,
			ShutdownTimeout: cfg.HTTP.ShutdownTimeout,
		},
		handler,
		checks,
		logger,
	)

	errCh := make(chan error, 1)
	go func() {
		if err := server.Start(); err != nil {
			errCh <- err
		}
	}()

	logger.Info("gopay service ready",
		"addr", cfg.HTTP.Addr,
		"metrics", cfg.HTTP.Addr+"/metrics",
		"health", cfg.HTTP.Addr+"/healthz/ready")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		logger.Info("shutdown signal received", "signal", sig.String())
	case err := <-errCh:
		logger.Error("fatal server error", "err", err)
		return err
	}

	if err := server.Shutdown(context.Background()); err != nil {
		logger.Error("graceful shutdown error", "err", err)
		return err
	}

	logger.Info("gopay service stopped")
	return nil
}

func newLogger(prod bool) *slog.Logger {
	opts := &slog.HandlerOptions{
		AddSource: prod,
	}

	var handler slog.Handler
	if prod {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		opts.Level = slog.LevelDebug
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func runMigrations(dsn, migrationsPath string, log *slog.Logger) error {
	log.Info("running database migrations", "path", migrationsPath, "dsn", dsn)

	m, err := migrate.New(migrationsPath, dsn)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		log.Info("migrate closed", "source_err", srcErr, "db_err", dbErr)
	}()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}

	return nil
}
