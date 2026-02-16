package redis

import (
	"context"
	"errors"
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

func NewIdempotencyStore(client redis.UniversalClient, namespace string, log *slog.Logger) *IdempotencyStore {
	return &IdempotencyStore{
		client:    client,
		namespace: namespace,
		log:       log,
	}
}

func (s *IdempotencyStore) key(k string) string {
	return fmt.Sprintf("%s:idempotency:%s", s.namespace, k)
}

func (s *IdempotencyStore) Get(ctx context.Context, key string) (string, bool, error) {
	val, err := s.client.Get(ctx, s.key(key)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("redis GET idempotency key: %w", err)
	}
	return val, true, nil
}

func (s *IdempotencyStore) Set(ctx context.Context, key string, result string, ttl time.Duration) error {
	ok, err := s.client.SetNX(ctx, s.key(key), result, ttl).Result()
	if err != nil {
		return fmt.Errorf("redis SETNX idempotency key: %w", err)
	}
	if !ok {
		s.log.DebugContext(ctx, "idempotency key already cached", "key", key)
	}
	return nil
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
