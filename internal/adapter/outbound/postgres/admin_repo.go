package postgres

import (
	"context"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres/db"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AdminRepo struct {
	q *db.Queries
}

func NewAdminRepo(pool *pgxpool.Pool) *AdminRepo {
	return &AdminRepo{q: db.New(pool)}
}

func (r *AdminRepo) GetPaymentAttempts(ctx context.Context, paymentID uuid.UUID) ([]ports.AttemptRow, error) {
	rows, err := r.q.GetPaymentAttemptsByPayment(ctx, paymentID)
	if err != nil {
		return nil, apperror.Internal("get payment attempts", err)
	}
	out := make([]ports.AttemptRow, 0, len(rows))
	for _, row := range rows {
		a := ports.AttemptRow{
			ID:            row.ID,
			AttemptNumber: row.AttemptNumber,
			Status:        row.Status,
			CreatedAt:     row.CreatedAt.Time,
		}
		if row.ErrorCode != nil {
			a.ErrorCode = *row.ErrorCode
		}
		if row.GatewayTransactionID != nil {
			a.GatewayTransactionID = *row.GatewayTransactionID
		}
		if row.DurationMs.Valid {
			a.DurationMs = row.DurationMs.Int64
		}
		out = append(out, a)
	}
	return out, nil
}

func (r *AdminRepo) GetPaymentEvents(ctx context.Context, paymentID uuid.UUID) ([]ports.EventRow, error) {
	rows, err := r.q.GetPaymentEventsByPayment(ctx, paymentID)
	if err != nil {
		return nil, apperror.Internal("get payment events", err)
	}
	out := make([]ports.EventRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, ports.EventRow{
			ID:             row.ID,
			EventType:      row.EventType,
			SequenceNumber: row.SequenceNumber,
			Payload:        row.Payload,
			CreatedAt:      row.CreatedAt.Time,
		})
	}
	return out, nil
}

func (r *AdminRepo) GetRefunds(ctx context.Context, paymentID uuid.UUID) ([]ports.RefundRow, error) {
	rows, err := r.q.GetRefundsByPayment(ctx, paymentID)
	if err != nil {
		return nil, apperror.Internal("get refunds", err)
	}
	out := make([]ports.RefundRow, 0, len(rows))
	for _, row := range rows {
		ref := ports.RefundRow{
			ID:          row.ID,
			ReferenceID: row.ReferenceID,
			Status:      row.Status,
			Amount:      row.Amount,
			CreatedAt:   row.CreatedAt.Time,
		}
		if row.GatewayRefundID != nil {
			ref.GatewayRefundID = *row.GatewayRefundID
		}
		if row.Reason.Valid {
			ref.Reason = row.Reason.String
		}
		out = append(out, ref)
	}
	return out, nil
}

func (r *AdminRepo) GetOutboxStats(ctx context.Context) (*ports.OutboxStats, error) {
	rows, err := r.q.GetOutboxStats(ctx)
	if err != nil {
		return nil, apperror.Internal("get outbox stats", err)
	}
	result := &ports.OutboxStats{}
	for _, row := range rows {
		ageSecs := toInt(row.OldestAgeSeconds)
		switch row.Status {
		case "PENDING":
			result.Pending = int(row.Count)
			result.OldestPendingAgeSecs = ageSecs
		case "PUBLISHED":
			result.Published = int(row.Count)
		case "FAILED":
			result.Failed = int(row.Count)
		}
	}
	return result, nil
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}
