package config

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Env string `envconfig:"ENV" default:"development"`

	Database DatabaseConfig
}

type DatabaseConfig struct {
	// postgreSQL connection string.
	DSN string `envconfig:"DATABASE_DSN" required:"true"`

	// where golang-migrate looks for SQL files.
	MigrationsPath string `envconfig:"DATABASE_MIGRATIONS_PATH" default:"file://migrations"`

	MaxConns int32 `envconfig:"DATABASE_MAX_CONNS" default:"20"`

	MinConns int32 `envconfig:"DATABASE_MIN_CONNS" default:"5"`

	MaxConnLifeTime time.Duration `envconfig:"DATABASE_MAX_CONN_LIFETIME" default:"1h"`
	MaxConnIdleTime time.Duration `envconfig:"DATABASE_MAX_CONN_IDLE" default:"30m"`
	HealthPeriod    time.Duration `envconfig:"DATABASE_HEALTH_PERIOD" default:"1m"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, fmt.Errorf("parse environment config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) IsProd() bool {
	return c.Env == "production"
}
