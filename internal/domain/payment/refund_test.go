package payment_test

import (
	"testing"
	"time"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

// completedPayment returns a 100000 IDR payment that has been paid, with events cleared.
func completedPayment(t *testing.T) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(100000, "IDR")
	p, err := payment.New("REF-R", "order-service", m, payment.GatewayMidtrans, payment.MethodQRIS,
		payment.CustomerInfo{}, "", nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := p.MarkPending("tx-1", nil); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	if err := p.MarkCompleted(time.Now()); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	p.PopEvents()
	return p
}

func eventTypes(p *payment.Payment) []string {
	var out []string
	for _, e := range p.PopEvents() {
		out = append(out, e.EventType())
	}
	return out
}

func TestRequestRefund_ReservesAmount(t *testing.T) {
	p := completedPayment(t)

	r, err := p.RequestRefund("rf-1", 30000, "damaged", "order-service")
	if err != nil {
		t.Fatalf("RequestRefund: %v", err)
	}
	if r.Status != payment.RefundStatusPending {
		t.Errorf("status: got %s, want PENDING", r.Status)
	}
	if got := p.RefundableAmount(); got != 70000 {
		t.Errorf("refundable: got %d, want 70000 (pending refunds stay reserved)", got)
	}
	if got := p.RefundedAmount(); got != 0 {
		t.Errorf("refunded: got %d, want 0 until the gateway confirms", got)
	}
	if p.Status != payment.StatusCompleted {
		t.Errorf("payment status: got %s, want COMPLETED while the refund is pending", p.Status)
	}
	if got := eventTypes(p); len(got) != 1 || got[0] != "RefundRequested" {
		t.Errorf("events: got %v, want [RefundRequested]", got)
	}
}

func TestRequestRefund_Rules(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(p *payment.Payment)
		ref    string
		amount int64
		want   apperror.Code
	}{
		{"zero amount", nil, "rf", 0, apperror.CodeInvalidInput},
		{"more than paid", nil, "rf", 100001, apperror.CodeInvalidInput},
		{"missing reference", nil, "", 1000, apperror.CodeInvalidInput},
		{"more than what's left", func(p *payment.Payment) { _, _ = p.RequestRefund("rf-0", 90000, "", "svc") }, "rf", 20000, apperror.CodeInvalidInput},
		{"reused reference", func(p *payment.Payment) { _, _ = p.RequestRefund("rf", 1000, "", "svc") }, "rf", 1000, apperror.CodeAlreadyExists},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := completedPayment(t)
			if tc.setup != nil {
				tc.setup(p)
			}
			if _, err := p.RequestRefund(tc.ref, tc.amount, "", "svc"); !apperror.IsCode(err, tc.want) {
				t.Fatalf("expected %s, got %v", tc.want, err)
			}
		})
	}
}

func TestRequestRefund_OnlyAfterPayment(t *testing.T) {
	m, _ := payment.NewMoney(100000, "IDR")
	p, _ := payment.New("REF-P", "svc", m, payment.GatewayMidtrans, payment.MethodQRIS, payment.CustomerInfo{}, "", nil, nil)
	_ = p.MarkPending("tx", nil)

	if _, err := p.RequestRefund("rf", 1000, "", "svc"); !apperror.IsCode(err, apperror.CodeInvalidState) {
		t.Fatalf("expected INVALID_STATE for a PENDING payment, got %v", err)
	}
}

func TestCompleteRefund_PartialThenFull(t *testing.T) {
	p := completedPayment(t)

	r1, _ := p.RequestRefund("rf-1", 40000, "", "svc")
	if err := p.CompleteRefund(r1.ID, "gw-1"); err != nil {
		t.Fatalf("CompleteRefund 1: %v", err)
	}
	if p.Status != payment.StatusPartiallyRefunded || p.RefundedAmount() != 40000 {
		t.Fatalf("after first refund: status=%s refunded=%d", p.Status, p.RefundedAmount())
	}
	if r1.GatewayRefundID != "gw-1" || r1.CompletedAt == nil {
		t.Errorf("refund not stamped: %+v", r1)
	}

	r2, err := p.RequestRefund("rf-2", 60000, "", "svc")
	if err != nil {
		t.Fatalf("second RequestRefund: %v", err)
	}
	if err := p.CompleteRefund(r2.ID, "gw-2"); err != nil {
		t.Fatalf("CompleteRefund 2: %v", err)
	}
	if p.Status != payment.StatusRefunded || p.RefundableAmount() != 0 {
		t.Fatalf("after full refund: status=%s refundable=%d", p.Status, p.RefundableAmount())
	}
	if _, err := p.RequestRefund("rf-3", 1, "", "svc"); !apperror.IsCode(err, apperror.CodeInvalidState) {
		t.Errorf("refund after fully refunded: expected INVALID_STATE, got %v", err)
	}
}

func TestCompleteRefund_IsIdempotent(t *testing.T) {
	p := completedPayment(t)
	r, _ := p.RequestRefund("rf", 10000, "", "svc")
	_ = p.CompleteRefund(r.ID, "gw")
	p.PopEvents()

	if err := p.CompleteRefund(r.ID, "gw"); err != nil {
		t.Fatalf("second CompleteRefund: %v", err)
	}
	if got := eventTypes(p); len(got) != 0 {
		t.Errorf("repeat confirmation raised events: %v", got)
	}
}

func TestFailRefund_ReleasesAmount(t *testing.T) {
	p := completedPayment(t)
	r, _ := p.RequestRefund("rf", 100000, "", "svc")
	p.PopEvents()

	if err := p.FailRefund(r.ID, "midtrans 412: cannot modify"); err != nil {
		t.Fatalf("FailRefund: %v", err)
	}
	if r.Status != payment.RefundStatusFailed || r.FailureReason == "" {
		t.Errorf("refund: %+v", r)
	}
	if p.RefundableAmount() != 100000 || p.Status != payment.StatusCompleted {
		t.Errorf("refundable=%d status=%s, want the full amount back and COMPLETED", p.RefundableAmount(), p.Status)
	}
	if got := eventTypes(p); len(got) != 1 || got[0] != "RefundFailed" {
		t.Errorf("events: got %v, want [RefundFailed]", got)
	}
	if err := p.CompleteRefund(r.ID, "gw"); !apperror.IsCode(err, apperror.CodeInvalidState) {
		t.Errorf("completing a failed refund: expected INVALID_STATE, got %v", err)
	}
}
