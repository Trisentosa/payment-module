package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

type reconcileHarness struct {
	repo     *fake.PaymentRepo
	gw       *fake.Gateway
	attempts *fake.AttemptRecorder
	r        *command.Reconciler
}

func newReconcileHarness(seed ...*payment.Payment) *reconcileHarness {
	repo, gw, att := fake.NewPaymentRepo(seed...), &fake.Gateway{}, &fake.AttemptRecorder{}
	return &reconcileHarness{repo: repo, gw: gw, attempts: att,
		r: command.NewReconciler(repo, fake.GatewayFactory{"MIDTRANS": gw}, fake.NewCache(), att, command.ReconcileConfig{})}
}

// orphanedCharge is an INITIATED payment whose charge reply was lost `age` ago.
func orphanedCharge(t *testing.T, age time.Duration) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(150000, "IDR")
	p, err := payment.New("ORDER-O", "order-service", m, payment.GatewayMidtrans, payment.MethodCard, payment.CustomerInfo{}, "", nil, nil)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	p.CreatedAt = time.Now().Add(-age)
	return p
}

func TestReconcileCharge(t *testing.T) {
	cases := []struct {
		name       string
		state      *ports.TransactionState
		stateErr   error
		cancelErr  error
		wantStatus payment.Status
		wantCancel bool
	}{
		{"gateway never created it", &ports.TransactionState{Found: false}, nil, nil, payment.StatusFailed, false},
		{"card was charged", &ports.TransactionState{Found: true, GatewayTransactionID: "tx-o", Status: ports.TransactionSettled, GatewayStatus: "capture"},
			nil, nil, payment.StatusCompleted, false},
		{"gateway declined", &ports.TransactionState{Found: true, GatewayTransactionID: "tx-o", Status: ports.TransactionFailed, GatewayStatus: "deny"},
			nil, nil, payment.StatusFailed, false},
		{"pending without instructions is cancelled", &ports.TransactionState{Found: true, GatewayTransactionID: "tx-o", Status: ports.TransactionPending, GatewayStatus: "pending"},
			nil, nil, payment.StatusFailed, true},
		{"cancel failed: try again later", &ports.TransactionState{Found: true, GatewayTransactionID: "tx-o", Status: ports.TransactionPending},
			nil, apperror.GatewayError("timeout", ports.ErrUnknownOutcome), payment.StatusInitiated, true},
		{"status unavailable: try again later", nil, apperror.GatewayError("timeout", ports.ErrUnknownOutcome), nil, payment.StatusInitiated, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := orphanedCharge(t, 10*time.Minute)
			rh := newReconcileHarness(p)
			rh.gw.State, rh.gw.StateErr, rh.gw.CancelErr = tc.state, tc.stateErr, tc.cancelErr

			rh.r.RunOnce(context.Background())

			got := stored(t, rh.repo, p.ID)
			if got.Status != tc.wantStatus {
				t.Errorf("status: got %s, want %s", got.Status, tc.wantStatus)
			}
			if tc.wantStatus == payment.StatusCompleted && got.GatewayTransactionID != "tx-o" {
				t.Errorf("transaction id not recorded: %q", got.GatewayTransactionID)
			}
			if cancelled := len(rh.gw.CancelCalls) > 0; cancelled != tc.wantCancel {
				t.Errorf("cancel called: %v, want %v", cancelled, tc.wantCancel)
			}
			if len(rh.gw.StatusCalls) != 1 || rh.gw.StatusCalls[0].PaymentID != p.ID.String() {
				t.Errorf("status lookups: %+v, want one by payment id", rh.gw.StatusCalls)
			}
			if len(rh.attempts.Of(ports.OperationStatus)) != 1 {
				t.Errorf("STATUS attempts: %+v", rh.attempts.Attempts)
			}
		})
	}
}

func TestReconcileCharge_LeavesInFlightChargesAlone(t *testing.T) {
	p := orphanedCharge(t, 10*time.Second) // younger than the grace period: the call may still be running
	rh := newReconcileHarness(p)

	rh.r.RunOnce(context.Background())

	if len(rh.gw.StatusCalls) != 0 || stored(t, rh.repo, p.ID).Status != payment.StatusInitiated {
		t.Errorf("young INITIATED payment was touched")
	}
}

// pendingRefundAged returns a completed payment with one PENDING refund of 40000 created `age` ago.
func pendingRefundAged(t *testing.T, age time.Duration) (*payment.Payment, *payment.Refund) {
	t.Helper()
	p := paidPayment(t, payment.MethodQRIS)
	r, err := p.RequestRefund("rf-1", 40000, "", "order-service")
	if err != nil {
		t.Fatalf("RequestRefund: %v", err)
	}
	r.CreatedAt = time.Now().Add(-age)
	return p, r
}

func TestReconcileRefund_CompletesFromGatewayRefundList(t *testing.T) {
	p, r := pendingRefundAged(t, 10*time.Minute)
	rh := newReconcileHarness(p)
	rh.gw.State = &ports.TransactionState{Found: true, GatewayTransactionID: "gw-tx-r", GatewayStatus: "partial_refund",
		Refunds: []ports.RefundNotification{{RefundKey: r.ID.String(), GatewayRefundID: "3", Amount: 40000}}}

	rh.r.RunOnce(context.Background())

	got := stored(t, rh.repo, p.ID)
	if got.Refund(r.ID).Status != payment.RefundStatusSucceeded || got.Status != payment.StatusPartiallyRefunded {
		t.Errorf("refund=%s payment=%s", got.Refund(r.ID).Status, got.Status)
	}
	if len(rh.gw.RefundCalls) != 0 {
		t.Error("a refund the gateway already lists must not be re-sent")
	}
}

func TestReconcileRefund_RetryRules(t *testing.T) {
	cases := []struct {
		name       string
		age        time.Duration
		result     *ports.RefundResult
		wantStatus payment.RefundStatus
		wantRetry  bool
	}{
		{"too soon to retry", 10 * time.Minute, nil, payment.RefundStatusPending, false},
		{"retried and accepted", time.Hour, nil, payment.RefundStatusSucceeded, true},
		{"retried and declined", time.Hour, &ports.RefundResult{Accepted: false, DeclineReason: "midtrans 412"}, payment.RefundStatusFailed, true},
		{"past the retry window", 7 * 24 * time.Hour, nil, payment.RefundStatusPending, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, r := pendingRefundAged(t, tc.age)
			rh := newReconcileHarness(p)
			rh.gw.State = &ports.TransactionState{Found: true, GatewayTransactionID: "gw-tx-r", Status: ports.TransactionSettled}
			rh.gw.RefundResult = tc.result

			rh.r.RunOnce(context.Background())

			if got := stored(t, rh.repo, p.ID).Refund(r.ID).Status; got != tc.wantStatus {
				t.Errorf("refund status: got %s, want %s", got, tc.wantStatus)
			}
			retried := len(rh.gw.RefundCalls) > 0
			if retried != tc.wantRetry {
				t.Fatalf("retried: %v, want %v", retried, tc.wantRetry)
			}
			if retried && rh.gw.RefundCalls[0].RefundKey != r.ID.String() {
				t.Errorf("retry must reuse the refund key: got %q", rh.gw.RefundCalls[0].RefundKey)
			}
		})
	}
}
