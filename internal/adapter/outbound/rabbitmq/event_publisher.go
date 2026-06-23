package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

const (
	exchange    = "paygate.payments"
	contentType = "application/json"
)

var routingKey = map[string]string{
	"PaymentInitiated": "payment.initiated",
	"PaymentPending":   "payment.pending",
	"PaymentCompleted": "payment.completed",
	"PaymentFailed":    "payment.failed",
	"PaymentCancelled": "payment.cancelled",
	"PaymentExpired":   "payment.expired",
	"RefundRequested":  "refund.requested",
	"RefundCompleted":  "refund.completed",
}

type Publisher struct {
	conn    *amqp.Connection
	channel *amqp.Channel
}

func NewPublisher(url string) (*Publisher, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rabbitmq channel: %w", err)
	}
	closeAll := func() { _ = ch.Close(); _ = conn.Close() }
	if err = ch.ExchangeDeclare(exchange, "topic", true, false, false, false, nil); err != nil {
		closeAll()
		return nil, fmt.Errorf("exchange declare: %w", err)
	}
	if err = ch.Confirm(false); err != nil {
		closeAll()
		return nil, fmt.Errorf("confirm mode: %w", err)
	}

	// Declare dead-letter exchange and queue for failed events.
	if err = ch.ExchangeDeclare("paygate.payments.dlx", "fanout", true, false, false, false, nil); err != nil {
		closeAll()
		return nil, fmt.Errorf("dlx declare: %w", err)
	}
	if _, err = ch.QueueDeclare("paygate.payments.dead", true, false, false, false, amqp.Table{
		"x-dead-letter-exchange": "paygate.payments.dlx",
	}); err != nil {
		closeAll()
		return nil, fmt.Errorf("dead-letter queue declare: %w", err)
	}

	return &Publisher{conn: conn, channel: ch}, nil
}

func (p *Publisher) Publish(ctx context.Context, event ports.DomainEvent) error {
	log := logger.FromContext(ctx)

	rk, ok := routingKey[event.EventType]
	if !ok {
		log.Warn("unknown event type, skipping publish", "event_type", event.EventType)
		return nil
	}

	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}

	envelope := map[string]any{
		"event_id":       event.ID,
		"event_type":     rk,
		"aggregate_id":   event.AggregateID,
		"aggregate_type": "PAYMENT",
		"version":        1,
		"occurred_at":    occurredAt,
		"data":           json.RawMessage(event.Payload),
		"source_service": "paygate-service",
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return apperror.Internal("marshal event envelope", err)
	}

	confirms := p.channel.NotifyPublish(make(chan amqp.Confirmation, 1))

	err = p.channel.PublishWithContext(ctx, exchange, rk, false, false, amqp.Publishing{
		ContentType:  contentType,
		DeliveryMode: amqp.Persistent,
		MessageId:    event.ID.String(),
		Timestamp:    time.Now().UTC(),
		Body:         body,
	})
	if err != nil {
		return apperror.GatewayError("rabbitmq publish", err)
	}

	select {
	case confirm := <-confirms:
		if !confirm.Ack {
			return apperror.GatewayError("rabbitmq nack received", nil)
		}
	case <-ctx.Done():
		return apperror.Internal("publish context cancelled", ctx.Err())
	}

	log.Info("event published", "routing_key", rk, "aggregate_id", event.AggregateID)
	return nil
}

func (p *Publisher) Close() {
	_ = p.channel.Close()
	_ = p.conn.Close()
}
