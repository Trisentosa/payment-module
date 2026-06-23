package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type DomainEvent struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	EventType   string
	Payload     []byte
	OccurredAt  time.Time
}

type EventPublisher interface {
	Publish(ctx context.Context, event DomainEvent) error
}
