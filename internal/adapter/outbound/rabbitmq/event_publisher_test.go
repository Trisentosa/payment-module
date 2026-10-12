//go:build integration

package rabbitmq_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/rabbitmq"
	"github.com/Trisentosa/payment-module/internal/application/ports"
)

func startRabbitMQ(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "rabbitmq:3.13-alpine",
			ExposedPorts: []string{"5672/tcp"},
			// The default guest user may only connect from localhost inside the container.
			Env: map[string]string{
				"RABBITMQ_DEFAULT_USER": "paygate",
				"RABBITMQ_DEFAULT_PASS": "secret",
			},
			WaitingFor: wait.ForLog("Server startup complete").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("start rabbitmq container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := c.MappedPort(ctx, "5672/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}
	return fmt.Sprintf("amqp://paygate:secret@%s:%s/", host, port.Port())
}

// Regression: the publisher used to register a confirm listener per publish, which
// deadlocked the connection on the third publish.
func TestPublisher_PublishesManyEventsInSequence(t *testing.T) {
	pub, err := rabbitmq.NewPublisher(startRabbitMQ(t))
	if err != nil {
		t.Fatalf("NewPublisher: %v", err)
	}
	t.Cleanup(pub.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := range 10 {
		err := pub.Publish(ctx, ports.DomainEvent{
			ID:          uuid.New(),
			AggregateID: uuid.New(),
			EventType:   "PaymentPending",
			Payload:     []byte(`{"n":` + fmt.Sprint(i) + `}`),
			OccurredAt:  time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("publish #%d: %v", i+1, err)
		}
	}
}
