package payment_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

// Event payloads go to consumers as the "data" field of each message. These keys are the
// published contract documented in docs/events.md; renaming a field must not change them.
func TestEventPayloadKeys(t *testing.T) {
	id, at := uuid.New(), time.Now()
	cases := []struct {
		event payment.DomainEvent
		keys  []string
	}{
		{payment.PaymentInitiated{PaymentID: id, OccurredAt: at}, []string{"occurred_at", "payment_id"}},
		{payment.PaymentPending{PaymentID: id, OccurredAt: at}, []string{"gateway_transaction_id", "occurred_at", "payment_id"}},
		{payment.PaymentCompleted{PaymentID: id, OccurredAt: at}, []string{"occurred_at", "paid_at", "payment_id"}},
		{payment.PaymentFailed{PaymentID: id, OccurredAt: at}, []string{"error_code", "error_message", "occurred_at", "payment_id"}},
		{payment.PaymentCancelled{PaymentID: id, OccurredAt: at}, []string{"occurred_at", "payment_id"}},
		{payment.PaymentExpired{PaymentID: id, OccurredAt: at}, []string{"occurred_at", "payment_id"}},
		{payment.RefundRequested{PaymentID: id, OccurredAt: at}, []string{"amount", "occurred_at", "payment_id", "refund_id"}},
		{payment.RefundCompleted{PaymentID: id, OccurredAt: at},
			[]string{"amount", "occurred_at", "payment_id", "payment_status", "refund_id", "refunded_amount"}},
		{payment.RefundFailed{PaymentID: id, OccurredAt: at}, []string{"occurred_at", "payment_id", "reason", "refund_id"}},
	}
	for _, tc := range cases {
		b, err := json.Marshal(tc.event)
		if err != nil {
			t.Fatalf("%s: %v", tc.event.EventType(), err)
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		got := make([]string, 0, len(m))
		for k := range m {
			got = append(got, k)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, tc.keys) {
			t.Errorf("%s keys: got %v, want %v", tc.event.EventType(), got, tc.keys)
		}
	}
}
