package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type WebhookHandler struct {
	callbackHandler *command.ProcessCallbackHandler
}

func NewWebhookHandler(cb *command.ProcessCallbackHandler) *WebhookHandler {
	return &WebhookHandler{callbackHandler: cb}
}

func (h *WebhookHandler) HandleMidtrans(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	var n map[string]string
	_ = json.Unmarshal(payload, &n)

	err = h.callbackHandler.Handle(r.Context(), command.ProcessCallbackCommand{
		GatewayType: "MIDTRANS",
		RawPayload:  payload,
		Signature:   n["signature_key"],
	})
	if err != nil {
		if apperror.IsCode(err, apperror.CodeInvalidInput) {
			http.Error(w, "invalid signature", http.StatusBadRequest)
			return
		}
		middleware.WriteError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "received"})
}
