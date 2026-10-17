package query

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// ListPaymentsResult is one page of payments, newest first. NextCursor is empty on the last page.
type ListPaymentsResult struct {
	Payments   []*PaymentDTO
	NextCursor string
}

// listPage fetches one page for f. The cursor is opaque to callers: it encodes the
// last row's (created_at, id), so rows sharing a timestamp are neither skipped nor repeated.
func listPage(ctx context.Context, repo payment.Repository, f payment.ListFilter, cursor string, limit int) (*ListPaymentsResult, error) {
	after, err := decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	f.After = after
	f.Limit = limit + 1 // one extra row tells whether a next page exists

	payments, err := repo.ListPaginated(ctx, f)
	if err != nil {
		return nil, err
	}

	res := &ListPaymentsResult{}
	if len(payments) > limit {
		payments = payments[:limit]
		last := payments[limit-1]
		res.NextCursor = encodeCursor(payment.PageKey{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	res.Payments = make([]*PaymentDTO, 0, len(payments))
	for _, p := range payments {
		res.Payments = append(res.Payments, toDTO(p))
	}
	return res, nil
}

func encodeCursor(k payment.PageKey) string {
	raw := k.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + k.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (*payment.PageKey, error) {
	if s == "" {
		return nil, nil
	}
	invalid := apperror.InvalidInput("cursor is invalid; pass next_cursor from the previous page unchanged")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, invalid
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil, invalid
	}
	createdAt, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return nil, invalid
	}
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil, invalid
	}
	return &payment.PageKey{CreatedAt: createdAt, ID: parsedID}, nil
}
