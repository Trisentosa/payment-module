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
	PaymentID  uuid.UUID `json:"payment_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (e PaymentInitiated) EventType() string      { return "PaymentInitiated" }
func (e PaymentInitiated) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentPending struct {
	PaymentID   uuid.UUID `json:"payment_id"`
	GatewayTxID string    `json:"gateway_transaction_id"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func (e PaymentPending) EventType() string      { return "PaymentPending" }
func (e PaymentPending) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentCompleted struct {
	PaymentID  uuid.UUID `json:"payment_id"`
	PaidAt     time.Time `json:"paid_at"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (e PaymentCompleted) EventType() string      { return "PaymentCompleted" }
func (e PaymentCompleted) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentFailed struct {
	PaymentID    uuid.UUID `json:"payment_id"`
	ErrorCode    string    `json:"error_code"`
	ErrorMessage string    `json:"error_message"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (e PaymentFailed) EventType() string      { return "PaymentFailed" }
func (e PaymentFailed) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentCancelled struct {
	PaymentID  uuid.UUID `json:"payment_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (e PaymentCancelled) EventType() string      { return "PaymentCancelled" }
func (e PaymentCancelled) AggregateID() uuid.UUID { return e.PaymentID }

type PaymentExpired struct {
	PaymentID  uuid.UUID `json:"payment_id"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (e PaymentExpired) EventType() string      { return "PaymentExpired" }
func (e PaymentExpired) AggregateID() uuid.UUID { return e.PaymentID }

type RefundRequested struct {
	PaymentID  uuid.UUID `json:"payment_id"`
	RefundID   uuid.UUID `json:"refund_id"`
	Amount     int64     `json:"amount"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (e RefundRequested) EventType() string      { return "RefundRequested" }
func (e RefundRequested) AggregateID() uuid.UUID { return e.PaymentID }

type RefundCompleted struct {
	PaymentID      uuid.UUID `json:"payment_id"`
	RefundID       uuid.UUID `json:"refund_id"`
	Amount         int64     `json:"amount"`
	RefundedAmount int64     `json:"refunded_amount"` // total refunded on the payment after this refund
	PaymentStatus  Status    `json:"payment_status"`  // PARTIALLY_REFUNDED or REFUNDED
	OccurredAt     time.Time `json:"occurred_at"`
}

func (e RefundCompleted) EventType() string      { return "RefundCompleted" }
func (e RefundCompleted) AggregateID() uuid.UUID { return e.PaymentID }

type RefundFailed struct {
	PaymentID  uuid.UUID `json:"payment_id"`
	RefundID   uuid.UUID `json:"refund_id"`
	Reason     string    `json:"reason"`
	OccurredAt time.Time `json:"occurred_at"`
}

func (e RefundFailed) EventType() string      { return "RefundFailed" }
func (e RefundFailed) AggregateID() uuid.UUID { return e.PaymentID }
