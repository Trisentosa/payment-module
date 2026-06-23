package postgres

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres/db"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxWorker struct {
	pool      *pgxpool.Pool
	q         *db.Queries
	publisher ports.EventPublisher
	batchSize int
	interval  time.Duration
}

func NewOutboxWorker(pool *pgxpool.Pool, publisher ports.EventPublisher, batchSize int, interval time.Duration) *OutboxWorker {
	return &OutboxWorker{
		pool:      pool,
		q:         db.New(pool),
		publisher: publisher,
		batchSize: batchSize,
		interval:  interval,
	}
}

func (w *OutboxWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *OutboxWorker) processBatch(ctx context.Context) {
	log := logger.FromContext(ctx)

	rows, err := w.q.GetPendingOutboxEvents(ctx, int32(w.batchSize))
	if err != nil {
		log.Error("outbox poll failed", "err", err)
		return
	}

	for _, row := range rows {
		pubErr := w.publisher.Publish(ctx, ports.DomainEvent{
			ID:          row.ID,
			AggregateID: row.AggregateID,
			EventType:   row.EventType,
			Payload:     row.Payload,
		})
		if pubErr != nil {
			w.markFailed(ctx, row.ID, pubErr)
			log.Error("outbox publish failed", "outbox_id", row.ID, "err", pubErr)
			continue
		}
		w.markPublished(ctx, row.ID)
	}
}

func (w *OutboxWorker) markPublished(ctx context.Context, id uuid.UUID) {
	if err := w.q.MarkOutboxEventPublished(ctx, id); err != nil {
		logger.FromContext(ctx).Error("outbox mark published failed", "outbox_id", id, "err", err)
	}
}

func (w *OutboxWorker) markFailed(ctx context.Context, id uuid.UUID, err error) {
	if dbErr := w.q.MarkOutboxEventFailed(ctx, db.MarkOutboxEventFailedParams{
		ID:        id,
		LastError: pgtype.Text{String: err.Error(), Valid: true},
	}); dbErr != nil {
		logger.FromContext(ctx).Error("outbox mark failed failed", "outbox_id", id, "err", dbErr)
	}
}
