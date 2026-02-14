package config

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Env string `envconfig:"ENV" default:"development"`
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
