package http_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpAdapter "github.com/Trisentosa/payment-module/internal/adapter/inbound/http"
	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

const createBody = `{
	"reference_id": "ORDER-77",
	"amount": 150000,
	"currency": "IDR",
	"gateway_type": "MIDTRANS",
	"payment_method_type": "VIRTUAL_ACCOUNT",
	"bank_code": "BCA",
	"customer_name": "Alice",
	"customer_email": "alice@example.com"
}`

type harness struct {
	handler *httpAdapter.PaymentHandler
	gateway *fake.Gateway
	cache   *fake.Cache
}

func newHarness() *harness {
	gw := &fake.Gateway{}
	c := fake.NewCache()
	create := command.NewCreatePaymentHandler(fake.NewPaymentRepo(), fake.GatewayFactory{"MIDTRANS": gw}, nil)
	h := httpAdapter.NewPaymentHandler(create, nil, middleware.NewIdempotencyService(c, time.Hour), nil, nil, nil)
	return &harness{handler: h, gateway: gw, cache: c}
}

func (h *harness) post(body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(body))
	req.Header.Set("X-Service-Name", "order-service")
	rec := httptest.NewRecorder()
	h.handler.CreatePayment(rec, req)
	return rec
}

func TestCreatePayment_RetryReplaysOriginalResponse(t *testing.T) {
	h := newHarness()

	first := h.post(createBody)
	if first.Code != http.StatusCreated {
		t.Fatalf("first: status %d, body %s", first.Code, first.Body)
	}

	retry := h.post(createBody)
	if retry.Code != http.StatusCreated {
		t.Fatalf("retry: status %d, body %s", retry.Code, retry.Body)
	}
	if retry.Header().Get("X-Idempotent-Replay") != "true" {
		t.Error("retry not marked as replay")
	}
	if retry.Body.String() != first.Body.String() {
		t.Errorf("replayed body differs:\nfirst: %s\nretry: %s", first.Body, retry.Body)
	}
	if h.gateway.CreateCalls != 1 {
		t.Errorf("gateway called %d times, want 1", h.gateway.CreateCalls)
	}
}

func TestCreatePayment_SameReferenceDifferentPayloadConflicts(t *testing.T) {
	h := newHarness()

	if rec := h.post(createBody); rec.Code != http.StatusCreated {
		t.Fatalf("first: status %d, body %s", rec.Code, rec.Body)
	}
	rec := h.post(strings.Replace(createBody, "150000", "999000", 1))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), string(apperror.CodeIdempotencyConflict)) {
		t.Fatalf("status %d body %s, want 409 IDEMPOTENCY_CONFLICT", rec.Code, rec.Body)
	}
	if h.gateway.CreateCalls != 1 {
		t.Errorf("gateway called %d times, want 1", h.gateway.CreateCalls)
	}
}

func TestCreatePayment_RetryAfterGatewayFailureReportsExistingPayment(t *testing.T) {
	h := newHarness()
	h.gateway.CreateErr = apperror.GatewayError("midtrans charge failed", errors.New("timeout"))

	if rec := h.post(createBody); rec.Code != http.StatusBadGateway {
		t.Fatalf("first: status %d, body %s, want 502", rec.Code, rec.Body)
	}

	h.gateway.CreateErr = nil
	rec := h.post(createBody)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), string(apperror.CodeAlreadyExists)) {
		t.Fatalf("retry: status %d body %s, want 409 ALREADY_EXISTS", rec.Code, rec.Body)
	}
	if h.gateway.CreateCalls != 1 {
		t.Errorf("gateway called %d times, want 1", h.gateway.CreateCalls)
	}
}

func TestCreatePayment_CacheOutageStillBlocksDuplicates(t *testing.T) {
	h := newHarness()
	h.cache.Err = errors.New("redis: connection refused")

	if rec := h.post(createBody); rec.Code != http.StatusCreated {
		t.Fatalf("first: status %d, body %s", rec.Code, rec.Body)
	}
	rec := h.post(createBody)
	if rec.Code != http.StatusConflict {
		t.Fatalf("retry: status %d body %s, want 409", rec.Code, rec.Body)
	}
	if h.gateway.CreateCalls != 1 {
		t.Errorf("gateway called %d times, want 1", h.gateway.CreateCalls)
	}
}

func TestCreatePayment_ResponseCarriesNextAction(t *testing.T) {
	h := newHarness()

	rec := h.post(createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var resp struct {
		NextAction struct {
			Type     string `json:"type"`
			VANumber string `json:"va_number"`
			Bank     string `json:"bank"`
		} `json:"next_action"`
		PaymentInstructions map[string]any `json:"payment_instructions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.NextAction.Type != "DISPLAY_VA" || resp.NextAction.VANumber != "1234567890" || resp.NextAction.Bank != "bca" {
		t.Errorf("next_action: got %+v", resp.NextAction)
	}
	// Older VA callers still read the deprecated map.
	if resp.PaymentInstructions["va_number"] != "1234567890" {
		t.Errorf("payment_instructions.va_number: got %v", resp.PaymentInstructions["va_number"])
	}
}
