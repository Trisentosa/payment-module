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

func pendingPayment(t *testing.T, ref, gatewayTxID string) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(150000, "IDR")
	p, err := payment.New(ref, "order-service", m, payment.GatewayMidtrans, "VIRTUAL_ACCOUNT",
		payment.CustomerInfo{Name: "Alice", Email: "alice@example.com"}, "", nil, nil)
	if err != nil {
		t.Fatalf("payment.New: %v", err)
	}
	if err := p.MarkPending(gatewayTxID, nil); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	p.PopEvents()
	return p
}

func callbackHandler(repo *fake.PaymentRepo, gw *fake.Gateway, cache *fake.Cache) *command.ProcessCallbackHandler {
	return command.NewProcessCallbackHandler(repo, fake.GatewayFactory{"MIDTRANS": gw}, cache)
}

func TestProcessCallback_SettlementCompletesPayment(t *testing.T) {
	ctx := context.Background()
	p := pendingPayment(t, "ORDER-1", "gw-tx-1")
	repo := fake.NewPaymentRepo(p)
	cache := fake.NewCache()
	_ = cache.Set(ctx, "payment:"+p.ID.String(), "stale", 0)
	gw := &fake.Gateway{Notification: &ports.WebhookNotification{
		GatewayTransactionID: "gw-tx-1", Status: ports.TransactionSettled, GatewayStatus: "settlement",
	}}

	err := callbackHandler(repo, gw, cache).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if p.Status != payment.StatusCompleted {
		t.Errorf("status: got %s, want COMPLETED", p.Status)
	}
	if p.PaidAt == nil {
		t.Error("PaidAt not set")
	}
	if len(repo.Events) != 1 || repo.Events[0].EventType() != "PaymentCompleted" {
		t.Errorf("persisted events: got %v, want [PaymentCompleted]", repo.Events)
	}
	if cache.Has("payment:" + p.ID.String()) {
		t.Error("cached payment not invalidated")
	}
}

func TestProcessCallback_FailureStatusFailsPayment(t *testing.T) {
	ctx := context.Background()
	p := pendingPayment(t, "ORDER-2", "gw-tx-2")
	repo := fake.NewPaymentRepo(p)
	gw := &fake.Gateway{Notification: &ports.WebhookNotification{
		GatewayTransactionID: "gw-tx-2", Status: ports.TransactionFailed, GatewayStatus: "expire",
	}}

	if err := callbackHandler(repo, gw, fake.NewCache()).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if p.Status != payment.StatusFailed {
		t.Errorf("status: got %s, want FAILED", p.Status)
	}
}

func TestProcessCallback_PendingStatusChangesNothing(t *testing.T) {
	ctx := context.Background()
	p := pendingPayment(t, "ORDER-3", "gw-tx-3")
	repo := fake.NewPaymentRepo(p)
	gw := &fake.Gateway{Notification: &ports.WebhookNotification{
		GatewayTransactionID: "gw-tx-3", Status: ports.TransactionPending, GatewayStatus: "pending",
	}}

	if err := callbackHandler(repo, gw, fake.NewCache()).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if p.Status != payment.StatusPending || repo.SaveCalls != 0 {
		t.Errorf("expected no change: status=%s saves=%d", p.Status, repo.SaveCalls)
	}
}

func TestProcessCallback_RejectedWebhookIsNotApplied(t *testing.T) {
	ctx := context.Background()
	p := pendingPayment(t, "ORDER-4", "gw-tx-4")
	repo := fake.NewPaymentRepo(p)
	gw := &fake.Gateway{ParseErr: apperror.InvalidInput("webhook signature mismatch")}

	err := callbackHandler(repo, gw, fake.NewCache()).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"})
	if !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Fatalf("expected INVALID_INPUT, got %v", err)
	}
	if p.Status != payment.StatusPending || repo.SaveCalls != 0 {
		t.Errorf("rejected webhook changed state: status=%s saves=%d", p.Status, repo.SaveCalls)
	}
}

func TestProcessCallback_FindsPaymentByEchoedPaymentID(t *testing.T) {
	ctx := context.Background()
	p := pendingPayment(t, "ORDER-5", "gw-tx-5")
	repo := fake.NewPaymentRepo(p)
	// The transaction id doesn't match anything stored: the payment must be found by its ID.
	gw := &fake.Gateway{Notification: &ports.WebhookNotification{
		PaymentID: p.ID.String(), GatewayTransactionID: "unknown-tx", Status: ports.TransactionSettled, GatewayStatus: "settlement",
	}}

	if err := callbackHandler(repo, gw, fake.NewCache()).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if p.Status != payment.StatusCompleted {
		t.Errorf("status: got %s, want COMPLETED", p.Status)
	}
}

func TestProcessCallback_LegacyOrderIDFallsBackToTransactionID(t *testing.T) {
	ctx := context.Background()
	p := pendingPayment(t, "ORDER-6", "gw-tx-6")
	repo := fake.NewPaymentRepo(p)
	// Payments created before the change sent the caller's reference as order_id.
	gw := &fake.Gateway{Notification: &ports.WebhookNotification{
		PaymentID: "ORDER-6", GatewayTransactionID: "gw-tx-6", Status: ports.TransactionSettled, GatewayStatus: "settlement",
	}}

	if err := callbackHandler(repo, gw, fake.NewCache()).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if p.Status != payment.StatusCompleted {
		t.Errorf("status: got %s, want COMPLETED", p.Status)
	}
}

func TestProcessCallback_UnknownPaymentIsNotFound(t *testing.T) {
	ctx := context.Background()
	repo := fake.NewPaymentRepo()
	gw := &fake.Gateway{Notification: &ports.WebhookNotification{
		PaymentID: "5f0c2a3e-8a51-4c37-9a7e-2f1d6b0c9e11", GatewayTransactionID: "nope", Status: ports.TransactionSettled,
	}}

	err := callbackHandler(repo, gw, fake.NewCache()).Handle(ctx, command.ProcessCallbackCommand{GatewayType: "MIDTRANS"})
	if !apperror.IsCode(err, apperror.CodeNotFound) {
		t.Fatalf("expected NOT_FOUND, got %v", err)
	}
}
