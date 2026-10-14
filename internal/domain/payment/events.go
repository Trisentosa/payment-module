package payment

import (
	"time"

	"github.com/google/uuid"
)

type DomainEvent interface {
	EventType() string
	AggregateID() uuid.UUID
}

type PaymentInitiated struct {
	PaymentID  uuid.UUID
	OccurredAt time.Time
}

func (e PaymentInitiated) EventType() string      { return "PaymentInitiated" }
func (e PaymentInitiated) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentPending struct {
	PaymentID   uuid.UUID
	GatewayTxID string
	OccurredAt  time.Time
}

func (e PaymentPending) EventType() string      { return "PaymentPending" }
func (e PaymentPending) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentCompleted struct {
	PaymentID  uuid.UUID
	PaidAt     time.Time
	OccurredAt time.Time
}

func (e PaymentCompleted) EventType() string      { return "PaymentCompleted" }
func (e PaymentCompleted) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentFailed struct {
	PaymentID    uuid.UUID
	ErrorCode    string
	ErrorMessage string
	OccurredAt   time.Time
}

func (e PaymentFailed) EventType() string      { return "PaymentFailed" }
func (e PaymentFailed) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentCancelled struct {
	PaymentID  uuid.UUID
	OccurredAt time.Time
}

func (e PaymentCancelled) EventType() string      { return "PaymentCancelled" }
func (e PaymentCancelled) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentExpired struct {
	PaymentID  uuid.UUID
	OccurredAt time.Time
}

func (e PaymentExpired) EventType() string      { return "PaymentExpired" }
func (e PaymentExpired) AggregateID() uuid.UUID { return e.PaymentID }

type RefundRequested struct {
	PaymentID  uuid.UUID
	RefundID   uuid.UUID
	Amount     int64
	OccurredAt time.Time
}

func (e RefundRequested) EventType() string      { return "RefundRequested" }
func (e RefundRequested) AggregateID() uuid.UUID { return e.PaymentID }

type RefundCompleted struct {
	PaymentID      uuid.UUID
	RefundID       uuid.UUID
	Amount         int64
	RefundedAmount int64  // total refunded on the payment after this refund
	PaymentStatus  Status // PARTIALLY_REFUNDED or REFUNDED
	OccurredAt     time.Time
}

func (e RefundCompleted) EventType() string      { return "RefundCompleted" }
func (e RefundCompleted) AggregateID() uuid.UUID { return e.PaymentID }

type RefundFailed struct {
	PaymentID  uuid.UUID
	RefundID   uuid.UUID
	Reason     string
	OccurredAt time.Time
}

func (e RefundFailed) EventType() string      { return "RefundFailed" }
func (e RefundFailed) AggregateID() uuid.UUID { return e.PaymentID }
