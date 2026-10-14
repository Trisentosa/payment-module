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
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// fakeMidtrans answers /v2/charge with reply and records the charge body it received.
func fakeMidtrans(t *testing.T, httpStatus int, reply string) (*midtrans.Adapter, *map[string]any) {
	t.Helper()
	var charge map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/charge" {
			t.Errorf("path: got %s, want /v2/charge", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&charge); err != nil {
			t.Errorf("decode charge body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(httpStatus)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return midtrans.NewAdapter(midtrans.Config{ServerKey: serverKey, BaseURL: srv.URL, Timeout: 5 * time.Second}), &charge
}

func chargeReq(method payment.Method) ports.CreateTransactionRequest {
	return ports.CreateTransactionRequest{
		PaymentID:     orderID,
		ReferenceID:   "ORDER-101",
		Amount:        150000,
		Currency:      "IDR",
		PaymentMethod: method,
		CustomerName:  "Alice",
		CustomerEmail: "alice@example.com",
	}
}

// object returns body[key] as a JSON object, failing the test if it isn't one.
func object(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	m, ok := body[key].(map[string]any)
	if !ok {
		t.Fatalf("charge body %q: got %v, want an object", key, body[key])
	}
	return m
}

func TestCreateTransaction_CommonFields(t *testing.T) {
	a, charge := fakeMidtrans(t, http.StatusOK, `{"status_code":"201","transaction_id":"tx-1","transaction_status":"pending",
		"va_numbers":[{"bank":"bca","va_number":"12345678901"}]}`)
	req := chargeReq(payment.MethodVirtualAccount)
	req.BankCode = "BCA"

	if _, err := a.CreateTransaction(context.Background(), req); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	details := object(t, *charge, "transaction_details")
	if details["order_id"] != orderID {
		t.Errorf("order_id: got %v, want payment id %s", details["order_id"], orderID)
	}
	if (*charge)["custom_field1"] != "ORDER-101" {
		t.Errorf("custom_field1: got %v, want caller reference ORDER-101", (*charge)["custom_field1"])
	}
}

func TestCreateTransaction_PerMethod(t *testing.T) {
	expiry := time.Date(2026, 10, 15, 3, 0, 0, 0, time.UTC) // 10:00 WIB

	cases := []struct {
		name       string
		req        func() ports.CreateTransactionRequest
		reply      string
		wantType   string // payment_type sent to Midtrans
		checkBody  func(t *testing.T, body map[string]any)
		wantStatus ports.TransactionStatus
		want       ports.NextAction
	}{
		{
			name: "VA BCA",
			req: func() ports.CreateTransactionRequest {
				r := chargeReq(payment.MethodVirtualAccount)
				r.BankCode = "BCA"
				return r
			},
			reply: `{"status_code":"201","transaction_id":"tx-va","transaction_status":"pending",
				"va_numbers":[{"bank":"bca","va_number":"12345678901"}],"expiry_time":"2026-10-15 10:00:00"}`,
			wantType: "bank_transfer",
			checkBody: func(t *testing.T, body map[string]any) {
				if object(t, body, "bank_transfer")["bank"] != "bca" {
					t.Errorf("bank_transfer.bank: got %v", body["bank_transfer"])
				}
			},
			wantStatus: ports.TransactionPending,
			want:       ports.NextAction{Type: ports.NextActionDisplayVA, VANumber: "12345678901", Bank: "bca", ExpiresAt: &expiry},
		},
		{
			name: "VA Permata uses its own response field",
			req: func() ports.CreateTransactionRequest {
				r := chargeReq(payment.MethodVirtualAccount)
				r.BankCode = "permata"
				return r
			},
			reply:      `{"status_code":"201","transaction_id":"tx-pm","transaction_status":"pending","permata_va_number":"8562000087926752"}`,
			wantType:   "bank_transfer",
			wantStatus: ports.TransactionPending,
			want:       ports.NextAction{Type: ports.NextActionDisplayVA, VANumber: "8562000087926752", Bank: "permata"},
		},
		{
			name: "QRIS",
			req:  func() ports.CreateTransactionRequest { return chargeReq(payment.MethodQRIS) },
			reply: `{"status_code":"201","transaction_id":"tx-qr","transaction_status":"pending","acquirer":"gopay",
				"qr_string":"00020101021226620014COM.GO-JEK.WWW","actions":[
				{"name":"generate-qr-code","method":"GET","url":"https://api.sandbox.midtrans.com/v2/qris/tx-qr/qr-code"}]}`,
			wantType: "qris",
			checkBody: func(t *testing.T, body map[string]any) {
				if object(t, body, "qris")["acquirer"] != "gopay" {
					t.Errorf("qris.acquirer: got %v", body["qris"])
				}
			},
			wantStatus: ports.TransactionPending,
			want: ports.NextAction{Type: ports.NextActionDisplayQR, QRString: "00020101021226620014COM.GO-JEK.WWW",
				QRImageURL: "https://api.sandbox.midtrans.com/v2/qris/tx-qr/qr-code"},
		},
		{
			name: "GoPay deeplink with QR for desktop",
			req: func() ports.CreateTransactionRequest {
				r := chargeReq(payment.MethodGoPay)
				r.ReturnURL = "https://shop.example.com/orders/101"
				return r
			},
			reply: `{"status_code":"201","transaction_id":"tx-gp","transaction_status":"pending","actions":[
				{"name":"generate-qr-code","method":"GET","url":"https://api.sandbox.midtrans.com/v2/gopay/tx-gp/qr-code"},
				{"name":"deeplink-redirect","method":"GET","url":"https://simulator.sandbox.midtrans.com/gopay/ui/checkout?tref=tx-gp"},
				{"name":"get-status","method":"GET","url":"https://api.sandbox.midtrans.com/v2/tx-gp/status"},
				{"name":"cancel","method":"POST","url":"https://api.sandbox.midtrans.com/v2/tx-gp/cancel"}]}`,
			wantType: "gopay",
			checkBody: func(t *testing.T, body map[string]any) {
				gp := object(t, body, "gopay")
				if gp["enable_callback"] != true || gp["callback_url"] != "https://shop.example.com/orders/101" {
					t.Errorf("gopay: got %v", gp)
				}
			},
			wantStatus: ports.TransactionPending,
			want: ports.NextAction{Type: ports.NextActionRedirect,
				RedirectURL: "https://simulator.sandbox.midtrans.com/gopay/ui/checkout?tref=tx-gp",
				QRImageURL:  "https://api.sandbox.midtrans.com/v2/gopay/tx-gp/qr-code"},
		},
		{
			name: "ShopeePay",
			req: func() ports.CreateTransactionRequest {
				r := chargeReq(payment.MethodShopeePay)
				r.ReturnURL = "myshop://orders/101"
				return r
			},
			reply: `{"status_code":"201","transaction_id":"tx-sp","transaction_status":"pending","actions":[
				{"name":"deeplink-redirect","method":"GET","url":"https://wsa.uat.wallet.airpay.co.id/universal-link/wallet/pay?ref=abc"}]}`,
			wantType: "shopeepay",
			checkBody: func(t *testing.T, body map[string]any) {
				if object(t, body, "shopeepay")["callback_url"] != "myshop://orders/101" {
					t.Errorf("shopeepay: got %v", body["shopeepay"])
				}
			},
			wantStatus: ports.TransactionPending,
			want: ports.NextAction{Type: ports.NextActionRedirect,
				RedirectURL: "https://wsa.uat.wallet.airpay.co.id/universal-link/wallet/pay?ref=abc"},
		},
		{
			name: "Card needing 3DS",
			req: func() ports.CreateTransactionRequest {
				r := chargeReq(payment.MethodCard)
				r.CardToken = "481111-1114-tok"
				return r
			},
			reply: `{"status_code":"201","transaction_id":"tx-cc","transaction_status":"pending","fraud_status":"accept",
				"redirect_url":"https://api.sandbox.midtrans.com/v2/token/rba/redirect/481111-1114-tx-cc"}`,
			wantType: "credit_card",
			checkBody: func(t *testing.T, body map[string]any) {
				cc := object(t, body, "credit_card")
				if cc["token_id"] != "481111-1114-tok" || cc["authentication"] != true {
					t.Errorf("credit_card: got %v", cc)
				}
			},
			wantStatus: ports.TransactionPending,
			want: ports.NextAction{Type: ports.NextActionRedirect,
				RedirectURL: "https://api.sandbox.midtrans.com/v2/token/rba/redirect/481111-1114-tx-cc"},
		},
		{
			name: "Card captured at once",
			req: func() ports.CreateTransactionRequest {
				r := chargeReq(payment.MethodCard)
				r.CardToken = "tok"
				return r
			},
			reply:      `{"status_code":"200","transaction_id":"tx-cap","transaction_status":"capture","fraud_status":"accept"}`,
			wantType:   "credit_card",
			wantStatus: ports.TransactionSettled,
			want:       ports.NextAction{Type: ports.NextActionNone},
		},
		{
			name: "Card declined",
			req: func() ports.CreateTransactionRequest {
				r := chargeReq(payment.MethodCard)
				r.CardToken = "tok"
				return r
			},
			reply:      `{"status_code":"202","status_message":"Card declined","transaction_id":"tx-deny","transaction_status":"deny"}`,
			wantType:   "credit_card",
			wantStatus: ports.TransactionFailed,
			want:       ports.NextAction{Type: ports.NextActionNone},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, charge := fakeMidtrans(t, http.StatusOK, tc.reply)

			resp, err := a.CreateTransaction(context.Background(), tc.req())
			if err != nil {
				t.Fatalf("CreateTransaction: %v", err)
			}

			if (*charge)["payment_type"] != tc.wantType {
				t.Errorf("payment_type: got %v, want %s", (*charge)["payment_type"], tc.wantType)
			}
			if tc.checkBody != nil {
				tc.checkBody(t, *charge)
			}
			if resp.Status != tc.wantStatus {
				t.Errorf("status: got %s, want %s", resp.Status, tc.wantStatus)
			}
			got := resp.NextAction
			if got.Type != tc.want.Type || got.VANumber != tc.want.VANumber || got.Bank != tc.want.Bank ||
				got.QRString != tc.want.QRString || got.QRImageURL != tc.want.QRImageURL || got.RedirectURL != tc.want.RedirectURL {
				t.Errorf("next action:\n got  %+v\n want %+v", got, tc.want)
			}
			if (got.ExpiresAt == nil) != (tc.want.ExpiresAt == nil) ||
				(got.ExpiresAt != nil && !got.ExpiresAt.Equal(*tc.want.ExpiresAt)) {
				t.Errorf("expires_at: got %v, want %v", got.ExpiresAt, tc.want.ExpiresAt)
			}
		})
	}
}

func TestCreateTransaction_SendsCustomExpiry(t *testing.T) {
	a, charge := fakeMidtrans(t, http.StatusOK, `{"status_code":"201","transaction_id":"tx","transaction_status":"pending"}`)
	req := chargeReq(payment.MethodQRIS)
	expiresAt := time.Now().Add(90 * time.Minute)
	req.ExpiredAt = &expiresAt

	if _, err := a.CreateTransaction(context.Background(), req); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	ce := object(t, *charge, "custom_expiry")
	if ce["unit"] != "minute" || ce["expiry_duration"] != float64(90) {
		t.Errorf("custom_expiry: got %v, want 90 minutes", ce)
	}
	if _, err := time.Parse("2006-01-02 15:04:05 -0700", ce["order_time"].(string)); err != nil {
		t.Errorf("order_time %q not in Midtrans format: %v", ce["order_time"], err)
	}
}

func TestCreateTransaction_InvalidInputNeverReachesMidtrans(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	cases := map[string]ports.CreateTransactionRequest{
		"VA without bank": chargeReq(payment.MethodVirtualAccount),
		"VA with Mandiri": func() ports.CreateTransactionRequest {
			r := chargeReq(payment.MethodVirtualAccount)
			r.BankCode = "MANDIRI"
			return r
		}(),
		"card without token":   chargeReq(payment.MethodCard),
		"ShopeePay w/o return": chargeReq(payment.MethodShopeePay),
		"expiry in the past": func() ports.CreateTransactionRequest {
			r := chargeReq(payment.MethodQRIS)
			r.ExpiredAt = &past
			return r
		}(),
		"method Midtrans lacks": chargeReq(payment.Method("OVO")),
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
			defer srv.Close()
			a := midtrans.NewAdapter(midtrans.Config{ServerKey: serverKey, BaseURL: srv.URL, Timeout: time.Second})

			_, err := a.CreateTransaction(context.Background(), req)
			if !apperror.IsCode(err, apperror.CodeInvalidInput) {
				t.Fatalf("expected INVALID_INPUT, got %v", err)
			}
			if called {
				t.Error("request reached Midtrans")
			}
		})
	}
}

func TestCreateTransaction_ErrorClassification(t *testing.T) {
	cases := []struct {
		name       string
		httpStatus int
		reply      string
		want       string
	}{
		{"validation error in body", http.StatusOK,
			`{"status_code":"400","status_message":"One or more parameters in the payload is invalid.","validation_messages":["gross_amount must be > 0"]}`,
			string(apperror.CodeInvalidInput)},
		{"duplicate order id", http.StatusOK, `{"status_code":"406","status_message":"The request could not be completed due to a conflict"}`,
			string(apperror.CodeGatewayError)},
		{"midtrans outage", http.StatusInternalServerError, `{"status_code":"500","status_message":"Sorry, we encountered internal server error"}`,
			string(apperror.CodeGatewayError)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := fakeMidtrans(t, tc.httpStatus, tc.reply)
			req := chargeReq(payment.MethodQRIS)
			_, err := a.CreateTransaction(context.Background(), req)
			if !apperror.IsCode(err, apperror.Code(tc.want)) {
				t.Fatalf("expected %s, got %v", tc.want, err)
			}
		})
	}
}

func TestCapability_FollowsMidtransRefundRules(t *testing.T) {
	a := midtrans.NewAdapter(midtrans.Config{})
	cases := map[payment.Method]ports.MethodCapability{
		payment.MethodVirtualAccount: {Charge: true},
		payment.MethodQRIS:           {Charge: true, Refund: true, PartialRefund: true},
		payment.MethodGoPay:          {Charge: true, Refund: true, PartialRefund: true},
		payment.MethodShopeePay:      {Charge: true, Refund: true},
		payment.MethodCard:           {Charge: true, Refund: true, PartialRefund: true},
		payment.Method("OVO"):        {},
	}
	for method, want := range cases {
		if got := a.Capability(method); got != want {
			t.Errorf("%s: got %+v, want %+v", method, got, want)
		}
	}
}
