package query

import (
	"context"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type ListRefundsQuery struct {
	PaymentID     uuid.UUID
	CallerService string
}

type ListRefundsHandler struct {
	repo payment.Repository
}

func NewListRefundsHandler(repo payment.Repository) *ListRefundsHandler {
	return &ListRefundsHandler{repo: repo}
}

// Handle returns the payment with its refunds loaded, if the caller owns it.
func (h *ListRefundsHandler) Handle(ctx context.Context, q ListRefundsQuery) (*payment.Payment, error) {
	p, err := h.repo.FindByID(ctx, q.PaymentID)
	if err != nil {
		return nil, err
	}
	if p.CallerService != q.CallerService {
		return nil, apperror.NotFound("payment not found: " + q.PaymentID.String())
	}
	return p, nil
}
