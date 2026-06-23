package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
		err = qtx.InsertPayment(ctx, db.InsertPaymentParams{
			ID:                     p.ID,
			ReferenceID:            p.ReferenceID,
			CallerService:          p.CallerService,
			GatewayType:            string(p.GatewayType),
			GatewayTransactionID:   toNullableString(p.GatewayTransactionID),
			Status:                 string(p.Status),
			Amount:                 p.Amount.Amount,
			Currency:               p.Amount.Currency,
			PaymentMethodType:      toNullableString(p.PaymentMethodType),
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
	} else {
		err = qtx.UpdatePaymentFull(ctx, db.UpdatePaymentFullParams{
			ID:                     p.ID,
			GatewayTransactionID:   toNullableString(p.GatewayTransactionID),
			Status:                 string(p.Status),
			GatewayResponsePayload: marshalJSONOrNil(p.GatewayResponsePayload),
			PaidAt:                 p.PaidAt,
			UpdatedAt:              pgtype.Timestamptz{Time: p.UpdatedAt, Valid: true},
		})
	}
	if err != nil {
		return apperror.Internal("persist payment", err)
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
	return toDomain(row)
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
	return toDomain(row)
}

func (r *PaymentRepo) FindByGatewayTransactionID(ctx context.Context, gatewayTxID string) (*payment.Payment, error) {
	row, err := r.q.GetPaymentByGatewayTransactionID(ctx, &gatewayTxID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperror.NotFound("payment not found for gateway tx: " + gatewayTxID)
	}
	if err != nil {
		return nil, apperror.Internal("get payment by gateway tx", err)
	}
	return toDomain(row)
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
		PaymentMethodType:    derefString(row.PaymentMethodType),
		Description:          row.Description.String,
		ExpiredAt:            row.ExpiredAt,
		PaidAt:               row.PaidAt,
		CreatedAt:            row.CreatedAt.Time,
		UpdatedAt:            row.UpdatedAt.Time,
		DeletedAt:            row.DeletedAt,
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
		       description, expired_at, paid_at, created_at, updated_at, deleted_at
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
			&row.ExpiredAt, &row.PaidAt, &row.CreatedAt, &row.UpdatedAt, &row.DeletedAt,
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
