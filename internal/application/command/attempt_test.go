package command_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

// unknownOutcome is how adapters report a timeout or 5xx.
var unknownOutcome = apperror.GatewayError("midtrans charge failed", fmt.Errorf("%w: i/o timeout", ports.ErrUnknownOutcome))

func createWith(gw *fake.Gateway) (*command.CreatePaymentHandler, *fake.PaymentRepo, *fake.AttemptRecorder) {
	repo, att := fake.NewPaymentRepo(), &fake.AttemptRecorder{}
	return command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, att), repo, att
}

func onlyAttempt(t *testing.T, att *fake.AttemptRecorder, op ports.GatewayOperation) ports.Attempt {
	t.Helper()
	got := att.Of(op)
	if len(got) != 1 {
		t.Fatalf("%s attempts: got %d, want 1 (all: %+v)", op, len(got), att.Attempts)
	}
	return got[0]
}

func TestCreatePayment_RecordsSuccessfulCharge(t *testing.T) {
	h, _, att := createWith(&fake.Gateway{})

	res, err := h.Handle(context.Background(), createCmd("ORDER-A1"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	a := onlyAttempt(t, att, ports.OperationCharge)
	if a.PaymentID != res.Payment.ID || a.Outcome != ports.AttemptSucceeded || a.GatewayTransactionID != res.Payment.GatewayTransactionID {
		t.Errorf("attempt: %+v", a)
	}
}

func TestCreatePayment_RecordsDefiniteGatewayFailure(t *testing.T) {
	h, repo, att := createWith(&fake.Gateway{CreateErr: apperror.GatewayError("midtrans charge failed: 406 duplicate", nil)})

	if _, err := h.Handle(context.Background(), createCmd("ORDER-A2")); !apperror.IsCode(err, apperror.CodeGatewayError) {
		t.Fatalf("expected GATEWAY_ERROR, got %v", err)
	}
	a := onlyAttempt(t, att, ports.OperationCharge)
	if a.Outcome != ports.AttemptFailed || a.ErrorCode != string(apperror.CodeGatewayError) {
		t.Errorf("attempt: %+v", a)
	}
	p, _ := repo.FindByReference(context.Background(), "ORDER-A2", "order-service")
	if p == nil || p.Status != payment.StatusFailed {
		t.Errorf("payment after a definite failure: %+v, want FAILED", p)
	}
}

func TestCreatePayment_UnknownOutcomeLeavesPaymentForReconciliation(t *testing.T) {
	h, repo, att := createWith(&fake.Gateway{CreateErr: unknownOutcome})

	_, err := h.Handle(context.Background(), createCmd("ORDER-A3"))
	if !apperror.IsCode(err, apperror.CodeGatewayError) || !errors.Is(err, ports.ErrUnknownOutcome) {
		t.Fatalf("expected an unknown-outcome GATEWAY_ERROR, got %v", err)
	}
	p, ferr := repo.FindByReference(context.Background(), "ORDER-A3", "order-service")
	if ferr != nil {
		t.Fatalf("payment not stored: %v", ferr)
	}
	if p.Status != payment.StatusInitiated {
		t.Errorf("status: got %s, want INITIATED (a card may have been charged)", p.Status)
	}
	if !strings.Contains(err.Error(), p.ID.String()) {
		t.Errorf("the caller's error should name the payment to poll: %v", err)
	}
	if a := onlyAttempt(t, att, ports.OperationCharge); a.Outcome != ports.AttemptUnknown {
		t.Errorf("attempt outcome: got %s, want UNKNOWN", a.Outcome)
	}
}

func TestCreatePayment_InvalidInputRecordsNothing(t *testing.T) {
	h, _, att := createWith(&fake.Gateway{CreateErr: apperror.InvalidInput("card_token is required")})

	if _, err := h.Handle(context.Background(), createCmd("ORDER-A4")); !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
	if len(att.Attempts) != 0 {
		t.Errorf("attempts: %+v, want none", att.Attempts)
	}
}

func TestCancelPayment_RecordsGatewayCancel(t *testing.T) {
	p := pendingPayment(t, "ORDER-A5", "gw-tx-a5")
	repo, att := fake.NewPaymentRepo(p), &fake.AttemptRecorder{}
	gw := &fake.Gateway{}
	h := command.NewCancelPaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, fake.NewCache(), att)

	if err := h.Handle(context.Background(), command.CancelPaymentCommand{PaymentID: p.ID, CallerService: "order-service"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	a := onlyAttempt(t, att, ports.OperationCancel)
	if a.Outcome != ports.AttemptSucceeded || a.GatewayTransactionID != "gw-tx-a5" {
		t.Errorf("attempt: %+v", a)
	}
}

func TestRefund_RecordsAttemptsPerOutcome(t *testing.T) {
	cases := []struct {
		name    string
		result  *ports.RefundResult
		err     error
		outcome ports.AttemptOutcome
	}{
		{"accepted", nil, nil, ports.AttemptSucceeded},
		{"declined", &ports.RefundResult{Accepted: false, DeclineReason: "midtrans 412"}, nil, ports.AttemptFailed},
		{"unknown", nil, apperror.GatewayError("timeout", ports.ErrUnknownOutcome), ports.AttemptUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rh := newRefundHarness(t, payment.MethodQRIS)
			rh.gw.RefundResult, rh.gw.RefundErr = tc.result, tc.err

			res, err := rh.refund("rf-1", 0)
			if err != nil {
				t.Fatalf("Handle: %v", err)
			}
			a := onlyAttempt(t, rh.attempts, ports.OperationRefund)
			if a.Outcome != tc.outcome || a.RefundID == nil || *a.RefundID != res.Refund.ID {
				t.Errorf("attempt: %+v", a)
			}
		})
	}
}

func TestProcessCallback_SettlesChargeWhoseReplyWasLost(t *testing.T) {
	ctx := context.Background()
	m, _ := payment.NewMoney(150000, "IDR")
	p, _ := payment.New("ORDER-A6", "order-service", m, payment.GatewayMidtrans, payment.MethodCard, payment.CustomerInfo{}, "", nil, nil)
	repo := fake.NewPaymentRepo(p) // stored INITIATED, as after an unknown charge outcome

	gw := &fake.Gateway{Notification: &ports.WebhookNotification{
		PaymentID: p.ID.String(), GatewayTransactionID: "gw-tx-a6", Status: ports.TransactionSettled, GatewayStatus: "capture",
	}}
	if err := callbackHandler(repo, gw, fake.NewCache()).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	got := stored(t, repo, p.ID)
	if got.Status != payment.StatusCompleted || got.GatewayTransactionID != "gw-tx-a6" {
		t.Errorf("status=%s tx=%q, want COMPLETED with the notification's transaction", got.Status, got.GatewayTransactionID)
	}
}
