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
	Cursor        *time.Time
	Limit         int
}

type ListPaymentsResult struct {
	Payments   []*PaymentDTO
	NextCursor *time.Time
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
	limit := clampLimit(q.Limit, 50)

	payments, err := h.repo.ListPaginated(ctx, payment.ListFilter{
		Status:        q.Status,
		CallerService: q.CallerService,
		From:          q.From,
		To:            q.To,
		Cursor:        q.Cursor,
		Limit:         limit + 1, // fetch one extra to determine if there's a next page
	})
	if err != nil {
		return nil, err
	}

	var nextCursor *time.Time
	if len(payments) > limit {
		payments = payments[:limit]
		t := payments[limit-1].CreatedAt
		nextCursor = &t
	}

	dtos := make([]*PaymentDTO, 0, len(payments))
	for _, p := range payments {
		dtos = append(dtos, toDTO(p))
	}

	return &ListPaymentsResult{Payments: dtos, NextCursor: nextCursor}, nil
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
