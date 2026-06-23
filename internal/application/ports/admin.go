package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type AttemptRow struct {
	ID                   uuid.UUID
	AttemptNumber        int32
	Status               string
	ErrorCode            string
	GatewayTransactionID string
	DurationMs           int64
	CreatedAt            time.Time
}

type EventRow struct {
	ID             uuid.UUID
	EventType      string
	SequenceNumber int32
	Payload        []byte
	CreatedAt      time.Time
}

type RefundRow struct {
	ID              uuid.UUID
	ReferenceID     string
	GatewayRefundID string
	Status          string
	Amount          int64
	Reason          string
	CreatedAt       time.Time
}

type OutboxStats struct {
	Pending              int
	Published            int
	Failed               int
	OldestPendingAgeSecs int
}

type AdminReader interface {
	GetPaymentAttempts(ctx context.Context, paymentID uuid.UUID) ([]AttemptRow, error)
	GetPaymentEvents(ctx context.Context, paymentID uuid.UUID) ([]EventRow, error)
	GetRefunds(ctx context.Context, paymentID uuid.UUID) ([]RefundRow, error)
	GetOutboxStats(ctx context.Context) (*OutboxStats, error)
}
