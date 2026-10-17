package http

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/query"
)

type AdminHandler struct {
	listPayments *query.AdminListPaymentsHandler
	getPayment   *query.GetPaymentHandler
	getAttempts  *query.GetAttemptsHandler
	getEvents    *query.GetEventsHandler
	getRefunds   *query.GetRefundsHandler
	outboxStats  *query.GetOutboxStatsHandler
	forceExpire  *command.ForceExpireHandler
}

func NewAdminHandler(
	listPayments *query.AdminListPaymentsHandler,
	getPayment *query.GetPaymentHandler,
	getAttempts *query.GetAttemptsHandler,
	getEvents *query.GetEventsHandler,
	getRefunds *query.GetRefundsHandler,
	outboxStats *query.GetOutboxStatsHandler,
	forceExpire *command.ForceExpireHandler,
) *AdminHandler {
	return &AdminHandler{
		listPayments: listPayments,
		getPayment:   getPayment,
		getAttempts:  getAttempts,
		getEvents:    getEvents,
		getRefunds:   getRefunds,
		outboxStats:  outboxStats,
		forceExpire:  forceExpire,
	}
}

func (h *AdminHandler) ListPayments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	params, err := parseListParams(q)
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	result, err := h.listPayments.Handle(r.Context(), query.AdminListPaymentsQuery{
		Status:        q.Get("status"),
		CallerService: q.Get("caller_service"),
		GatewayType:   q.Get("gateway_type"),
		ReferenceID:   q.Get("reference_id"),
		From:          params.From,
		To:            params.To,
		Cursor:        params.Cursor,
		Limit:         params.Limit,
	})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}
	writePaymentList(w, result)
}

func (h *AdminHandler) GetPaymentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseAdminPaymentID(r)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidPaymentID)
		return
	}

	ctx := r.Context()
	p, err := h.getPayment.Handle(ctx, query.GetPaymentQuery{ID: id, Admin: true})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}
	attempts, err := h.getAttempts.Handle(ctx, query.GetAttemptsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}
	events, err := h.getEvents.Handle(ctx, query.GetEventsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}
	refunds, err := h.getRefunds.Handle(ctx, query.GetRefundsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"payment_id": id.String(),
		"payment":    p,
		"attempts":   attempts,
		"events":     events,
		"refunds":    refunds,
	})
}

func (h *AdminHandler) ListPaymentAttempts(w http.ResponseWriter, r *http.Request) {
	id, err := parseAdminPaymentID(r)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidPaymentID)
		return
	}

	dtos, err := h.getAttempts.Handle(r.Context(), query.GetAttemptsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": dtos})
}

func (h *AdminHandler) ListPaymentEvents(w http.ResponseWriter, r *http.Request) {
	id, err := parseAdminPaymentID(r)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidPaymentID)
		return
	}

	events, err := h.getEvents.Handle(r.Context(), query.GetEventsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": events})
}

func (h *AdminHandler) ListRefunds(w http.ResponseWriter, r *http.Request) {
	id, err := parseAdminPaymentID(r)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidPaymentID)
		return
	}

	refunds, err := h.getRefunds.Handle(r.Context(), query.GetRefundsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": refunds})
}

func (h *AdminHandler) ForceExpire(w http.ResponseWriter, r *http.Request) {
	id, err := parseAdminPaymentID(r)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidPaymentID)
		return
	}

	if err = h.forceExpire.Handle(r.Context(), command.ForceExpireCommand{PaymentID: id}); err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"payment_id": id.String(), "status": "EXPIRED"})
}

func (h *AdminHandler) OutboxStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.outboxStats.Handle(r.Context())
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pending":                    stats.Pending,
		"published":                  stats.Published,
		"failed":                     stats.Failed,
		"oldest_pending_age_seconds": stats.OldestPendingAgeSecs,
	})
}

func parseAdminPaymentID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue("id"))
}
