package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/postgres/db"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// AttemptRecorder writes payment_attempts rows. Each write is its own statement, outside
// any payment transaction, so a gateway call stays on record even if the save after it fails.
type AttemptRecorder struct {
	q *db.Queries
}

func NewAttemptRecorder(pool *pgxpool.Pool) *AttemptRecorder {
	return &AttemptRecorder{q: db.New(pool)}
}

// maxNumberRetries bounds retries when concurrent attempts pick the same attempt_number.
const maxNumberRetries = 3

func (r *AttemptRecorder) Record(ctx context.Context, a ports.Attempt) error {
	params := db.InsertPaymentAttemptParams{
		PaymentID:            a.PaymentID,
		Operation:            string(a.Operation),
		Status:               string(a.Outcome),
		GatewayTransactionID: toNullableString(a.GatewayTransactionID),
		ErrorCode:            toNullableString(a.ErrorCode),
		ErrorMessage:         pgtype.Text{String: a.ErrorMessage, Valid: a.ErrorMessage != ""},
		DurationMs:           a.Duration.Milliseconds(),
	}
	if a.RefundID != nil {
		params.RefundID = pgtype.UUID{Bytes: *a.RefundID, Valid: true}
	}
	if a.GatewayResponse != nil {
		b, err := json.Marshal(a.GatewayResponse)
		if err != nil {
			return apperror.Internal("marshal attempt gateway response", err)
		}
		params.GatewayResponse = b
	}

	var err error
	for range maxNumberRetries {
		if err = r.q.InsertPaymentAttempt(ctx, params); !isUniqueViolation(err) {
			break
		}
	}
	if err != nil {
		return apperror.Internal("insert payment attempt", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
