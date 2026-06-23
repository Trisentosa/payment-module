package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

type CreatePaymentRequest struct {
	ReferenceID        string         `json:"reference_id"`
	Amount             int64          `json:"amount"`
	Currency           string         `json:"currency"`
	GatewayType        string         `json:"gateway_type"`
	PaymentMethodType  string         `json:"payment_method_type"`
	BankCode           string         `json:"bank_code"`
	CustomerName       string         `json:"customer_name"`
	CustomerEmail      string         `json:"customer_email"`
	CustomerPhone      string         `json:"customer_phone"`
	CustomerExternalID string         `json:"customer_external_id"`
	Description        string         `json:"description"`
	ExpiredAt          *time.Time     `json:"expired_at"`
	Metadata           map[string]any `json:"metadata"`
}

type CreatePaymentResponse struct {
	PaymentID           string         `json:"payment_id"`
	ReferenceID         string         `json:"reference_id"`
	Status              string         `json:"status"`
	Amount              int64          `json:"amount"`
	Currency            string         `json:"currency"`
	PaymentInstructions map[string]any `json:"payment_instructions,omitempty"`
	CreatedAt           time.Time      `json:"created_at"`
}

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
		GatewayType:        req.GatewayType,
		PaymentMethodType:  req.PaymentMethodType,
		BankCode:           req.BankCode,
		CustomerName:       req.CustomerName,
		CustomerEmail:      req.CustomerEmail,
		CustomerPhone:      req.CustomerPhone,
		CustomerExternalID: req.CustomerExternalID,
		Description:        req.Description,
		ExpiredAt:          req.ExpiredAt,
		Metadata:           req.Metadata,
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
	return CreatePaymentResponse{
		PaymentID:           result.Payment.ID.String(),
		ReferenceID:         result.Payment.ReferenceID,
		Status:              string(result.Payment.Status),
		Amount:              result.Payment.Amount.Amount,
		Currency:            result.Payment.Amount.Currency,
		PaymentInstructions: result.PaymentInstructions,
		CreatedAt:           result.Payment.CreatedAt,
	}
}

type CancelPaymentResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
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
		Status:    string(payment.StatusCancelled),
	})
}
