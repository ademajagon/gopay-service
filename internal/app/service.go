package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ademajagon/gopay-service/internal/domain"
)

type IdempotencyStore interface {
	// Get returns (result, true, nil), ("", false, nil) if miss
	Get(ctx context.Context, key string) (string, bool, error)
	// Set stores result for key with a TTL
	// Uses SET NX so the first writer wins in a race between two concurrent identical requests
	Set(ctx context.Context, key string, result string, ttl time.Duration) error
}

// OutboxWriter appends domain events to the transactional outbox
type OutboxWriter interface {
	Write(ctx context.Context, aggregateID string, eventType string, payload []byte) error
}

type InitiatePaymentRequest struct {
	OrderID        string
	CustomerID     string
	AmountCents    int64
	Currency       string
	IdempotencyKey string
}

type InitiatePaymentResponse struct {
	PaymentID string
	Status    string
}

func (r InitiatePaymentRequest) Validate() error {
	switch {
	case r.OrderID == "":
		return errors.New("order_id is required")
	case r.CustomerID == "":
		return errors.New("customer_id is required")
	case r.AmountCents <= 0:
		return errors.New("amount_cents must be a positive integer")
	case r.Currency == "":
		return errors.New("currency is required")
	case r.IdempotencyKey == "":
		return errors.New("idempotency_key is required (use the Idempotency-Key header)")
	default:
		return nil
	}
}

const idempotencyTTL = 24 * time.Hour

type PaymentService struct {
	repo       domain.Repository
	idempotent IdempotencyStore
	outbox     OutboxWriter
	log        *slog.Logger
}

func NewPaymentService(
	repo domain.Repository,
	idempotent IdempotencyStore,
	outbox OutboxWriter,
	log *slog.Logger,
) *PaymentService {
	return &PaymentService{
		repo:       repo,
		idempotent: idempotent,
		outbox:     outbox,
		log:        log,
	}
}

func (s *PaymentService) InitiatePayment(ctx context.Context, req InitiatePaymentRequest) (InitiatePaymentResponse, error) {
	if cached, ok, err := s.idempotent.Get(ctx, req.IdempotencyKey); err != nil {
		s.log.WarnContext(ctx, "idempotency cache unavailable, DB check",
			"err", err,
			"idempotency_key", req.IdempotencyKey)
	} else if ok {
		var resp InitiatePaymentResponse
		if err := json.Unmarshal([]byte(cached), &resp); err != nil {
			s.log.WarnContext(ctx, "corrupt idempotency cache entry, evicting", "err", err)
		} else {
			s.log.InfoContext(ctx, "idempotent replay from cache",
				"payment_id", resp.PaymentID,
				"idempotency_key", req.IdempotencyKey,
			)
			return resp, nil
		}
	}

	existing, err := s.repo.FindByIdempotencyKey(req.IdempotencyKey)
	if err != nil {
		return InitiatePaymentResponse{}, fmt.Errorf("idempotency key lookup: %w", err)
	}
	if existing != nil {
		resp := InitiatePaymentResponse{
			PaymentID: existing.ID().String(),
			Status:    string(existing.Status()),
		}

		// re-populate the cache for future requests to skip db next time
		s.cache(ctx, req.IdempotencyKey, resp)
		return resp, nil
	}

	amount, err := domain.NewMoney(req.AmountCents, req.Currency)
	if err != nil {
		return InitiatePaymentResponse{}, fmt.Errorf("invalid amount: %w", err)
	}

	payment, err := domain.New(req.OrderID, req.CustomerID, amount, req.IdempotencyKey)
	if err != nil {
		return InitiatePaymentResponse{}, fmt.Errorf("create payment: %w", err)
	}

	if err := s.repo.Save(payment); err != nil {
		return InitiatePaymentResponse{}, fmt.Errorf("save payment: %w", err)
	}

	for _, evt := range payment.PopEvents() {
		payload, err := json.Marshal(evt)
		if err != nil {
			s.log.ErrorContext(ctx, "marshal event", "event_type", domain.EventType(evt), "err", err)
			continue
		}
		if err := s.outbox.Write(ctx, payment.ID().String(), domain.EventType(evt), payload); err != nil {
			s.log.ErrorContext(ctx, "write event", "event_type", domain.EventType(evt), "err", err)
		}
	}

	// cache result
	resp := InitiatePaymentResponse{
		PaymentID: payment.ID().String(),
		Status:    string(payment.Status()),
	}
	s.cache(ctx, req.IdempotencyKey, resp)

	s.log.InfoContext(ctx, "payment initiated",
		"payment_id", payment.ID().String(),
		"order_id", req.OrderID,
		"customer_id", req.CustomerID,
		"amount", amount.String(),
	)

	return resp, nil
}

func (s *PaymentService) cache(ctx context.Context, key string, resp InitiatePaymentResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		s.log.WarnContext(ctx, "cannot marshal idempotency response for caching", "err", err)
		return
	}
	if err := s.idempotent.Set(ctx, key, string(data), idempotencyTTL); err != nil {
		s.log.WarnContext(ctx, "failed to cache idempotency response", "err", err)
	}
}
