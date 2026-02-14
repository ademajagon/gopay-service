package main

import (
	"fmt"
	"log/slog"
	"os"

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

	fmt.Printf("config: %+v\n", cfg)

	logger := newLogger(cfg.IsProd())
	logger.Info("payment service starting",
		"env", cfg.Env)

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
