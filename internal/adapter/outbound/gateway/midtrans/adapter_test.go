package midtrans_test

import (
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/Trisentosa/payment-module/internal/adapter/outbound/gateway/midtrans"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

const (
	serverKey = "SB-Mid-server-test"
	orderID   = "5f0c2a3e-8a51-4c37-9a7e-2f1d6b0c9e11" // PayGate payment ID sent as order_id
)

func sign(orderID, statusCode, grossAmount, key string) string {
	sum := sha512.Sum512([]byte(orderID + statusCode + grossAmount + key))
	return hex.EncodeToString(sum[:])
}

// vaNotification mirrors a real Midtrans bank-transfer notification, including the
// array fields (va_numbers, payment_amounts) that a map[string]string can't hold.
func vaNotification(t *testing.T, status, fraud, signature string) []byte {
	t.Helper()
	n := map[string]any{
		"va_numbers":         []map[string]string{{"va_number": "12345678901", "bank": "bca"}},
		"transaction_time":   "2026-10-12 10:00:00",
		"transaction_status": status,
		"transaction_id":     "9aed5972-5b6a-401e-894b-a32c91ed1a3a",
		"status_message":     "midtrans payment notification",
		"status_code":        "200",
		"signature_key":      signature,
		"settlement_time":    "2026-10-12 10:05:00",
		"payment_type":       "bank_transfer",
		"payment_amounts":    []any{},
		"order_id":           orderID,
		"merchant_id":        "G123456789",
		"gross_amount":       "150000.00",
		"currency":           "IDR",
	}
	if fraud != "" {
		n["fraud_status"] = fraud
	}
	b, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	return b
}

func newAdapter(key string) *midtrans.Adapter {
	return midtrans.NewAdapter(midtrans.Config{ServerKey: key, BaseURL: "http://unused"})
}

func TestParseWebhook_VASettlementWithArrayFields(t *testing.T) {
	payload := vaNotification(t, "settlement", "accept", sign(orderID, "200", "150000.00", serverKey))

	n, err := newAdapter(serverKey).ParseWebhook(payload, nil)
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if n.PaymentID != orderID {
		t.Errorf("payment id: got %q, want order_id %q", n.PaymentID, orderID)
	}
	if n.GatewayTransactionID != "9aed5972-5b6a-401e-894b-a32c91ed1a3a" {
		t.Errorf("transaction id: got %q", n.GatewayTransactionID)
	}
	if n.Status != ports.TransactionSettled {
		t.Errorf("status: got %s, want %s", n.Status, ports.TransactionSettled)
	}
	if n.GatewayStatus != "settlement" {
		t.Errorf("gateway status: got %q, want settlement", n.GatewayStatus)
	}
}

func TestParseWebhook_RejectsBadSignature(t *testing.T) {
	payload := vaNotification(t, "settlement", "", sign(orderID, "200", "150000.00", "some-other-key"))

	_, err := newAdapter(serverKey).ParseWebhook(payload, nil)
	if !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestParseWebhook_RejectsWhenServerKeyMissing(t *testing.T) {
	// With no key configured, a signature computed over an empty key must not be accepted.
	payload := vaNotification(t, "settlement", "", sign(orderID, "200", "150000.00", ""))

	_, err := newAdapter("").ParseWebhook(payload, nil)
	if !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestParseWebhook_RejectsMalformedJSON(t *testing.T) {
	_, err := newAdapter(serverKey).ParseWebhook([]byte(`{not json`), nil)
	if !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestParseWebhook_StatusMapping(t *testing.T) {
	cases := []struct {
		status, fraud string
		want          ports.TransactionStatus
	}{
		{"settlement", "", ports.TransactionSettled},
		{"capture", "accept", ports.TransactionSettled},
		{"capture", "challenge", ports.TransactionPending},
		{"pending", "", ports.TransactionPending},
		{"deny", "", ports.TransactionFailed},
		{"cancel", "", ports.TransactionFailed},
		{"expire", "", ports.TransactionFailed},
		{"failure", "", ports.TransactionFailed},
		{"refund", "", ports.TransactionUnknown},
	}
	a := newAdapter(serverKey)
	for _, tc := range cases {
		t.Run(tc.status+"/"+tc.fraud, func(t *testing.T) {
			payload := vaNotification(t, tc.status, tc.fraud, sign(orderID, "200", "150000.00", serverKey))
			n, err := a.ParseWebhook(payload, nil)
			if err != nil {
				t.Fatalf("ParseWebhook: %v", err)
			}
			if n.Status != tc.want {
				t.Errorf("got %s, want %s", n.Status, tc.want)
			}
		})
	}
}
