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
	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

type PaymentHandler struct {
	createHandler  *command.CreatePaymentHandler
	cancelHandler  *command.CancelPaymentHandler
	idem           *middleware.IdempotencyService
	getHandler     *query.GetPaymentHandler
	getByRefHandler *query.GetByRefHandler
	listHandler    *query.ListPaymentsHandler
}

func NewPaymentHandler(
	create *command.CreatePaymentHandler,
	cancel *command.CancelPaymentHandler,
	idem *middleware.IdempotencyService,
	get *query.GetPaymentHandler,
	getByRef *query.GetByRefHandler,
	list *query.ListPaymentsHandler,
) *PaymentHandler {
	return &PaymentHandler{
		createHandler:   create,
		cancelHandler:   cancel,
		idem:            idem,
		getHandler:      get,
		getByRefHandler: getByRef,
		listHandler:     list,
	}
}

func (h *PaymentHandler) CreatePayment(w http.ResponseWriter, r *http.Request) {
	var req CreatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
		return
	}

	callerService := r.Header.Get("X-Service-Name")
	idemKey := middleware.IdempotencyKey(callerService, req.ReferenceID)

	cached, isDuplicate, _ := h.idem.CheckOrAcquire(r.Context(), idemKey)
	if isDuplicate && cached != nil {
		w.Header().Set("X-Idempotent-Replay", "true")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(cached)
		return
	}

	result, err := h.createHandler.Handle(r.Context(), command.CreatePaymentCommand{
		ReferenceID:        req.ReferenceID,
		CallerService:      callerService,
		Amount:             req.Amount,
		Currency:           req.Currency,
		GatewayType:        string(req.GatewayType),
		PaymentMethodType:  string(req.PaymentMethodType),
		BankCode:           derefStr(req.BankCode),
		CustomerName:       req.CustomerName,
		CustomerEmail:      req.CustomerEmail,
		CustomerPhone:      derefStr(req.CustomerPhone),
		CustomerExternalID: derefStr(req.CustomerExternalID),
		Description:        derefStr(req.Description),
		ExpiredAt:          req.ExpiredAt,
		Metadata:           derefMap(req.Metadata),
	})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	resp := toCreatePaymentResponse(result)
	body, _ := json.Marshal(resp)
	_ = h.idem.StoreResponse(r.Context(), idemKey, result.Payment.ID, http.StatusCreated, body)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(body)
}

func toCreatePaymentResponse(result *command.CreatePaymentResult) CreatePaymentResponse {
	resp := CreatePaymentResponse{
		PaymentID:   result.Payment.ID.String(),
		ReferenceID: result.Payment.ReferenceID,
		Status:      CreatePaymentResponseStatus(result.Payment.Status),
		Amount:      result.Payment.Amount.Amount,
		Currency:    result.Payment.Amount.Currency,
		CreatedAt:   result.Payment.CreatedAt,
	}
	if result.PaymentInstructions != nil {
		m := map[string]any(result.PaymentInstructions)
		resp.PaymentInstructions = &m
	}
	return resp
}

func (h *PaymentHandler) CancelPayment(w http.ResponseWriter, r *http.Request) {
	paymentIDStr := r.PathValue("id")
	callerService := r.Header.Get("X-Service-Name")

	cmd := command.CancelPaymentCommand{CallerService: callerService}
	if err := cmd.PaymentID.Scan(paymentIDStr); err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
		return
	}

	if err := h.cancelHandler.Handle(r.Context(), cmd); err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(CancelPaymentResponse{
		PaymentID: cmd.PaymentID.String(),
		Status:    CancelPaymentResponseStatus(payment.StatusCancelled),
	})
}

func (h *PaymentHandler) GetPayment(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		middleware.WriteError(w, middleware.ErrInvalidJSON)
		return
	}

	dto, err := h.getHandler.Handle(r.Context(), query.GetPaymentQuery{ID: id})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto)
}

func (h *PaymentHandler) GetPaymentByRef(w http.ResponseWriter, r *http.Request) {
	refID := r.PathValue("reference_id")
	callerService := r.Header.Get("X-Service-Name")

	dto, err := h.getByRefHandler.Handle(r.Context(), query.GetByRefQuery{
		ReferenceID:   refID,
		CallerService: callerService,
	})
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(dto)
}

func (h *PaymentHandler) ListPayments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	lq := query.ListPaymentsQuery{
		Status:        q.Get("status"),
		CallerService: q.Get("caller_service"),
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

	result, err := h.listHandler.Handle(r.Context(), lq)
	if err != nil {
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefMap(m *map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	return *m
}
