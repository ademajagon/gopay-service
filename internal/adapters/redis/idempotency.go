package redis

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

type IdempotencyStore struct {
	client    redis.UniversalClient
	namespace string
	log       *slog.Logger
}

func (i IdempotencyStore) Get(ctx context.Context, key string) (string, bool, error) {
	//TODO implement me
	panic("implement me")
}

func (i IdempotencyStore) Set(ctx context.Context, key string, result string, ttl time.Duration) error {
	//TODO implement me
	panic("implement me")
}

func NewIdempotencyStore(client redis.UniversalClient, namespace string, log *slog.Logger) *IdempotencyStore {
	return &IdempotencyStore{
		client:    client,
		namespace: namespace,
		log:       log,
	}
}

type Config struct {
	Addr     string
	Password string
	// Redis logical database number
	DB int
}

func NewClient(cfg Config) redis.UniversalClient {
	return redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     20,
		MinIdleConns: 5,
		MaxRetries:   3,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  500 * time.Millisecond,
		WriteTimeout: 500 * time.Millisecond,
	})
}

func Ping(ctx context.Context, client redis.UniversalClient) error {
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	return nil
}
