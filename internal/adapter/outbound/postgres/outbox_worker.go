package postgres

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres/db"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
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
			w.RunOnce(ctx)
		}
	}
}

// RunOnce publishes one batch of due outbox events. The batch is claimed with
// FOR UPDATE SKIP LOCKED inside a transaction held until every row is marked, so
// concurrent workers (other replicas) skip these rows instead of publishing them again.
// If the worker dies mid-batch the transaction rolls back and the rows are published
// again later: delivery is at-least-once, and consumers deduplicate on event_id.
func (w *OutboxWorker) RunOnce(ctx context.Context) {
	log := logger.FromContext(ctx)

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		log.Error("outbox: begin transaction", "err", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit
	qtx := w.q.WithTx(tx)

	rows, err := qtx.GetPendingOutboxEvents(ctx, int32(w.batchSize))
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
			OccurredAt:  row.CreatedAt.Time.UTC(),
		})
		if pubErr != nil {
			log.Error("outbox publish failed", "outbox_id", row.ID, "err", pubErr)
			err = qtx.MarkOutboxEventFailed(ctx, db.MarkOutboxEventFailedParams{
				ID:        row.ID,
				LastError: pgtype.Text{String: pubErr.Error(), Valid: true},
			})
		} else {
			err = qtx.MarkOutboxEventPublished(ctx, row.ID)
		}
		if err != nil {
			log.Error("outbox: mark event", "outbox_id", row.ID, "err", err)
			return
		}
	}

	if err = tx.Commit(ctx); err != nil {
		log.Error("outbox: commit", "err", err)
	}
}
