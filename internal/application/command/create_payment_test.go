package command_test

import (
	"context"
	"testing"

	"github.com/Trisentosa/payment-module/internal/application/command"
	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/Trisentosa/payment-module/internal/testsupport/fake"
)

func createCmd(ref string) command.CreatePaymentCommand {
	return command.CreatePaymentCommand{
		ReferenceID:       ref,
		CallerService:     "order-service",
		Amount:            150000,
		Currency:          "IDR",
		GatewayType:       "MIDTRANS",
		PaymentMethodType: "VIRTUAL_ACCOUNT",
		BankCode:          "BCA",
		CustomerName:      "Alice",
		CustomerEmail:     "alice@example.com",
	}
}

func TestCreatePayment_MarksPendingAndSaves(t *testing.T) {
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{}
	h := command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, nil)

	res, err := h.Handle(context.Background(), createCmd("ORDER-10"))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Payment.Status != "PENDING" {
		t.Errorf("status: got %s, want PENDING", res.Payment.Status)
	}
	if res.NextAction.Type != ports.NextActionDisplayVA || res.NextAction.VANumber == "" {
		t.Errorf("next action: got %+v, want DISPLAY_VA with a va_number", res.NextAction)
	}
}

func TestCreatePayment_ExistingReferenceSkipsGateway(t *testing.T) {
	ctx := context.Background()
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{}
	h := command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, nil)

	if _, err := h.Handle(ctx, createCmd("ORDER-11")); err != nil {
		t.Fatalf("first Handle: %v", err)
	}

	_, err := h.Handle(ctx, createCmd("ORDER-11"))
	if !apperror.IsCode(err, apperror.CodeAlreadyExists) {
		t.Fatalf("expected ALREADY_EXISTS, got %v", err)
	}
	if gw.CreateCalls != 1 {
		t.Errorf("gateway called %d times, want 1", gw.CreateCalls)
	}
}

func TestCreatePayment_GatewayFailureRecordsFailedPayment(t *testing.T) {
	ctx := context.Background()
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{CreateErr: apperror.GatewayError("midtrans charge failed", nil)}
	h := command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, nil)

	_, err := h.Handle(ctx, createCmd("ORDER-12"))
	if !apperror.IsCode(err, apperror.CodeGatewayError) {
		t.Fatalf("expected GATEWAY_ERROR, got %v", err)
	}
	p, err := repo.FindByReference(ctx, "ORDER-12", "order-service")
	if err != nil {
		t.Fatalf("failed payment not recorded: %v", err)
	}
	if p.Status != "FAILED" {
		t.Errorf("status: got %s, want FAILED", p.Status)
	}
}

func TestCreatePayment_RejectsMethodTheGatewayCannotCharge(t *testing.T) {
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{Capabilities: map[payment.Method]ports.MethodCapability{
		payment.MethodVirtualAccount: {Charge: true},
	}}
	h := command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, nil)

	cmd := createCmd("ORDER-13")
	cmd.PaymentMethodType = "GOPAY"
	_, err := h.Handle(context.Background(), cmd)
	if !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
	if gw.CreateCalls != 0 || repo.SaveCalls != 0 {
		t.Errorf("gateway calls=%d saves=%d, want none", gw.CreateCalls, repo.SaveCalls)
	}
}

func TestCreatePayment_UnknownMethodIsInvalidInput(t *testing.T) {
	h := command.NewCreatePaymentHandler(fake.NewPaymentRepo(), fake.GatewayFactory{"MIDTRANS": &fake.Gateway{}}, nil)
	cmd := createCmd("ORDER-14")
	cmd.PaymentMethodType = "BITCOIN"
	if _, err := h.Handle(context.Background(), cmd); !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
}

func TestCreatePayment_CardCapturedAtChargeCompletesPayment(t *testing.T) {
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{CreateResp: &ports.GatewayResponse{
		GatewayTransactionID: "tx-cap", Status: ports.TransactionSettled, NextAction: ports.NextAction{Type: ports.NextActionNone},
	}}
	h := command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, nil)
	cmd := createCmd("ORDER-15")
	cmd.PaymentMethodType, cmd.CardToken = "CARD", "tok"

	res, err := h.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Payment.Status != payment.StatusCompleted || res.Payment.PaidAt == nil {
		t.Errorf("status=%s paid_at=%v, want COMPLETED with paid_at", res.Payment.Status, res.Payment.PaidAt)
	}
}

func TestCreatePayment_CardDeclinedAtChargeFailsPayment(t *testing.T) {
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{CreateResp: &ports.GatewayResponse{
		GatewayTransactionID: "tx-deny", Status: ports.TransactionFailed, NextAction: ports.NextAction{Type: ports.NextActionNone},
	}}
	h := command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, nil)
	cmd := createCmd("ORDER-16")
	cmd.PaymentMethodType, cmd.CardToken = "CARD", "tok"

	res, err := h.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.Payment.Status != payment.StatusFailed {
		t.Errorf("status: got %s, want FAILED", res.Payment.Status)
	}
	if res.Payment.GatewayTransactionID != "tx-deny" {
		t.Errorf("gateway transaction id not kept: %q", res.Payment.GatewayTransactionID)
	}
}

func TestCreatePayment_GatewayInputErrorStoresNothing(t *testing.T) {
	ctx := context.Background()
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{CreateErr: apperror.InvalidInput("card_token is required for CARD")}
	h := command.NewCreatePaymentHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, nil)
	cmd := createCmd("ORDER-17")
	cmd.PaymentMethodType = "CARD"

	if _, err := h.Handle(ctx, cmd); !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
	if repo.SaveCalls != 0 {
		t.Errorf("saved %d times; a fixable request must not consume the reference", repo.SaveCalls)
	}
}
