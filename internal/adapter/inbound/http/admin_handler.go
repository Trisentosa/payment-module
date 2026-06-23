package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/query"
)

type AdminHandler struct {
	listPayments  *query.AdminListPaymentsHandler
	getAttempts   *query.GetAttemptsHandler
	getEvents     *query.GetEventsHandler
	getRefunds    *query.GetRefundsHandler
	outboxStats   *query.GetOutboxStatsHandler
	forceExpire   *command.ForceExpireHandler
}

func NewAdminHandler(
	listPayments *query.AdminListPaymentsHandler,
	getAttempts *query.GetAttemptsHandler,
	getEvents *query.GetEventsHandler,
	getRefunds *query.GetRefundsHandler,
	outboxStats *query.GetOutboxStatsHandler,
	forceExpire *command.ForceExpireHandler,
) *AdminHandler {
	return &AdminHandler{
		listPayments: listPayments,
		getAttempts:  getAttempts,
		getEvents:    getEvents,
		getRefunds:   getRefunds,
		outboxStats:  outboxStats,
		forceExpire:  forceExpire,
	}
}

func (h *AdminHandler) ListPayments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	lq := query.AdminListPaymentsQuery{
		Status:        q.Get("status"),
		CallerService: q.Get("caller_service"),
		GatewayType:   q.Get("gateway_type"),
		ReferenceID:   q.Get("reference_id"),
	}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			lq.From = &t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			lq.To = &t
		}
	}
	if v := q.Get("cursor"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			lq.Cursor = &t
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			lq.Limit = n
		}
	}

	result, err := h.listPayments.Handle(r.Context(), lq)
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	type meta struct {
		Count      int    `json:"count"`
		NextCursor string `json:"next_cursor,omitempty"`
		HasMore    bool   `json:"has_more"`
	}
	resp := struct {
		Data []*query.PaymentDTO `json:"data"`
		Meta meta                `json:"meta"`
	}{
		Data: result.Payments,
		Meta: meta{Count: len(result.Payments), HasMore: result.NextCursor != nil},
	}
	if result.NextCursor != nil {
		resp.Meta.NextCursor = result.NextCursor.UTC().Format(time.RFC3339)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *AdminHandler) GetPaymentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseAdminPaymentID(r)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
		return
	}

	// Reuse existing get handler via the admin list with an exact ID lookup.
	// Since we already have getPayment via the standard handler, here we delegate
	// to the attempts/events query to produce the full detail response.
	// For simplicity, return a combined view of attempts + events.
	attempts, err := h.getAttempts.Handle(r.Context(), query.GetAttemptsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}
	events, err := h.getEvents.Handle(r.Context(), query.GetEventsQuery{PaymentID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"payment_id": id.String(),
		"attempts":   attempts,
		"events":     events,
	})
}

func (h *AdminHandler) ListPaymentAttempts(w http.ResponseWriter, r *http.Request) {
	id, err := parseAdminPaymentID(r)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
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
		middleware.WriteError(w, middleware.ErrInvalidJSON)
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
		middleware.WriteError(w, middleware.ErrInvalidJSON)
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
		middleware.WriteError(w, middleware.ErrInvalidJSON)
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
		"pending":                     stats.Pending,
		"published":                   stats.Published,
		"failed":                      stats.Failed,
		"oldest_pending_age_seconds":  stats.OldestPendingAgeSecs,
	})
}

func parseAdminPaymentID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue("id"))
}
