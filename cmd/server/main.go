package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	pgadapter "github.com/ademajagon/gopay-service/internal/adapters/postgres"
	redisadapter "github.com/ademajagon/gopay-service/internal/adapters/redis"
	"github.com/ademajagon/gopay-service/internal/config"
	"github.com/joho/godotenv"
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
	logger.Info("payment service starting",
		"env", cfg.Env)

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
	
	logger.Info("payment service stopped")
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
