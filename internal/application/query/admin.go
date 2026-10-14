package query

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/google/uuid"
)

// ------- list admin payments -------

type AdminListPaymentsQuery struct {
	Status        string
	CallerService string
	GatewayType   string
	ReferenceID   string
	From          *time.Time
	To            *time.Time
	Cursor        string // next_cursor of the previous page
	Limit         int
}

type AdminListPaymentsHandler struct {
	repo payment.Repository
}

func NewAdminListPaymentsHandler(repo payment.Repository) *AdminListPaymentsHandler {
	return &AdminListPaymentsHandler{repo: repo}
}

func (h *AdminListPaymentsHandler) Handle(ctx context.Context, q AdminListPaymentsQuery) (*ListPaymentsResult, error) {
	return listPage(ctx, h.repo, payment.ListFilter{
		Status:        q.Status,
		CallerService: q.CallerService,
		GatewayType:   q.GatewayType,
		ReferenceID:   q.ReferenceID,
		From:          q.From,
		To:            q.To,
	}, q.Cursor, clampLimit(q.Limit, 20))
}

// ------- get attempts -------

type PaymentAttemptDTO struct {
	ID                   string `json:"id"`
	Operation            string `json:"operation"`
	RefundID             string `json:"refund_id,omitempty"`
	AttemptNumber        int32  `json:"attempt_number"`
	Status               string `json:"status"`
	ErrorCode            string `json:"error_code,omitempty"`
	ErrorMessage         string `json:"error_message,omitempty"`
	GatewayTransactionID string `json:"gateway_transaction_id,omitempty"`
	DurationMs           int64  `json:"duration_ms,omitempty"`
	CreatedAt            string `json:"created_at"`
}

type GetAttemptsQuery struct{ PaymentID uuid.UUID }

type GetAttemptsHandler struct{ repo ports.AdminReader }

func NewGetAttemptsHandler(repo ports.AdminReader) *GetAttemptsHandler {
	return &GetAttemptsHandler{repo: repo}
}

func (h *GetAttemptsHandler) Handle(ctx context.Context, q GetAttemptsQuery) ([]PaymentAttemptDTO, error) {
	rows, err := h.repo.GetPaymentAttempts(ctx, q.PaymentID)
	if err != nil {
		return nil, err
	}
	dtos := make([]PaymentAttemptDTO, 0, len(rows))
	for _, r := range rows {
		dto := PaymentAttemptDTO{
			ID:                   r.ID.String(),
			Operation:            r.Operation,
			AttemptNumber:        r.AttemptNumber,
			Status:               r.Status,
			ErrorCode:            r.ErrorCode,
			ErrorMessage:         r.ErrorMessage,
			GatewayTransactionID: r.GatewayTransactionID,
			DurationMs:           r.DurationMs,
			CreatedAt:            r.CreatedAt.UTC().Format(time.RFC3339),
		}
		if r.RefundID != nil {
			dto.RefundID = r.RefundID.String()
		}
		dtos = append(dtos, dto)
	}
	return dtos, nil
}

// ------- get events -------

type GetEventsQuery struct{ PaymentID uuid.UUID }

type GetEventsHandler struct{ repo ports.AdminReader }

func NewGetEventsHandler(repo ports.AdminReader) *GetEventsHandler {
	return &GetEventsHandler{repo: repo}
}

func (h *GetEventsHandler) Handle(ctx context.Context, q GetEventsQuery) ([]map[string]any, error) {
	rows, err := h.repo.GetPaymentEvents(ctx, q.PaymentID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id":              r.ID.String(),
			"event_type":      r.EventType,
			"sequence_number": r.SequenceNumber,
			"payload":         r.Payload,
			"created_at":      r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// ------- get refunds -------

type RefundDTO struct {
	ID              string `json:"id"`
	ReferenceID     string `json:"reference_id"`
	GatewayRefundID string `json:"gateway_refund_id,omitempty"`
	Status          string `json:"status"`
	Amount          int64  `json:"amount"`
	Reason          string `json:"reason,omitempty"`
	CreatedAt       string `json:"created_at"`
}

type GetRefundsQuery struct{ PaymentID uuid.UUID }

type GetRefundsHandler struct{ repo ports.AdminReader }

func NewGetRefundsHandler(repo ports.AdminReader) *GetRefundsHandler {
	return &GetRefundsHandler{repo: repo}
}

func (h *GetRefundsHandler) Handle(ctx context.Context, q GetRefundsQuery) ([]RefundDTO, error) {
	rows, err := h.repo.GetRefunds(ctx, q.PaymentID)
	if err != nil {
		return nil, err
	}
	dtos := make([]RefundDTO, 0, len(rows))
	for _, r := range rows {
		dtos = append(dtos, RefundDTO{
			ID:              r.ID.String(),
			ReferenceID:     r.ReferenceID,
			GatewayRefundID: r.GatewayRefundID,
			Status:          r.Status,
			Amount:          r.Amount,
			Reason:          r.Reason,
			CreatedAt:       r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return dtos, nil
}

// ------- outbox stats -------

type GetOutboxStatsHandler struct{ repo ports.AdminReader }

func NewGetOutboxStatsHandler(repo ports.AdminReader) *GetOutboxStatsHandler {
	return &GetOutboxStatsHandler{repo: repo}
}

func (h *GetOutboxStatsHandler) Handle(ctx context.Context) (*ports.OutboxStats, error) {
	return h.repo.GetOutboxStats(ctx)
}
