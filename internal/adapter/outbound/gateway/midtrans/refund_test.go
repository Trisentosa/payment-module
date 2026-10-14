package midtrans_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/gateway/midtrans"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

const refundKey = "0b9a6c62-6f5c-4f0e-8a51-6a7e2b1d3c44"

func refundServer(t *testing.T, httpStatus int, reply string) (*midtrans.Adapter, *map[string]any) {
	t.Helper()
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/tx-cc/refund" {
			t.Errorf("path: got %s, want /v2/tx-cc/refund", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(httpStatus)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return midtrans.NewAdapter(midtrans.Config{ServerKey: serverKey, BaseURL: srv.URL, Timeout: 5 * time.Second}), &sent
}

func refundReq() ports.RefundRequest {
	return ports.RefundRequest{GatewayTransactionID: "tx-cc", RefundKey: refundKey, Amount: 40000, Reason: "damaged"}
}

func TestCreateRefund_Accepted(t *testing.T) {
	// Sample from Midtrans's refund reference: refund_chargeback_id is a number.
	a, sent := refundServer(t, http.StatusOK, `{"status_code":"200","status_message":"Success, refund request is approved",
		"transaction_id":"tx-cc","transaction_status":"partial_refund","gross_amount":"100000.00",
		"refund_chargeback_id":1,"refund_amount":"40000.00","refund_key":"`+refundKey+`"}`)

	res, err := a.CreateRefund(context.Background(), refundReq())
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	if !res.Accepted || res.GatewayRefundID != "1" {
		t.Errorf("result: %+v", res)
	}
	if (*sent)["refund_key"] != refundKey || (*sent)["amount"] != float64(40000) || (*sent)["reason"] != "damaged" {
		t.Errorf("request body: %v", *sent)
	}
}

func TestCreateRefund_Outcomes(t *testing.T) {
	cases := []struct {
		name       string
		httpStatus int
		reply      string
		declined   bool // a definite "no": the refund failed and no money moved
		unknown    bool // no definite answer: keep the refund pending
	}{
		{"status can't be modified", http.StatusOK, `{"status_code":"412","status_message":"Merchant cannot modify the status of the transaction"}`, true, false},
		{"invalid amount", http.StatusOK, `{"status_code":"414","status_message":"Refund request is rejected due to invalid amount"}`, true, false},
		{"bad server key", http.StatusUnauthorized, `{"status_code":"401","status_message":"Transaction cannot be authorized"}`, true, false},
		{"duplicate refund key", http.StatusOK, `{"status_code":"406","status_message":"Duplicate refund ID"}`, false, true},
		{"midtrans outage", http.StatusInternalServerError, `{"status_code":"500","status_message":"internal error"}`, false, true},
		{"unparseable reply", http.StatusBadGateway, `<html>bad gateway</html>`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := refundServer(t, tc.httpStatus, tc.reply)
			res, err := a.CreateRefund(context.Background(), refundReq())
			switch {
			case tc.unknown:
				if !apperror.IsCode(err, apperror.CodeGatewayError) {
					t.Fatalf("expected GATEWAY_ERROR (unknown outcome), got res=%+v err=%v", res, err)
				}
			case tc.declined:
				if err != nil || res.Accepted || res.DeclineReason == "" {
					t.Fatalf("expected a decline with a reason, got res=%+v err=%v", res, err)
				}
			}
		})
	}
}

func TestParseWebhook_RefundNotification(t *testing.T) {
	// Shape from Midtrans's refund notification sample: chargeback ids are numbers,
	// amounts are decimal strings.
	n := map[string]any{
		"transaction_id":     "tx-cc",
		"order_id":           orderID,
		"status_code":        "200",
		"gross_amount":       "100000.00",
		"transaction_status": "partial_refund",
		"fraud_status":       "accept",
		"refund_amount":      "40000.00",
		"refunds": []map[string]any{{
			"refund_chargeback_id": 1, "refund_amount": "40000.00", "created_at": "2026-10-15 09:00:00",
			"reason": "damaged", "refund_key": refundKey, "refund_method": "online", "bank_confirmed_at": "2026-10-15 09:05:00",
		}},
	}
	n["signature_key"] = sign(orderID, "200", "100000.00", serverKey)
	payload, _ := json.Marshal(n)

	got, err := midtrans.NewAdapter(midtrans.Config{ServerKey: serverKey}).ParseWebhook(payload, nil)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if got.Status != ports.TransactionUnknown {
		t.Errorf("status: got %s; a refund must not read as a payment settlement or failure", got.Status)
	}
	if len(got.Refunds) != 1 {
		t.Fatalf("refunds: got %d, want 1", len(got.Refunds))
	}
	want := ports.RefundNotification{RefundKey: refundKey, GatewayRefundID: "1", Amount: 40000}
	if got.Refunds[0] != want {
		t.Errorf("refund: got %+v, want %+v", got.Refunds[0], want)
	}
}
