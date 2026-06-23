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
