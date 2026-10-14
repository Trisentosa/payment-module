package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	httpAdapter "github.com/Trisentosa/payment-module/internal/adapter/inbound/http"
	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/query"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apikey"
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

const (
	orderKey   = "pgk_order-service-test-key"
	billingKey = "pgk_billing-service-test-key"
)

// callerKeys mirrors CALLER_API_KEYS for the two test services.
var callerKeys = map[string]string{
	apikey.Hash(orderKey):   "order-service",
	apikey.Hash(billingKey): "billing-service",
}

// harness serves the caller API the way cmd/server wires it: real handlers on fakes,
// behind CallerAuth.
type harness struct {
	mux     http.Handler
	repo    *fake.PaymentRepo
	gateway *fake.Gateway
	cache   *fake.Cache
}

func newHarness() *harness {
	gw := &fake.Gateway{}
	c := fake.NewCache()
	repo := fake.NewPaymentRepo()
	factory := fake.GatewayFactory{"MIDTRANS": gw}

	get := query.NewGetPaymentHandler(repo, c, func(payment.Status) time.Duration { return time.Minute })
	h := httpAdapter.NewPaymentHandler(
		command.NewCreatePaymentHandler(repo, factory, nil),
		command.NewCancelPaymentHandler(repo, factory, c, &fake.AttemptRecorder{}),
		middleware.NewIdempotencyService(c, time.Hour),
		get,
		query.NewGetByRefHandler(repo, c, get),
		query.NewListPaymentsHandler(repo),
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /payments", h.CreatePayment)
	mux.HandleFunc("DELETE /payments/{id}", h.CancelPayment)
	mux.HandleFunc("GET /payments", h.ListPayments)
	mux.HandleFunc("GET /payments/{id}", h.GetPayment)
	mux.HandleFunc("GET /payments/ref/{reference_id}", h.GetPaymentByRef)
	return &harness{mux: middleware.CallerAuth(callerKeys)(mux), repo: repo, gateway: gw, cache: c}
}

func (h *harness) do(method, path, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// post creates a payment as order-service.
func (h *harness) post(body string) *httptest.ResponseRecorder {
	return h.do(http.MethodPost, "/payments", orderKey, body)
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

// createdID creates a payment as order-service and returns its ID.
func createdID(t *testing.T, h *harness) string {
	t.Helper()
	rec := h.post(createBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body)
	}
	var resp struct {
		PaymentID string `json:"payment_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.PaymentID
}

func TestCallerAPI_RequiresAKey(t *testing.T) {
	h := newHarness()
	req := httptest.NewRequest(http.MethodPost, "/payments", strings.NewReader(createBody))
	req.Header.Set("X-Service-Name", "order-service") // used to be enough on its own
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if h.gateway.CreateCalls != 0 {
		t.Error("unauthenticated request reached the gateway")
	}
}

func TestCallerAPI_PaymentBelongsToTheKeysService(t *testing.T) {
	h := newHarness()
	id := createdID(t, h)

	p, err := h.repo.FindByID(context.Background(), uuid.MustParse(id))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if p.CallerService != "order-service" {
		t.Errorf("caller_service: got %q, want the key's service", p.CallerService)
	}
}

func TestCallerAPI_OtherServicesCantSeeOrCancel(t *testing.T) {
	h := newHarness()
	id := createdID(t, h)

	for name, req := range map[string][2]string{
		"get":          {http.MethodGet, "/payments/" + id},
		"by reference": {http.MethodGet, "/payments/ref/ORDER-77"},
		"cancel":       {http.MethodDelete, "/payments/" + id},
	} {
		if rec := h.do(req[0], req[1], billingKey, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s as billing-service: status %d body %s, want 404", name, rec.Code, rec.Body)
		}
	}

	// The owner still can.
	if rec := h.do(http.MethodGet, "/payments/"+id, orderKey, ""); rec.Code != http.StatusOK {
		t.Errorf("get as owner: status %d", rec.Code)
	}
	if rec := h.do(http.MethodDelete, "/payments/"+id, orderKey, ""); rec.Code != http.StatusOK {
		t.Errorf("cancel as owner: status %d body %s", rec.Code, rec.Body)
	}
}

func TestCallerAPI_ListIgnoresCallerServiceParameter(t *testing.T) {
	h := newHarness()

	// Asking for order-service's payments as billing-service must still be scoped to billing.
	rec := h.do(http.MethodGet, "/payments?caller_service=order-service", billingKey, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d body %s", rec.Code, rec.Body)
	}
	if got := h.repo.LastListFilter().CallerService; got != "billing-service" {
		t.Errorf("list filter caller: got %q, want billing-service", got)
	}
}
