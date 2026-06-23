package midtrans

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type Config struct {
	ServerKey string
	BaseURL   string
	Timeout   time.Duration
}

type Adapter struct {
	cfg    Config
	client *http.Client
}

func NewAdapter(cfg Config) *Adapter {
	return &Adapter{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (a *Adapter) CreateTransaction(ctx context.Context, req ports.CreateTransactionRequest) (*ports.GatewayResponse, error) {
	log := logger.FromContext(ctx)

	body := a.buildChargeRequest(req)
	respBody, err := a.post(ctx, "/v2/charge", body)
	if err != nil {
		return nil, apperror.GatewayError("midtrans charge failed", err)
	}

	txID, ok := respBody["transaction_id"].(string)
	if !ok {
		return nil, apperror.GatewayError("missing transaction_id in midtrans response", nil)
	}

	log.Info("midtrans transaction created", "gateway_tx_id", txID, "payment_id", req.PaymentID)

	return &ports.GatewayResponse{
		GatewayTransactionID: txID,
		PaymentInstructions:  extractInstructions(respBody),
		RawResponse:          respBody,
	}, nil
}

func (a *Adapter) VerifyWebhookSignature(payload []byte, signature string) error {
	var n map[string]string
	if err := json.Unmarshal(payload, &n); err != nil {
		return apperror.InvalidInput("invalid webhook payload")
	}
	raw := n["order_id"] + n["status_code"] + n["gross_amount"] + a.cfg.ServerKey
	expected := fmt.Sprintf("%x", sha512.Sum512([]byte(raw)))
	if expected != signature {
		return apperror.InvalidInput("webhook signature mismatch")
	}
	return nil
}

func (a *Adapter) CancelTransaction(ctx context.Context, gatewayTxID string) error {
	_, err := a.post(ctx, fmt.Sprintf("/v2/%s/cancel", gatewayTxID), nil)
	if err != nil {
		return apperror.GatewayError("midtrans cancel failed", err)
	}
	return nil
}

func (a *Adapter) GetTransactionStatus(ctx context.Context, gatewayTxID string) (string, error) {
	resp, err := a.get(ctx, fmt.Sprintf("/v2/%s/status", gatewayTxID))
	if err != nil {
		return "", apperror.GatewayError("midtrans status check failed", err)
	}
	status, _ := resp["transaction_status"].(string)
	return status, nil
}

func (a *Adapter) CreateRefund(ctx context.Context, gatewayTxID string, amount int64, reason string) (string, error) {
	body := map[string]any{"amount": amount, "reason": reason}
	resp, err := a.post(ctx, fmt.Sprintf("/v2/%s/refund", gatewayTxID), body)
	if err != nil {
		return "", apperror.GatewayError("midtrans refund failed", err)
	}
	refundID, _ := resp["refund_chargeback_id"].(string)
	return refundID, nil
}

func (a *Adapter) post(ctx context.Context, path string, body map[string]any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.cfg.ServerKey, "")
	req.Header.Set("Content-Type", "application/json")

	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var out map[string]any
	if err = json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("midtrans HTTP %d: %v", res.StatusCode, out)
	}
	return out, nil
}

func (a *Adapter) get(ctx context.Context, path string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(a.cfg.ServerKey, "")
	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out, nil
}

func (a *Adapter) buildChargeRequest(req ports.CreateTransactionRequest) map[string]any {
	return map[string]any{
		"payment_type": "bank_transfer",
		"transaction_details": map[string]any{
			"order_id":     req.ReferenceID,
			"gross_amount": req.Amount,
		},
		"bank_transfer": map[string]any{"bank": strings.ToLower(req.BankCode)},
		"customer_details": map[string]any{
			"first_name": req.CustomerName,
			"email":      req.CustomerEmail,
			"phone":      req.CustomerPhone,
		},
	}
}

func extractInstructions(resp map[string]any) map[string]any {
	instr := map[string]any{}
	if vaNumbers, ok := resp["va_numbers"].([]any); ok && len(vaNumbers) > 0 {
		if va, ok := vaNumbers[0].(map[string]any); ok {
			instr["va_number"] = va["va_number"]
			instr["bank"] = va["bank"]
		}
	}
	instr["expiry_time"] = resp["expiry_time"]
	return instr
}
