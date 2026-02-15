package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ademajagon/gopay-service/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Save(p *domain.Payment) error {
	ctx := context.Background()

	return r.withTx(ctx, func(tx pgx.Tx) error {
		if err := r.upsertPayment(ctx, tx, p); err != nil {
			return err
		}

		if err := r.writeOutboxEvents(ctx, tx, p); err != nil {
			return err
		}
		return nil
	})
}

func (r *Repository) upsertPayment(ctx context.Context, tx pgx.Tx, p *domain.Payment) error {
	const q = `
		INSERT INTO payments (
			id, order_id, customer_id,
			amount_cents, currency,
			status, provider_ref, failure_reason,
			idempotency_key,
			created_at, updated_at,
			version
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)
		ON CONFLICT (id) DO UPDATE SET
			status         = EXCLUDED.status,
			provider_ref   = EXCLUDED.provider_ref,
			failure_reason = EXCLUDED.failure_reason,
			updated_at     = EXCLUDED.updated_at,
			version        = EXCLUDED.version
		WHERE
			-- This is the optimistic locking check.
			-- EXCLUDED.version is what we're trying to write.
			-- payments.version is what's currently in the DB.
			-- They should differ by exactly 1: if another writer already
			-- incremented it, this WHERE clause fails → 0 rows affected.
			payments.version = EXCLUDED.version - 1
	`

	tag, err := tx.Exec(ctx, q,
		p.ID().String(),
		p.OrderID(),
		p.CustomerID(),
		p.Amount().Amount(),
		p.Amount().Currency(),
		string(p.Status()),
		p.ProviderRef(),
		p.FailureReason(),
		p.IdempotencyKey(),
		p.CreatedAt(),
		p.UpdatedAt(),
		p.Version(),
	)

	if err != nil {
		return fmt.Errorf("upsert payment: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return domain.ErrVersionConflict
	}

	return nil
}

func (r *Repository) writeOutboxEvents(ctx context.Context, tx pgx.Tx, p *domain.Payment) error {
	events := p.PopEvents()
	if len(events) == 0 {
		return nil
	}

	const q = `
		INSERT INTO outbox_events (aggregate_id, event_type, payload, created_at)
		VALUES ($1, $2, $3, NOW())
	`

	for _, evt := range events {
		payload, err := json.Marshal(evt)
		if err != nil {
			return fmt.Errorf("marshal event %s: %w", domain.EventType(evt), err)
		}
		if _, err := tx.Exec(ctx, q, p.ID().String(), domain.EventType(evt), payload); err != nil {
			return fmt.Errorf("insert outbox event %s: %w", domain.EventType(evt), err)
		}
	}
	return nil
}

func (r *Repository) Write(ctx context.Context, aggregateID, eventType string, payload []byte) error {
	const q = `
		INSERT INTO outbox_events (aggregate_id, event_type, payload, created_at)
		VALUES ($1, $2, $3, NOW())
	`

	if _, err := r.pool.Exec(ctx, q, aggregateID, eventType, payload); err != nil {
		return fmt.Errorf("outbox write: %w", err)
	}
	return nil
}

func (r *Repository) FindByIdempotencyKey(key string) (*domain.Payment, error) {
	ctx := context.Background()

	const q = `
		SELECT id, order_id, customer_id, amount_cents, currency,
		       status, provider_ref, failure_reason,
		       idempotency_key, created_at, updated_at, version
		FROM payments
		WHERE idempotency_key = $1
	`

	row := r.pool.QueryRow(ctx, q, key)
	p, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return p, nil
}

func scanPayment(row pgx.Row) (*domain.Payment, error) {
	var (
		rawID          string
		orderID        string
		customerID     string
		amountCents    int64
		currency       string
		status         string
		providerRef    string
		failureReason  string
		idempotencyKey string
		createdAt      time.Time
		updatedAt      time.Time
		version        int
	)

	err := row.Scan(
		&rawID, &orderID, &customerID, &amountCents, &currency,
		&status, &providerRef, &failureReason,
		&idempotencyKey, &createdAt, &updatedAt, &version,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("scan payment row: %w", err)
	}

	id, err := domain.ParsePaymentID(rawID)
	if err != nil {
		return nil, fmt.Errorf("parse stored payment ID %w", err)
	}
	amount, err := domain.NewMoney(amountCents, currency)
	if err != nil {
		return nil, fmt.Errorf("parse stored money %w", err)
	}

	return domain.Reconstitute(
		id, orderID, customerID, amount,
		domain.PaymentStatus(status),
		providerRef, failureReason, idempotencyKey,
		createdAt, updatedAt, version,
	), nil
}

func (r *Repository) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type PoolConfig struct {
	DSN               string
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse database DSN: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.HealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping connection pool: %w", err)
	}

	return pool, nil
}
