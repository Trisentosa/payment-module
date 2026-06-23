package postgres

import (
	"context"
	"encoding/json"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres/db"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/jackc/pgx/v5"
)

type OutboxWriter struct{}

func NewOutboxWriter() *OutboxWriter { return &OutboxWriter{} }

func (w *OutboxWriter) WriteWithTx(ctx context.Context, tx any, events []ports.OutboxEvent) error {
	pgxTx, ok := tx.(pgx.Tx)
	if !ok {
		return apperror.Internal("outbox: invalid transaction type", nil)
	}
	qtx := db.New(pgxTx)
	for _, e := range events {
		if err := qtx.InsertOutboxEvent(ctx, db.InsertOutboxEventParams{
			AggregateID: e.AggregateID,
			EventType:   e.EventType,
			Payload:     json.RawMessage(e.Payload),
		}); err != nil {
			return apperror.Internal("insert outbox event", err)
		}
	}
	return nil
}
