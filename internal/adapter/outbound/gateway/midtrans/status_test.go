package midtrans_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/gateway/midtrans"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

func statusServer(t *testing.T, wantPath string, httpStatus int, reply string) *midtrans.Adapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != wantPath {
			t.Errorf("request: %s %s, want GET %s", r.Method, r.URL.Path, wantPath)
		}
		w.WriteHeader(httpStatus)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return midtrans.NewAdapter(midtrans.Config{ServerKey: serverKey, BaseURL: srv.URL, Timeout: 5 * time.Second})
}

func TestGetTransaction_PartialRefundListsRefunds(t *testing.T) {
	// Shape from Midtrans's Get Status reference for a partial_refund transaction.
	a := statusServer(t, "/v2/tx-cc/status", http.StatusOK, `{"status_code":"200","transaction_id":"tx-cc",
		"order_id":"`+orderID+`","transaction_status":"partial_refund","fraud_status":"accept","refund_amount":"12000.00",
		"refunds":[{"refund_chargeback_id":1,"refund_amount":"5000.00","refund_key":"reference1"}]}`)

	st, err := a.GetTransaction(context.Background(), ports.TransactionRef{PaymentID: orderID, GatewayTransactionID: "tx-cc"})
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if !st.Found || st.GatewayTransactionID != "tx-cc" || st.GatewayStatus != "partial_refund" {
		t.Errorf("state: %+v", st)
	}
	want := ports.RefundNotification{RefundKey: "reference1", GatewayRefundID: "1", Amount: 5000}
	if len(st.Refunds) != 1 || st.Refunds[0] != want {
		t.Errorf("refunds: %+v, want [%+v]", st.Refunds, want)
	}
}

func TestGetTransaction_ByOrderIDWhenNoTransactionID(t *testing.T) {
	a := statusServer(t, "/v2/"+orderID+"/status", http.StatusOK,
		`{"status_code":"200","transaction_id":"tx-new","transaction_status":"capture","fraud_status":"accept"}`)

	st, err := a.GetTransaction(context.Background(), ports.TransactionRef{PaymentID: orderID})
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if st.Status != ports.TransactionSettled || st.GatewayTransactionID != "tx-new" {
		t.Errorf("state: %+v", st)
	}
}

func TestGetTransaction_NotFound(t *testing.T) {
	// The docs show status_code "404" in the body without saying which HTTP status comes with it.
	for name, httpStatus := range map[string]int{"HTTP 200": http.StatusOK, "HTTP 404": http.StatusNotFound} {
		t.Run(name, func(t *testing.T) {
			a := statusServer(t, "/v2/"+orderID+"/status", httpStatus, `{"status_code":"404","status_message":"Transaction doesn't exist."}`)
			st, err := a.GetTransaction(context.Background(), ports.TransactionRef{PaymentID: orderID})
			if err != nil || st.Found {
				t.Fatalf("state=%+v err=%v, want Found=false", st, err)
			}
		})
	}
}

func TestUnknownOutcomeClassification(t *testing.T) {
	ctx := context.Background()

	t.Run("charge 5xx is unknown", func(t *testing.T) {
		a, _ := fakeMidtrans(t, http.StatusServiceUnavailable, `{"status_code":"503","status_message":"unavailable"}`)
		_, err := a.CreateTransaction(ctx, chargeReq(payment.MethodQRIS))
		if !errors.Is(err, ports.ErrUnknownOutcome) {
			t.Errorf("got %v, want an unknown outcome", err)
		}
	})
	t.Run("charge 4xx is a definite failure", func(t *testing.T) {
		a, _ := fakeMidtrans(t, http.StatusOK, `{"status_code":"406","status_message":"duplicate order id"}`)
		_, err := a.CreateTransaction(ctx, chargeReq(payment.MethodQRIS))
		if !apperror.IsCode(err, apperror.CodeGatewayError) || errors.Is(err, ports.ErrUnknownOutcome) {
			t.Errorf("got %v, want a definite GATEWAY_ERROR", err)
		}
	})
	t.Run("unreachable gateway is unknown", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.Close() // connection refused
		a := midtrans.NewAdapter(midtrans.Config{ServerKey: serverKey, BaseURL: srv.URL, Timeout: time.Second})
		for name, err := range map[string]error{
			"charge": func() error { _, err := a.CreateTransaction(ctx, chargeReq(payment.MethodQRIS)); return err }(),
			"status": func() error { _, err := a.GetTransaction(ctx, ports.TransactionRef{PaymentID: orderID}); return err }(),
			"cancel": a.CancelTransaction(ctx, "tx"),
		} {
			if !errors.Is(err, ports.ErrUnknownOutcome) {
				t.Errorf("%s: got %v, want an unknown outcome", name, err)
			}
		}
	})
}
