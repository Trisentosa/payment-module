package rabbitmq

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

// Every domain event needs a routing key: the publisher drops unroutable events (with a
// warning), so a forgotten entry means consumers silently never hear about it.
func TestEveryDomainEventIsRoutable(t *testing.T) {
	events := []payment.DomainEvent{
		payment.PaymentInitiated{}, payment.PaymentPending{}, payment.PaymentCompleted{},
		payment.PaymentFailed{}, payment.PaymentCancelled{}, payment.PaymentExpired{},
		payment.RefundRequested{}, payment.RefundCompleted{}, payment.RefundFailed{},
	}
	for _, e := range events {
		if _, ok := routingKey[e.EventType()]; !ok {
			t.Errorf("%s has no routing key", e.EventType())
		}
	}
}

func TestEnvelope(t *testing.T) {
	id, agg := uuid.New(), uuid.New()
	at := time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC)

	rk, body, err := envelope(ports.DomainEvent{
		ID: id, AggregateID: agg, EventType: "RefundCompleted", Payload: []byte(`{"Amount":40000}`), OccurredAt: at,
	})
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if rk != "refund.completed" {
		t.Errorf("routing key: %s", rk)
	}

	var got struct {
		EventID     uuid.UUID       `json:"event_id"`
		EventType   string          `json:"event_type"`
		AggregateID uuid.UUID       `json:"aggregate_id"`
		Version     int             `json:"version"`
		OccurredAt  time.Time       `json:"occurred_at"`
		Data        json.RawMessage `json:"data"`
		Source      string          `json:"source_service"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.EventID != id || got.EventType != "refund.completed" || got.AggregateID != agg || got.Version != 1 ||
		!got.OccurredAt.Equal(at) || string(got.Data) != `{"Amount":40000}` || got.Source != "paygate-service" {
		t.Errorf("envelope: %+v", got)
	}
}

func TestEnvelope_UnknownEventType(t *testing.T) {
	if _, _, err := envelope(ports.DomainEvent{EventType: "Mystery"}); !errors.Is(err, errUnroutable) {
		t.Errorf("got %v, want errUnroutable", err)
	}
}
