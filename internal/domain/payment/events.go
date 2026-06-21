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
