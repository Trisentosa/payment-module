package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres/db"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PaymentRepo struct {
	pool   *pgxpool.Pool
	q      *db.Queries
	outbox ports.OutboxWriter
}

func NewPaymentRepo(pool *pgxpool.Pool, outbox ports.OutboxWriter) *PaymentRepo {
	return &PaymentRepo{pool: pool, q: db.New(pool), outbox: outbox}
}

func (r *PaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
	log := logger.FromContext(ctx)

	customerInfoJSON, metadataJSON, err := marshalJSONFields(p)
	if err != nil {
		return err
	}

	isNew := isNewPayment(p)

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return apperror.Internal("begin transaction", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	qtx := r.q.WithTx(tx)

	if isNew {
		var inserted int64
		inserted, err = qtx.InsertPayment(ctx, db.InsertPaymentParams{
			ID:                     p.ID,
			ReferenceID:            p.ReferenceID,
			CallerService:          p.CallerService,
			GatewayType:            string(p.GatewayType),
			GatewayTransactionID:   toNullableString(p.GatewayTransactionID),
			Status:                 string(p.Status),
			Amount:                 p.Amount.Amount,
			Currency:               p.Amount.Currency,
			PaymentMethodType:      toNullableString(string(p.PaymentMethodType)),
			CustomerInfo:           customerInfoJSON,
			Metadata:               metadataJSON,
			GatewayRequestPayload:  nil,
			GatewayResponsePayload: marshalJSONOrNil(p.GatewayResponsePayload),
			Description:            pgtype.Text{String: p.Description, Valid: p.Description != ""},
			ExpiredAt:              p.ExpiredAt,
			PaidAt:                 p.PaidAt,
			CreatedAt:              pgtype.Timestamptz{Time: p.CreatedAt, Valid: true},
			UpdatedAt:              pgtype.Timestamptz{Time: p.UpdatedAt, Valid: true},
		})
		// ON CONFLICT DO NOTHING inserts zero rows for a duplicate (reference_id, caller_service).
		// Fail here so no events or outbox rows are written for a payment that was never stored.
		if err == nil && inserted == 0 {
			err = apperror.AlreadyExists("payment already exists for reference_id: " + p.ReferenceID)
			return err
		}
	} else {
		var updated int64
		updated, err = qtx.UpdatePaymentFull(ctx, db.UpdatePaymentFullParams{
			ID:                     p.ID,
			GatewayTransactionID:   toNullableString(p.GatewayTransactionID),
			Status:                 string(p.Status),
			GatewayResponsePayload: marshalJSONOrNil(p.GatewayResponsePayload),
			PaidAt:                 p.PaidAt,
			UpdatedAt:              pgtype.Timestamptz{Time: p.UpdatedAt, Valid: true},
			Version:                int32(p.Version),
		})
		if err == nil && updated == 0 {
			err = apperror.Conflict("payment " + p.ID.String() + " was changed by another request; reload and retry")
			return err
		}
	}
	if err != nil {
		return apperror.Internal("persist payment", err)
	}

	for _, rf := range p.Refunds {
		if err = qtx.UpsertRefund(ctx, toRefundParams(rf)); err != nil {
			return apperror.Internal("persist refund", err)
		}
	}

	events := p.PopEvents()
	for i, evt := range events {
		err = qtx.InsertPaymentEvent(ctx, db.InsertPaymentEventParams{
			AggregateID:    p.ID,
			EventType:      evt.EventType(),
			SequenceNumber: int32(i + 1),
			Payload:        eventPayload(evt),
		})
		if err != nil {
			return apperror.Internal("insert payment_event", err)
		}
	}

	outboxEvents := toOutboxEvents(events)
	if len(outboxEvents) > 0 {
		if err = r.outbox.WriteWithTx(ctx, tx, outboxEvents); err != nil {
			return err
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return apperror.Internal("commit transaction", err)
	}
	if isNew {
		p.Version = 1
	} else {
		p.Version++
	}

	log.Info("payment saved", "payment_id", p.ID, "status", p.Status)
	return nil
}

func (r *PaymentRepo) FindByID(ctx context.Context, id uuid.UUID) (*payment.Payment, error) {
	row, err := r.q.GetPaymentByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("payment not found: " + id.String())
	}
	if err != nil {
		return nil, apperror.Internal("get payment", err)
	}
	return r.withRefunds(ctx, row)
}

func (r *PaymentRepo) FindByReference(ctx context.Context, referenceID, callerService string) (*payment.Payment, error) {
	row, err := r.q.GetPaymentByReference(ctx, db.GetPaymentByReferenceParams{
		ReferenceID:   referenceID,
		CallerService: callerService,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("payment not found for reference: " + referenceID)
	}
	if err != nil {
		return nil, apperror.Internal("get payment by reference", err)
	}
	return r.withRefunds(ctx, row)
}

func (r *PaymentRepo) FindByGatewayTransactionID(ctx context.Context, gatewayTxID string) (*payment.Payment, error) {
	row, err := r.q.GetPaymentByGatewayTransactionID(ctx, &gatewayTxID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("payment not found for gateway tx: " + gatewayTxID)
	}
	if err != nil {
		return nil, apperror.Internal("get payment by gateway tx", err)
	}
	return r.withRefunds(ctx, row)
}

func (r *PaymentRepo) FindExpired(ctx context.Context, limit int) ([]*payment.Payment, error) {
	rows, err := r.q.GetExpiredPayments(ctx, int32(limit))
	if err != nil {
		return nil, apperror.Internal("query expired payments", err)
	}
	payments := make([]*payment.Payment, 0, len(rows))
	for _, row := range rows {
		p, err := toDomain(row)
		if err != nil {
			return nil, err
		}
		payments = append(payments, p)
	}
	return payments, nil
}

func (r *PaymentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status payment.Status, _ map[string]any) error {
	err := r.q.UpdatePaymentStatus(ctx, db.UpdatePaymentStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		return apperror.Internal("update payment status", err)
	}
	return nil
}

func toDomain(row db.Payment) (*payment.Payment, error) {
	p := &payment.Payment{
		ID:                   row.ID,
		ReferenceID:          row.ReferenceID,
		CallerService:        row.CallerService,
		GatewayType:          payment.GatewayType(row.GatewayType),
		GatewayTransactionID: derefString(row.GatewayTransactionID),
		Status:               payment.Status(row.Status),
		Amount:               payment.Money{Amount: row.Amount, Currency: row.Currency},
		PaymentMethodType:    payment.Method(derefString(row.PaymentMethodType)),
		Description:          row.Description.String,
		ExpiredAt:            row.ExpiredAt,
		PaidAt:               row.PaidAt,
		CreatedAt:            row.CreatedAt.Time,
		UpdatedAt:            row.UpdatedAt.Time,
		DeletedAt:            row.DeletedAt,
		Version:              int(row.Version),
	}
	if err := json.Unmarshal(row.CustomerInfo, &p.CustomerInfo); err != nil {
		return nil, apperror.Internal("unmarshal customer_info", err)
	}
	if row.Metadata != nil {
		if err := json.Unmarshal(row.Metadata, &p.Metadata); err != nil {
			return nil, apperror.Internal("unmarshal metadata", err)
		}
	}
	if row.GatewayRequestPayload != nil {
		if err := json.Unmarshal(row.GatewayRequestPayload, &p.GatewayRequestPayload); err != nil {
			return nil, apperror.Internal("unmarshal gateway_request_payload", err)
		}
	}
	if row.GatewayResponsePayload != nil {
		if err := json.Unmarshal(row.GatewayResponsePayload, &p.GatewayResponsePayload); err != nil {
			return nil, apperror.Internal("unmarshal gateway_response_payload", err)
		}
	}
	return p, nil
}

// isNewPayment returns true if the payment has a PaymentInitiated event pending,
// meaning it has never been persisted before.
func isNewPayment(p *payment.Payment) bool {
	for _, e := range p.PeekEvents() {
		if _, ok := e.(payment.PaymentInitiated); ok {
			return true
		}
	}
	return false
}

func toOutboxEvents(events []payment.DomainEvent) []ports.OutboxEvent {
	out := make([]ports.OutboxEvent, 0, len(events))
	for _, e := range events {
		payload, _ := json.Marshal(e)
		out = append(out, ports.OutboxEvent{
			AggregateID: e.AggregateID(),
			EventType:   e.EventType(),
			Payload:     payload,
		})
	}
	return out
}

func marshalJSONOrNil(v map[string]any) []byte {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func (r *PaymentRepo) ListPaginated(ctx context.Context, f payment.ListFilter) ([]*payment.Payment, error) {
	var (
		args  []any
		where []string
		i     = 1
	)

	where = append(where, "deleted_at IS NULL")

	if f.Status != "" {
		where = append(where, fmt.Sprintf("status = $%d", i))
		args = append(args, f.Status)
		i++
	}
	if f.CallerService != "" {
		where = append(where, fmt.Sprintf("caller_service = $%d", i))
		args = append(args, f.CallerService)
		i++
	}
	if f.GatewayType != "" {
		where = append(where, fmt.Sprintf("gateway_type = $%d", i))
		args = append(args, f.GatewayType)
		i++
	}
	if f.ReferenceID != "" {
		where = append(where, fmt.Sprintf("reference_id LIKE $%d", i))
		args = append(args, f.ReferenceID+"%")
		i++
	}
	if f.From != nil {
		where = append(where, fmt.Sprintf("created_at >= $%d", i))
		args = append(args, *f.From)
		i++
	}
	if f.To != nil {
		where = append(where, fmt.Sprintf("created_at <= $%d", i))
		args = append(args, *f.To)
		i++
	}
	if f.Cursor != nil {
		where = append(where, fmt.Sprintf("created_at < $%d", i))
		args = append(args, *f.Cursor)
		i++
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}

	q := `SELECT id, reference_id, caller_service, gateway_type, gateway_transaction_id,
		       status, amount, currency, payment_method_type,
		       customer_info, metadata, gateway_request_payload, gateway_response_payload,
		       description, expired_at, paid_at, created_at, updated_at, deleted_at, version
		  FROM payments`
	for j, c := range where {
		if j == 0 {
			q += " WHERE " + c
		} else {
			q += " AND " + c
		}
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", i)
	args = append(args, int32(limit))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, apperror.Internal("list payments", err)
	}
	defer rows.Close()

	var results []*payment.Payment
	for rows.Next() {
		var row db.Payment
		if err = rows.Scan(
			&row.ID, &row.ReferenceID, &row.CallerService, &row.GatewayType,
			&row.GatewayTransactionID, &row.Status, &row.Amount, &row.Currency,
			&row.PaymentMethodType, &row.CustomerInfo, &row.Metadata,
			&row.GatewayRequestPayload, &row.GatewayResponsePayload, &row.Description,
			&row.ExpiredAt, &row.PaidAt, &row.CreatedAt, &row.UpdatedAt, &row.DeletedAt, &row.Version,
		); err != nil {
			return nil, apperror.Internal("scan payment row", err)
		}
		p, err := toDomain(row)
		if err != nil {
			return nil, err
		}
		results = append(results, p)
	}
	if err = rows.Err(); err != nil {
		return nil, apperror.Internal("list payments rows", err)
	}
	return results, nil
}

// withRefunds maps a payment row and loads its refunds, which commands need to enforce
// refund limits. List queries skip this; they never feed a save.
func (r *PaymentRepo) withRefunds(ctx context.Context, row db.Payment) (*payment.Payment, error) {
	p, err := toDomain(row)
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListRefundsByPayment(ctx, p.ID)
	if err != nil {
		return nil, apperror.Internal("list refunds", err)
	}
	for _, rr := range rows {
		p.Refunds = append(p.Refunds, &payment.Refund{
			ID:              rr.ID,
			PaymentID:       rr.PaymentID,
			ReferenceID:     rr.ReferenceID,
			Amount:          rr.Amount,
			Reason:          rr.Reason.String,
			Status:          payment.RefundStatus(rr.Status),
			GatewayRefundID: derefString(rr.GatewayRefundID),
			FailureReason:   rr.FailureReason.String,
			RequestedBy:     rr.RequestedBy,
			CreatedAt:       rr.CreatedAt.Time,
			UpdatedAt:       rr.UpdatedAt.Time,
			CompletedAt:     rr.CompletedAt,
		})
	}
	return p, nil
}

func toRefundParams(rf *payment.Refund) db.UpsertRefundParams {
	return db.UpsertRefundParams{
		ID:              rf.ID,
		PaymentID:       rf.PaymentID,
		ReferenceID:     rf.ReferenceID,
		GatewayRefundID: toNullableString(rf.GatewayRefundID),
		Status:          string(rf.Status),
		Amount:          rf.Amount,
		Reason:          pgtype.Text{String: rf.Reason, Valid: rf.Reason != ""},
		FailureReason:   pgtype.Text{String: rf.FailureReason, Valid: rf.FailureReason != ""},
		RequestedBy:     rf.RequestedBy,
		CreatedAt:       pgtype.Timestamptz{Time: rf.CreatedAt, Valid: true},
		UpdatedAt:       pgtype.Timestamptz{Time: rf.UpdatedAt, Valid: true},
		CompletedAt:     rf.CompletedAt,
	}
}

// FindAwaitingChargeOutcome returns INITIATED payments created before cutoff: their charge
// call ended without a usable answer, so only the gateway knows what happened.
func (r *PaymentRepo) FindAwaitingChargeOutcome(ctx context.Context, cutoff time.Time, limit int) ([]*payment.Payment, error) {
	rows, err := r.q.GetPaymentsAwaitingChargeOutcome(ctx, db.GetPaymentsAwaitingChargeOutcomeParams{
		CreatedAt: pgtype.Timestamptz{Time: cutoff, Valid: true},
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, apperror.Internal("query payments awaiting charge outcome", err)
	}
	out := make([]*payment.Payment, 0, len(rows))
	for _, row := range rows {
		p, err := toDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// FindWithPendingRefunds returns payments (refunds loaded) that have a refund still PENDING
// since before cutoff.
func (r *PaymentRepo) FindWithPendingRefunds(ctx context.Context, cutoff time.Time, limit int) ([]*payment.Payment, error) {
	ids, err := r.q.GetPaymentIDsWithPendingRefunds(ctx, db.GetPaymentIDsWithPendingRefundsParams{
		CreatedAt: pgtype.Timestamptz{Time: cutoff, Valid: true},
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, apperror.Internal("query payments with pending refunds", err)
	}
	out := make([]*payment.Payment, 0, len(ids))
	for _, id := range ids {
		p, err := r.FindByID(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
