package query

import (
	"context"
	"encoding/json"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type GetByRefQuery struct {
	ReferenceID   string
	CallerService string
}

type GetByRefHandler struct {
	repo  payment.Repository
	cache ports.CachePort
	get   *GetPaymentHandler
}

func NewGetByRefHandler(repo payment.Repository, cache ports.CachePort, get *GetPaymentHandler) *GetByRefHandler {
	return &GetByRefHandler{repo: repo, cache: cache, get: get}
}

func (h *GetByRefHandler) Handle(ctx context.Context, q GetByRefQuery) (*PaymentDTO, error) {
	if q.ReferenceID == "" {
		return nil, apperror.InvalidInput("reference_id is required")
	}
	if q.CallerService == "" {
		return nil, apperror.InvalidInput("caller_service is required")
	}

	// Check the reference → UUID cache first.
	refKey := "reference:" + q.CallerService + ":" + q.ReferenceID
	if cached, err := h.cache.Get(ctx, refKey); err == nil {
		var dto PaymentDTO
		if jsonErr := json.Unmarshal([]byte(cached), &dto); jsonErr == nil {
			return &dto, nil
		}
	}

	p, err := h.repo.FindByReference(ctx, q.ReferenceID, q.CallerService)
	if err != nil {
		return nil, err
	}

	dto := toDTO(p)

	if b, marshalErr := json.Marshal(dto); marshalErr == nil {
		_ = h.cache.Set(ctx, refKey, string(b), h.get.ttlForFn(p.Status))
	}

	return dto, nil
}
