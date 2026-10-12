package command_test

import (
	"context"
	"testing"

	"github.com/Trisentosa/payment-module/internal/application/command"
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
	if res.PaymentInstructions["va_number"] == nil {
		t.Error("payment instructions missing va_number")
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
