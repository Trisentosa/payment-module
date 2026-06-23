package ports

import (
	"context"

	"github.com/google/uuid"
)

type DomainEvent struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	EventType   string
	Payload     []byte
}

type EventPublisher interface {
	Publish(ctx context.Context, event DomainEvent) error
}
