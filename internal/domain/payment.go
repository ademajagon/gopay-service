package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound = errors.New("payment not found")

	ErrVersionConflict = errors.New("payment version conflict")

	ErrInvalidTransition = errors.New("invalid payment status transition")
)

type PaymentID struct{ value string }

func NewPaymentID() PaymentID { return PaymentID{value: uuid.New().String()} }

func ParsePaymentID(s string) (PaymentID, error) {
	if _, err := uuid.Parse(s); err != nil {
		return PaymentID{}, fmt.Errorf("invalid payment ID: %q", s)
	}
	return PaymentID{value: s}, nil
}

func (id PaymentID) String() string { return id.value }

type Money struct {
	amount   int64
	currency string
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount <= 0 {
		return Money{}, fmt.Errorf("amount must be positive, got %d", amount)
	}
	c := strings.ToUpper(strings.TrimSpace(currency))
	if len(c) != 3 {
		return Money{}, fmt.Errorf("currency must be a 3-letter, got %q", c)
	}
	return Money{amount: amount, currency: c}, nil
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }
func (m Money) String() string   { return fmt.Sprintf("%d %s", m.amount, m.currency) }

type PaymentStatus string

const (
	StatusPending    PaymentStatus = "PENDING"
	StatusProcessing PaymentStatus = "PROCESSING"
	StatusCompleted  PaymentStatus = "COMPLETED"
	StatusFailed     PaymentStatus = "FAILED"
)

type Event interface {
	eventType() string
}

type PaymentInitiated struct {
	PaymentID  string
	OrderID    string
	Amount     int64
	Currency   string
	OccurredAt time.Time
}

func (e PaymentInitiated) eventType() string { return "payment.initiated" }

func EventType(e Event) string { return e.eventType() }

type Payment struct {
	id             PaymentID
	orderID        string
	customerID     string
	amount         Money
	status         PaymentStatus
	providerRef    string // gateway transaction id, set when processing
	failureReason  string
	idempotencyKey string // deduplication key
	createdAt      time.Time
	updatedAt      time.Time

	version int

	events []Event
}

func New(orderID, customerID string, amount Money, idempotencyKey string) (*Payment, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.New("orderID is required")
	}
	if strings.TrimSpace(customerID) == "" {
		return nil, errors.New("customerID is required")
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, errors.New("idempotencyKey is required")
	}

	now := time.Now().UTC()
	p := &Payment{
		id:             NewPaymentID(),
		orderID:        orderID,
		customerID:     customerID,
		amount:         amount,
		status:         StatusPending,
		idempotencyKey: idempotencyKey,
		createdAt:      now,
		updatedAt:      now,
		version:        1,
	}

	p.events = append(p.events, PaymentInitiated{
		PaymentID:  p.id.String(),
		OrderID:    orderID,
		Amount:     amount.Amount(),
		Currency:   amount.Currency(),
		OccurredAt: p.createdAt,
	})

	return p, nil
}

func (p *Payment) ID() PaymentID          { return p.id }
func (p *Payment) OrderID() string        { return p.orderID }
func (p *Payment) CustomerID() string     { return p.customerID }
func (p *Payment) Amount() Money          { return p.amount }
func (p *Payment) Status() PaymentStatus  { return p.status }
func (p *Payment) ProviderRef() string    { return p.providerRef }
func (p *Payment) FailureReason() string  { return p.failureReason }
func (p *Payment) IdempotencyKey() string { return p.idempotencyKey }
func (p *Payment) CreatedAt() time.Time   { return p.createdAt }
func (p *Payment) UpdatedAt() time.Time   { return p.updatedAt }
func (p *Payment) Version() int           { return p.version }

func (p *Payment) PopEvents() []Event {
	events := p.events
	p.events = nil
	return events
}

func Reconstitute(
	id PaymentID,
	orderID, customerID string,
	amount Money,
	status PaymentStatus,
	providerRef, failureReason, idempotencyKey string,
	createdAt, updatedAt time.Time,
	version int,
) *Payment {
	return &Payment{
		id:             id,
		orderID:        orderID,
		customerID:     customerID,
		amount:         amount,
		status:         status,
		providerRef:    providerRef,
		failureReason:  failureReason,
		idempotencyKey: idempotencyKey,
		createdAt:      createdAt,
		updatedAt:      updatedAt,
		version:        version,
	}
}

type Repository interface {
	// Save inserts a new Payment or updates an existing one - upsert
	Save(p *Payment) error

	// FindByIdempotencyKey looks up a payment by its idempotency key
	FindByIdempotencyKey(key string) (*Payment, error)
}
