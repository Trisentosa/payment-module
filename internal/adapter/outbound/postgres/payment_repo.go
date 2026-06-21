package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PaymentRepo struct {
	pool *pgxpool.Pool
}

func NewPaymentRepo(pool *pgxpool.Pool) *PaymentRepo {
	return &PaymentRepo{pool: pool}
}

func (r *PaymentRepo) Save(ctx context.Context, p *payment.Payment) error {
	log := logger.FromContext(ctx)

	customerInfoJSON, metadataJSON, err := marshalJSONFields(p)
	if err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return apperror.Internal("begin transaction", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	_, err = tx.Exec(ctx, `
		INSERT INTO payments (
			id, reference_id, caller_service, gateway_type, gateway_transaction_id,
			status, amount, currency, payment_method_type,
			customer_info, metadata, gateway_request_payload, gateway_response_payload,
			description, expired_at, paid_at, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18
		)
		ON CONFLICT (reference_id, caller_service) DO NOTHING`,
		p.ID, p.ReferenceID, p.CallerService, p.GatewayType, nilIfEmpty(p.GatewayTransactionID),
		p.Status, p.Amount.Amount, p.Amount.Currency, nilIfEmpty(p.PaymentMethodType),
		customerInfoJSON, metadataJSON, nil, nil,
		nilIfEmpty(p.Description), p.ExpiredAt, p.PaidAt, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return apperror.Internal("insert payment", err)
	}

	events := p.PopEvents()
	for i, evt := range events {
		_, err = tx.Exec(ctx, `
			INSERT INTO payment_events (aggregate_id, aggregate_type, event_type, sequence_number, payload)
			VALUES ($1, 'PAYMENT', $2, $3, $4)`,
			p.ID, evt.EventType(), i+1, eventPayload(evt),
		)
		if err != nil {
			return apperror.Internal("insert payment_event", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return apperror.Internal("commit transaction", err)
	}

	log.Info("payment saved", "payment_id", p.ID, "status", p.Status)
	return nil
}

func (r *PaymentRepo) FindByID(ctx context.Context, id uuid.UUID) (*payment.Payment, error) {
	row := r.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM payments WHERE id = $1 AND deleted_at IS NULL", selectPaymentCols),
		id)
	p, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.NotFound("payment not found: " + id.String())
		}
		return nil, apperror.Internal("scan payment", err)
	}
	return p, nil
}

func (r *PaymentRepo) FindByReference(ctx context.Context, referenceID, callerService string) (*payment.Payment, error) {
	row := r.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM payments WHERE reference_id = $1 AND caller_service = $2 AND deleted_at IS NULL", selectPaymentCols),
		referenceID, callerService)
	p, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.NotFound("payment not found for reference: " + referenceID)
		}
		return nil, apperror.Internal("scan payment", err)
	}
	return p, nil
}

func (r *PaymentRepo) FindByGatewayTransactionID(ctx context.Context, gatewayTxID string) (*payment.Payment, error) {
	row := r.pool.QueryRow(ctx,
		fmt.Sprintf("SELECT %s FROM payments WHERE gateway_transaction_id = $1 AND deleted_at IS NULL", selectPaymentCols),
		gatewayTxID)
	p, err := scanPayment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.NotFound("payment not found for gateway tx: " + gatewayTxID)
		}
		return nil, apperror.Internal("scan payment", err)
	}
	return p, nil
}

func (r *PaymentRepo) FindExpired(ctx context.Context, limit int) ([]*payment.Payment, error) {
	rows, err := r.pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM payments
			WHERE status IN ('PENDING','INITIATED')
			  AND expired_at < NOW()
			  AND deleted_at IS NULL
			LIMIT $1`, selectPaymentCols),
		limit)
	if err != nil {
		return nil, apperror.Internal("query expired payments", err)
	}
	defer rows.Close()

	payments, err := scanPayments(rows)
	if err != nil {
		return nil, apperror.Internal("scan expired payments", err)
	}
	return payments, nil
}

func (r *PaymentRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status payment.Status, _ map[string]any) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE payments SET status = $1, updated_at = NOW() WHERE id = $2`,
		status, id)
	if err != nil {
		return apperror.Internal("update payment status", err)
	}
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
