package http

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/query"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

type RefundHandler struct {
	refund *command.RefundPaymentHandler
	list   *query.ListRefundsHandler
}

func NewRefundHandler(refund *command.RefundPaymentHandler, list *query.ListRefundsHandler) *RefundHandler {
	return &RefundHandler{refund: refund, list: list}
}

// CreateRefund handles POST /payments/{id}/refunds for the calling service.
func (h *RefundHandler) CreateRefund(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, false)
}

// AdminCreateRefund handles POST /api/v1/admin/payments/{id}/refunds without the ownership check.
func (h *RefundHandler) AdminCreateRefund(w http.ResponseWriter, r *http.Request) {
	h.create(w, r, true)
}

func (h *RefundHandler) create(w http.ResponseWriter, r *http.Request, admin bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
		return
	}
	var req CreateRefundRequest
	if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
		return
	}

	cmd := command.RefundPaymentCommand{
		PaymentID:     id,
		ReferenceID:   req.ReferenceID,
		Reason:        derefStr(req.Reason),
		CallerService: r.Header.Get("X-Service-Name"),
		Admin:         admin,
	}
	if req.Amount != nil {
		cmd.Amount = *req.Amount
	}

	result, err := h.refund.Handle(r.Context(), cmd)
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	status := http.StatusCreated
	switch {
	case result.Replayed:
		status = http.StatusOK
		w.Header().Set("X-Idempotent-Replay", "true")
	case result.Refund.Status == payment.RefundStatusPending:
		status = http.StatusAccepted
	}
	writeJSON(w, status, RefundResponse{Refund: toRefund(result.Refund), Payment: toRefundPaymentSummary(result.Payment)})
}

// ListRefunds handles GET /payments/{id}/refunds for the calling service.
func (h *RefundHandler) ListRefunds(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
		return
	}

	p, err := h.list.Handle(r.Context(), query.ListRefundsQuery{PaymentID: id, CallerService: r.Header.Get("X-Service-Name")})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	data := make([]Refund, 0, len(p.Refunds))
	for _, rf := range p.Refunds {
		data = append(data, toRefund(rf))
	}
	writeJSON(w, http.StatusOK, RefundListResponse{Payment: toRefundPaymentSummary(p), Data: data})
}

func toRefund(rf *payment.Refund) Refund {
	return Refund{
		RefundID:        rf.ID.String(),
		ReferenceID:     rf.ReferenceID,
		Amount:          rf.Amount,
		Status:          RefundStatus(rf.Status),
		Reason:          optStr(rf.Reason),
		FailureReason:   optStr(rf.FailureReason),
		GatewayRefundID: optStr(rf.GatewayRefundID),
		CreatedAt:       rf.CreatedAt,
		CompletedAt:     rf.CompletedAt,
	}
}

func toRefundPaymentSummary(p *payment.Payment) RefundPaymentSummary {
	return RefundPaymentSummary{
		PaymentID:        p.ID.String(),
		Status:           string(p.Status),
		Amount:           p.Amount.Amount,
		RefundedAmount:   p.RefundedAmount(),
		RefundableAmount: p.RefundableAmount(),
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
