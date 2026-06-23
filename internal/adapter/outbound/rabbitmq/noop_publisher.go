package rabbitmq

import (
	"context"

	"github.com/Trisentosa/payment-module/internal/application/ports"
)

// NoopPublisher is a stub used until the real RabbitMQ publisher is wired in TD-03.
type NoopPublisher struct{}

func (n *NoopPublisher) Publish(_ context.Context, _ ports.DomainEvent) error { return nil }
