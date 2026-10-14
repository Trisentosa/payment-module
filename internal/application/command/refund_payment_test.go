package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

// paidPayment is a completed 100000 IDR payment owned by order-service.
func paidPayment(t *testing.T, method payment.Method) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(100000, "IDR")
	p, err := payment.New("ORDER-R", "order-service", m, payment.GatewayMidtrans, method,
		payment.CustomerInfo{Name: "Alice"}, "", nil, nil)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	_ = p.MarkPending("gw-tx-r", nil)
	_ = p.MarkCompleted(time.Now())
	return p
}

type refundHarness struct {
	repo     *fake.PaymentRepo
	gw       *fake.Gateway
	cache    *fake.Cache
	attempts *fake.AttemptRecorder
	h        *command.RefundPaymentHandler
	p        *payment.Payment
}

func newRefundHarness(t *testing.T, method payment.Method) *refundHarness {
	t.Helper()
	p := paidPayment(t, method)
	repo := fake.NewPaymentRepo(p)
	gw := &fake.Gateway{Capabilities: map[payment.Method]ports.MethodCapability{
		payment.MethodVirtualAccount: {Charge: true},
		payment.MethodQRIS:           {Charge: true, Refund: true, PartialRefund: true},
		payment.MethodShopeePay:      {Charge: true, Refund: true},
	}}
	c := fake.NewCache()
	att := &fake.AttemptRecorder{}
	return &refundHarness{repo: repo, gw: gw, cache: c, p: p,
		attempts: att,
		h:        command.NewRefundPaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, c, att)}
}

func (rh *refundHarness) refund(ref string, amount int64) (*command.RefundPaymentResult, error) {
	return rh.h.Handle(context.Background(), command.RefundPaymentCommand{
		PaymentID: rh.p.ID, ReferenceID: ref, Amount: amount, Reason: "customer request", CallerService: "order-service",
	})
}

func (rh *refundHarness) stored(t *testing.T) *payment.Payment {
	return stored(t, rh.repo, rh.p.ID)
}

func TestRefund_FullRefundThroughGateway(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)

	res, err := rh.refund("rf-1", 0) // 0 = everything refundable
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Refund.Status != payment.RefundStatusSucceeded || res.Refund.Amount != 100000 {
		t.Errorf("refund: status=%s amount=%d", res.Refund.Status, res.Refund.Amount)
	}

	if len(rh.gw.RefundCalls) != 1 {
		t.Fatalf("gateway refund calls: %d, want 1", len(rh.gw.RefundCalls))
	}
	call := rh.gw.RefundCalls[0]
	if call.RefundKey != res.Refund.ID.String() || call.GatewayTransactionID != "gw-tx-r" || call.Amount != 100000 {
		t.Errorf("gateway request: %+v (refund key must be the refund ID)", call)
	}

	got := rh.stored(t)
	if got.Status != payment.StatusRefunded || got.Refund(res.Refund.ID).GatewayRefundID != "gw-refund-1" {
		t.Errorf("stored: status=%s refund=%+v", got.Status, got.Refund(res.Refund.ID))
	}
}

func TestRefund_PartialRefundsAddUp(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)

	if _, err := rh.refund("rf-1", 25000); err != nil {
		t.Fatalf("first: %v", err)
	}
	if got := rh.stored(t); got.Status != payment.StatusPartiallyRefunded || got.RefundedAmount() != 25000 {
		t.Fatalf("after first: status=%s refunded=%d", got.Status, got.RefundedAmount())
	}
	if _, err := rh.refund("rf-2", 75000); err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := rh.stored(t); got.Status != payment.StatusRefunded {
		t.Fatalf("after second: status=%s", got.Status)
	}
	if _, err := rh.refund("rf-3", 1); !apperror.IsCode(err, apperror.CodeInvalidState) {
		t.Errorf("refunding a fully refunded payment: expected INVALID_STATE, got %v", err)
	}
}

func TestRefund_VirtualAccountIsRejected(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodVirtualAccount)

	_, err := rh.refund("rf-1", 0)
	if !apperror.IsCode(err, apperror.CodeInvalidState) {
		t.Fatalf("expected INVALID_STATE, got %v", err)
	}
	if len(rh.gw.RefundCalls) != 0 || len(rh.stored(t).Refunds) != 0 {
		t.Errorf("VA refund must not reach the gateway or be stored")
	}
}

func TestRefund_ShopeePayOnlyInFull(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodShopeePay)

	if _, err := rh.refund("rf-1", 40000); !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("partial ShopeePay refund: expected INVALID_INPUT, got %v", err)
	}
	if res, err := rh.refund("rf-2", 0); err != nil || res.Refund.Status != payment.RefundStatusSucceeded {
		t.Fatalf("full ShopeePay refund: res=%+v err=%v", res, err)
	}
}

func TestRefund_OnlyTheOwningCallerOrAdmin(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)

	_, err := rh.h.Handle(context.Background(), command.RefundPaymentCommand{
		PaymentID: rh.p.ID, ReferenceID: "rf-1", CallerService: "billing-service",
	})
	if !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Fatalf("other caller: expected NOT_FOUND, got %v", err)
	}

	res, err := rh.h.Handle(context.Background(), command.RefundPaymentCommand{
		PaymentID: rh.p.ID, ReferenceID: "rf-1", Admin: true,
	})
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	if res.Refund.RequestedBy != "admin" {
		t.Errorf("requested_by: got %q, want admin", res.Refund.RequestedBy)
	}
}

func TestRefund_SameReferenceReplays(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)

	first, err := rh.refund("rf-1", 30000)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	again, err := rh.refund("rf-1", 30000)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || again.Refund.ID != first.Refund.ID {
		t.Errorf("replay: replayed=%v id=%s, want the original %s", again.Replayed, again.Refund.ID, first.Refund.ID)
	}
	if len(rh.gw.RefundCalls) != 1 {
		t.Errorf("gateway refund calls: %d, want 1", len(rh.gw.RefundCalls))
	}

	if _, err := rh.refund("rf-1", 50000); !apperror.IsCode(err, apperror.CodeIdempotencyConflict) {
		t.Errorf("same reference, different amount: expected IDEMPOTENCY_CONFLICT, got %v", err)
	}
}

func TestRefund_GatewayDeclineFailsRefundAndReleasesAmount(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)
	rh.gw.RefundResult = &ports.RefundResult{Accepted: false, DeclineReason: "midtrans 412: cannot modify"}

	res, err := rh.refund("rf-1", 0)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Refund.Status != payment.RefundStatusFailed || res.Refund.FailureReason != "midtrans 412: cannot modify" {
		t.Errorf("refund: %+v", res.Refund)
	}
	if got := rh.stored(t); got.Status != payment.StatusCompleted || got.RefundableAmount() != 100000 {
		t.Errorf("stored: status=%s refundable=%d", got.Status, got.RefundableAmount())
	}
}

func TestRefund_UnknownOutcomeStaysPendingAndReserved(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)
	rh.gw.RefundErr = apperror.GatewayError("midtrans refund: no usable reply", nil)

	res, err := rh.refund("rf-1", 60000)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Refund.Status != payment.RefundStatusPending {
		t.Fatalf("status: got %s, want PENDING", res.Refund.Status)
	}
	// The pending 60000 stays reserved, so 50000 more would exceed what was paid.
	rh.gw.RefundErr = nil
	if _, err := rh.refund("rf-2", 50000); !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Errorf("over-refund while pending: expected INVALID_INPUT, got %v", err)
	}
}

func TestRefund_NotificationRacingTheReplyIsHandled(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)
	// The gateway's refund notification is processed while CreateRefund is still in flight.
	rh.gw.OnRefund = func(req ports.RefundRequest) {
		p, _ := rh.repo.FindByID(context.Background(), rh.p.ID)
		_ = p.CompleteRefund(uuid.MustParse(req.RefundKey), "gw-refund-1")
		if err := rh.repo.Save(context.Background(), p); err != nil {
			t.Errorf("simulated notification save: %v", err)
		}
	}

	res, err := rh.refund("rf-1", 0)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Refund.Status != payment.RefundStatusSucceeded {
		t.Errorf("status: got %s, want SUCCEEDED", res.Refund.Status)
	}
	completed := 0
	for _, e := range rh.repo.Events {
		if e.EventType() == "RefundCompleted" {
			completed++
		}
	}
	if completed != 1 {
		t.Errorf("RefundCompleted events: %d, want exactly 1", completed)
	}
}

func TestRefund_InvalidatesCachedPayment(t *testing.T) {
	rh := newRefundHarness(t, payment.MethodQRIS)
	ctx := context.Background()
	_ = rh.cache.Set(ctx, ports.PaymentCacheKey(rh.p.ID.String()), "stale", 0)
	_ = rh.cache.Set(ctx, ports.ReferenceCacheKey("order-service", "ORDER-R"), "stale", 0)

	if _, err := rh.refund("rf-1", 0); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if rh.cache.Has(ports.PaymentCacheKey(rh.p.ID.String())) || rh.cache.Has(ports.ReferenceCacheKey("order-service", "ORDER-R")) {
		t.Error("cached payment views not invalidated")
	}
}
