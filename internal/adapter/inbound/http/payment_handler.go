package http

import (
	"encoding/json"
	"net/http"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

type PaymentHandler struct {
	createHandler *command.CreatePaymentHandler
	cancelHandler *command.CancelPaymentHandler
	idem          *middleware.IdempotencyService
}

func NewPaymentHandler(
	create *command.CreatePaymentHandler,
	cancel *command.CancelPaymentHandler,
	idem *middleware.IdempotencyService,
) *PaymentHandler {
	return &PaymentHandler{createHandler: create, cancelHandler: cancel, idem: idem}
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
