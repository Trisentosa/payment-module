package query

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type ListPaymentsQuery struct {
	Status        string
	CallerService string
	From          *time.Time
	To            *time.Time
	Cursor        string // next_cursor of the previous page
	Limit         int
}

type ListPaymentsHandler struct {
	repo payment.Repository
}

func NewListPaymentsHandler(repo payment.Repository) *ListPaymentsHandler {
	return &ListPaymentsHandler{repo: repo}
}

func (h *ListPaymentsHandler) Handle(ctx context.Context, q ListPaymentsQuery) (*ListPaymentsResult, error) {
	// An empty caller would list every service's payments.
	if q.CallerService == "" {
		return nil, apperror.InvalidInput("caller_service is required")
	}
	return listPage(ctx, h.repo, payment.ListFilter{
		Status:        q.Status,
		CallerService: q.CallerService,
		From:          q.From,
		To:            q.To,
	}, q.Cursor, clampLimit(q.Limit, 50))
}

// maxPageSize caps list pages. Larger requests get the maximum, not the default.
const maxPageSize = 100

func clampLimit(requested, def int) int {
	switch {
	case requested <= 0:
		return def
	case requested > maxPageSize:
		return maxPageSize
	default:
		return requested
	}
}
