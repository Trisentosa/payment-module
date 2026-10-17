package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpAdapter "github.com/Trisentosa/payment-module/internal/adapter/inbound/http"
	"github.com/Trisentosa/payment-module/internal/adapter/inbound/http/middleware"
	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/query"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

type refundAPI struct {
	mux http.Handler
	gw  *fake.Gateway
	id  string
}

func newRefundAPI(t *testing.T) *refundAPI {
	t.Helper()
	m, _ := payment.NewMoney(100000, "IDR")
	p, err := payment.New("ORDER-H", "order-service", m, payment.GatewayMidtrans, payment.MethodQRIS,
		payment.CustomerInfo{}, "", nil, nil)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	_ = p.MarkPending("gw-tx-h", nil)
	_ = p.MarkCompleted(time.Now())

	repo := fake.NewPaymentRepo(p)
	gw := &fake.Gateway{}
	h := httpAdapter.NewRefundHandler(
		command.NewRefundPaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, fake.NewCache(), &fake.AttemptRecorder{}),
		query.NewListRefundsHandler(repo),
	)
	mux := httpAdapter.CallerRoutes(&httpAdapter.PaymentHandler{}, h)
	return &refundAPI{mux: middleware.CallerAuth(callerKeys)(mux), gw: gw, id: p.ID.String()}
}

// do calls the refunds endpoint as caller ("order-service" or "billing-service").
func (a *refundAPI) do(method, caller, body string) *httptest.ResponseRecorder {
	key := map[string]string{"order-service": orderKey, "billing-service": billingKey}[caller]
	req := httptest.NewRequest(method, "/payments/"+a.id+"/refunds", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	a.mux.ServeHTTP(rec, req)
	return rec
}

type refundBody struct {
	Refund struct {
		RefundID string `json:"refund_id"`
		Status   string `json:"status"`
		Amount   int64  `json:"amount"`
	} `json:"refund"`
	Payment struct {
		Status           string `json:"status"`
		RefundedAmount   int64  `json:"refunded_amount"`
		RefundableAmount int64  `json:"refundable_amount"`
	} `json:"payment"`
}

func decodeRefund(t *testing.T, rec *httptest.ResponseRecorder) refundBody {
	t.Helper()
	var b refundBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return b
}

func TestRefundAPI_CreateThenReplay(t *testing.T) {
	a := newRefundAPI(t)

	rec := a.do(http.MethodPost, "order-service", `{"reference_id":"rf-1","amount":30000,"reason":"damaged"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d body %s, want 201", rec.Code, rec.Body)
	}
	b := decodeRefund(t, rec)
	if b.Refund.Status != "SUCCEEDED" || b.Refund.Amount != 30000 {
		t.Errorf("refund: %+v", b.Refund)
	}
	if b.Payment.Status != "PARTIALLY_REFUNDED" || b.Payment.RefundedAmount != 30000 || b.Payment.RefundableAmount != 70000 {
		t.Errorf("payment summary: %+v", b.Payment)
	}

	replay := a.do(http.MethodPost, "order-service", `{"reference_id":"rf-1","amount":30000}`)
	if replay.Code != http.StatusOK || replay.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("replay: status %d replay header %q", replay.Code, replay.Header().Get("X-Idempotent-Replay"))
	}
	if decodeRefund(t, replay).Refund.RefundID != b.Refund.RefundID {
		t.Error("replay returned a different refund")
	}
}

func TestRefundAPI_UnknownOutcomeIs202(t *testing.T) {
	a := newRefundAPI(t)
	a.gw.RefundErr = apperror.GatewayError("timeout", nil)

	rec := a.do(http.MethodPost, "order-service", `{"reference_id":"rf-1"}`)
	if rec.Code != http.StatusAccepted || decodeRefund(t, rec).Refund.Status != "PENDING" {
		t.Fatalf("status %d body %s, want 202 PENDING", rec.Code, rec.Body)
	}
}

func TestRefundAPI_ListIsScopedToTheOwner(t *testing.T) {
	a := newRefundAPI(t)
	if rec := a.do(http.MethodPost, "order-service", `{"reference_id":"rf-1","amount":1000}`); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	rec := a.do(http.MethodGet, "order-service", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner list: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Data []struct {
			ReferenceID string `json:"reference_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Data) != 1 || list.Data[0].ReferenceID != "rf-1" {
		t.Errorf("list: %s", rec.Body)
	}

	if rec := a.do(http.MethodGet, "billing-service", ""); rec.Code != http.StatusNotFound {
		t.Errorf("other caller list: %d, want 404", rec.Code)
	}
	if rec := a.do(http.MethodPost, "billing-service", `{"reference_id":"rf-2"}`); rec.Code != http.StatusNotFound {
		t.Errorf("other caller refund: %d, want 404", rec.Code)
	}
}
