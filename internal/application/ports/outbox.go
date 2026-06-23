package ports

import (
	"context"

	"github.com/google/uuid"
)

type OutboxEvent struct {
	AggregateID uuid.UUID
	EventType   string
	Payload     []byte
}

type OutboxWriter interface {
	// WriteWithTx writes outbox rows inside an existing DB transaction.
	// tx is a *pgx.Tx typed as any to avoid coupling this port to pgx.
	WriteWithTx(ctx context.Context, tx any, events []OutboxEvent) error
}
