package query

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

type PaymentDTO struct {
	ID                   string         `json:"id"`
	ReferenceID          string         `json:"reference_id"`
	CallerService        string         `json:"caller_service"`
	Status               string         `json:"status"`
	GatewayType          string         `json:"gateway"`
	GatewayTransactionID string         `json:"gateway_transaction_id,omitempty"`
	Amount               int64          `json:"amount"`
	Currency             string         `json:"currency"`
	PaymentMethodType    string         `json:"payment_method_type"`
	CustomerInfo         map[string]any `json:"customer"`
	Metadata             map[string]any `json:"metadata,omitempty"`
	Description          string         `json:"description,omitempty"`
	ExpiredAt            *time.Time     `json:"expired_at,omitempty"`
	PaidAt               *time.Time     `json:"paid_at,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
}

type GetPaymentQuery struct{ ID uuid.UUID }

type GetPaymentHandler struct {
	repo     payment.Repository
	cache    ports.CachePort
	ttlForFn func(payment.Status) time.Duration
}

func NewGetPaymentHandler(repo payment.Repository, cache ports.CachePort, ttlFn func(payment.Status) time.Duration) *GetPaymentHandler {
	return &GetPaymentHandler{repo: repo, cache: cache, ttlForFn: ttlFn}
}

func (h *GetPaymentHandler) Handle(ctx context.Context, q GetPaymentQuery) (*PaymentDTO, error) {
	cacheKey := "payment:" + q.ID.String()

	raw, err := h.cache.Get(ctx, cacheKey)
	if err == nil {
		var dto PaymentDTO
		if jsonErr := json.Unmarshal([]byte(raw), &dto); jsonErr == nil {
			return &dto, nil
		}
	}

	p, err := h.repo.FindByID(ctx, q.ID)
	if err != nil {
		return nil, err
	}

	dto := toDTO(p)

	if b, marshalErr := json.Marshal(dto); marshalErr == nil {
		_ = h.cache.Set(ctx, cacheKey, string(b), h.ttlForFn(p.Status))
	}

	return dto, nil
}

func toDTO(p *payment.Payment) *PaymentDTO {
	customerMap := map[string]any{
		"external_id": p.CustomerInfo.ExternalID,
		"name":        p.CustomerInfo.Name,
		"email":       p.CustomerInfo.Email,
		"phone":       p.CustomerInfo.Phone,
	}
	return &PaymentDTO{
		ID:                   p.ID.String(),
		ReferenceID:          p.ReferenceID,
		CallerService:        p.CallerService,
		Status:               string(p.Status),
		GatewayType:          string(p.GatewayType),
		GatewayTransactionID: p.GatewayTransactionID,
		Amount:               p.Amount.Amount,
		Currency:             p.Amount.Currency,
		PaymentMethodType:    p.PaymentMethodType,
		CustomerInfo:         customerMap,
		Metadata:             p.Metadata,
		Description:          p.Description,
		ExpiredAt:            p.ExpiredAt,
		PaidAt:               p.PaidAt,
		CreatedAt:            p.CreatedAt,
		UpdatedAt:            p.UpdatedAt,
	}
}

