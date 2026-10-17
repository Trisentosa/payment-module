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
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/application/query"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

func pendingQRIS(t *testing.T, ref string) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(50000, "IDR")
	p, err := payment.New(ref, "order-service", m, payment.GatewayMidtrans, payment.MethodQRIS, payment.CustomerInfo{}, "", nil, nil)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	_ = p.MarkPending("tx-"+ref, nil)
	return p
}

func TestWebhook_Responses(t *testing.T) {
	p := pendingQRIS(t, "WH-1")
	cases := []struct {
		name     string
		gw       *fake.Gateway
		wantCode int
		wantBody string
	}{
		{"settled", &fake.Gateway{Notification: &ports.WebhookNotification{PaymentID: p.ID.String(), GatewayTransactionID: "tx-WH-1", Status: ports.TransactionSettled}},
			http.StatusOK, `"received"`},
		{"bad signature", &fake.Gateway{ParseErr: apperror.InvalidInput("webhook signature mismatch")},
			http.StatusBadRequest, string(apperror.CodeInvalidInput)},
		// Midtrans retries non-2xx notifications, which is what we want for an unknown payment.
		{"unknown payment", &fake.Gateway{Notification: &ports.WebhookNotification{PaymentID: uuid.NewString(), GatewayTransactionID: "nope", Status: ports.TransactionSettled}},
			http.StatusNotFound, string(apperror.CodeNotFound)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := fake.NewPaymentRepo(p)
			h := httpAdapter.NewWebhookHandler(command.NewProcessCallbackHandler(repo, fake.GatewayFactory{"MIDTRANS": tc.gw}, fake.NewCache()))

			rec := httptest.NewRecorder()
			h.HandleMidtrans(rec, httptest.NewRequest(http.MethodPost, "/webhooks/midtrans", strings.NewReader(`{}`)))
			if rec.Code != tc.wantCode || !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("status %d body %s; want %d containing %s", rec.Code, rec.Body, tc.wantCode, tc.wantBody)
			}
		})
	}
}

// adminReader is an in-memory ports.AdminReader.
type adminReader struct {
	attempts []ports.AttemptRow
	stats    ports.OutboxStats
}

func (a *adminReader) GetPaymentAttempts(context.Context, uuid.UUID) ([]ports.AttemptRow, error) {
	return a.attempts, nil
}
func (a *adminReader) GetPaymentEvents(context.Context, uuid.UUID) ([]ports.EventRow, error) {
	return nil, nil
}
func (a *adminReader) GetRefunds(context.Context, uuid.UUID) ([]ports.RefundRow, error) {
	return nil, nil
}
func (a *adminReader) GetOutboxStats(context.Context) (*ports.OutboxStats, error) {
	return &a.stats, nil
}

const adminKey = "admin-test-key-0123456789-0123456789"

func newAdminAPI(t *testing.T, repo *fake.PaymentRepo, reader *adminReader) http.Handler {
	t.Helper()
	h := httpAdapter.NewAdminHandler(
		query.NewAdminListPaymentsHandler(repo),
		query.NewGetPaymentHandler(repo, fake.NewCache(), func(payment.Status) time.Duration { return time.Minute }),
		query.NewGetAttemptsHandler(reader),
		query.NewGetEventsHandler(reader),
		query.NewGetRefundsHandler(reader),
		query.NewGetOutboxStatsHandler(reader),
		command.NewForceExpireHandler(repo, fake.NewCache()),
	)
	mux := httpAdapter.AdminRoutes(h, &httpAdapter.RefundHandler{})
	return middleware.AdminAuth(adminKey)(mux)
}

func adminDo(api http.Handler, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Admin-Key", adminKey)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	return rec
}

func TestAdmin_ListFiltersAcrossCallers(t *testing.T) {
	a, b := pendingQRIS(t, "ADM-A"), pendingQRIS(t, "ADM-B")
	b.CallerService = "billing-service"
	api := newAdminAPI(t, fake.NewPaymentRepo(a, b), &adminReader{})

	var resp struct {
		Data []struct {
			ReferenceID string `json:"reference_id"`
		} `json:"data"`
		Meta struct {
			Count int `json:"count"`
		} `json:"meta"`
	}
	rec := adminDo(api, http.MethodGet, "/api/v1/admin/payments")
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || resp.Meta.Count != 2 {
		t.Errorf("all callers: status %d body %s", rec.Code, rec.Body)
	}

	rec = adminDo(api, http.MethodGet, "/api/v1/admin/payments?caller_service=billing-service")
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Meta.Count != 1 || resp.Data[0].ReferenceID != "ADM-B" {
		t.Errorf("filtered: %s", rec.Body)
	}
}

func TestAdmin_DetailIncludesThePaymentOfAnyCaller(t *testing.T) {
	p := pendingQRIS(t, "ADM-D")
	p.CallerService = "billing-service"
	api := newAdminAPI(t, fake.NewPaymentRepo(p), &adminReader{})

	rec := adminDo(api, http.MethodGet, "/api/v1/admin/payments/"+p.ID.String())
	var resp struct {
		Payment struct {
			ReferenceID string `json:"reference_id"`
		} `json:"payment"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || resp.Payment.ReferenceID != "ADM-D" {
		t.Errorf("detail: status %d body %s", rec.Code, rec.Body)
	}

	if rec := adminDo(api, http.MethodGet, "/api/v1/admin/payments/"+uuid.NewString()); rec.Code != http.StatusNotFound {
		t.Errorf("unknown payment: status %d, want 404", rec.Code)
	}
}

func TestAdmin_AttemptsShowOperationAndRefund(t *testing.T) {
	refundID := uuid.New()
	reader := &adminReader{attempts: []ports.AttemptRow{
		{ID: uuid.New(), Operation: "REFUND", RefundID: &refundID, AttemptNumber: 3, Status: "FAILED",
			ErrorCode: "DECLINED", ErrorMessage: "midtrans 412", DurationMs: 120, CreatedAt: time.Now()},
	}}
	api := newAdminAPI(t, fake.NewPaymentRepo(), reader)

	rec := adminDo(api, http.MethodGet, "/api/v1/admin/payments/"+uuid.NewString()+"/attempts")
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || len(resp.Data) != 1 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	got := resp.Data[0]
	if got["operation"] != "REFUND" || got["refund_id"] != refundID.String() || got["error_message"] != "midtrans 412" {
		t.Errorf("attempt: %v", got)
	}
}

func TestAdmin_ForceExpire(t *testing.T) {
	pending := pendingQRIS(t, "ADM-E")
	done := pendingQRIS(t, "ADM-D")
	_ = done.MarkCompleted(time.Now())
	api := newAdminAPI(t, fake.NewPaymentRepo(pending, done), &adminReader{})

	if rec := adminDo(api, http.MethodPost, "/api/v1/admin/payments/"+pending.ID.String()+"/expire"); rec.Code != http.StatusOK {
		t.Errorf("pending: status %d body %s", rec.Code, rec.Body)
	}
	if rec := adminDo(api, http.MethodPost, "/api/v1/admin/payments/"+done.ID.String()+"/expire"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("completed: status %d, want 422", rec.Code)
	}
	if rec := adminDo(api, http.MethodPost, "/api/v1/admin/payments/not-a-uuid/expire"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: status %d, want 400", rec.Code)
	}
}

func TestAdmin_OutboxStats(t *testing.T) {
	api := newAdminAPI(t, fake.NewPaymentRepo(), &adminReader{stats: ports.OutboxStats{Pending: 4, Published: 10, Failed: 1, OldestPendingAgeSecs: 95}})

	rec := adminDo(api, http.MethodGet, "/api/v1/admin/outbox/stats")
	var got map[string]int
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["pending"] != 4 || got["published"] != 10 || got["failed"] != 1 || got["oldest_pending_age_seconds"] != 95 {
		t.Errorf("stats: %s", rec.Body)
	}
}

func TestWriteError_MapsCodesWithoutLeakingInternals(t *testing.T) {
	cases := []struct {
		err  error
		code int
		body string
	}{
		{apperror.NotFound("payment not found"), http.StatusNotFound, "payment not found"},
		{apperror.InvalidState("cannot cancel"), http.StatusUnprocessableEntity, "INVALID_STATE"},
		{apperror.Conflict("changed"), http.StatusConflict, "CONFLICT"},
		{apperror.Unauthorized("no key"), http.StatusUnauthorized, "UNAUTHORIZED"},
		{apperror.Forbidden("mismatch"), http.StatusForbidden, "FORBIDDEN"},
		{apperror.GatewayError("midtrans down", nil), http.StatusBadGateway, "GATEWAY_ERROR"},
		{apperror.Internal("insert payment", errors.New("pq: password=hunter2")), http.StatusInternalServerError, "INTERNAL_ERROR"},
		{errors.New("raw driver error with secrets"), http.StatusInternalServerError, "internal server error"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		middleware.WriteError(rec, tc.err)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%v: status %d body %s; want %d containing %q", tc.err, rec.Code, rec.Body, tc.code, tc.body)
		}
		if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "secrets") {
			t.Errorf("%v: error cause leaked to the client: %s", tc.err, rec.Body)
		}
	}
}
