package payment_test

import (
	"testing"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

func newTestPayment(t *testing.T) *payment.Payment {
	t.Helper()
	m, _ := payment.NewMoney(50000, "IDR")
	p, err := payment.New(
		"REF-001", "order-service",
		m, payment.GatewayMidtrans, "BANK_TRANSFER",
		payment.CustomerInfo{ExternalID: "u1", Name: "Alice", Email: "alice@example.com"},
		"Test payment", nil, nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestNew_InitiatedStatus(t *testing.T) {
	p := newTestPayment(t)
	if p.Status != payment.StatusInitiated {
		t.Errorf("status: got %s, want %s", p.Status, payment.StatusInitiated)
	}
}

func TestNew_RaisesInitiatedEvent(t *testing.T) {
	p := newTestPayment(t)
	evts := p.PopEvents()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
	if evts[0].EventType() != "PaymentInitiated" {
		t.Errorf("event type: got %s, want PaymentInitiated", evts[0].EventType())
	}
	if evts[0].AggregateID() != p.ID {
		t.Errorf("aggregate id mismatch")
	}
}

func TestNew_PopEventsClears(t *testing.T) {
	p := newTestPayment(t)
	_ = p.PopEvents()
	evts := p.PopEvents()
	if len(evts) != 0 {
		t.Errorf("expected 0 events after pop, got %d", len(evts))
	}
}

func TestNew_RequiresReferenceID(t *testing.T) {
	m, _ := payment.NewMoney(1000, "IDR")
	_, err := payment.New("", "svc", m, payment.GatewayMidtrans, "", payment.CustomerInfo{}, "", nil, nil)
	if !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Errorf("expected INVALID_INPUT, got %v", err)
	}
}

func TestNew_RequiresCallerService(t *testing.T) {
	m, _ := payment.NewMoney(1000, "IDR")
	_, err := payment.New("REF", "", m, payment.GatewayMidtrans, "", payment.CustomerInfo{}, "", nil, nil)
	if !apperror.IsCode(err, apperror.CodeInvalidInput) {
		t.Errorf("expected INVALID_INPUT, got %v", err)
	}
}

func TestMarkPending_TransitionsFromInitiated(t *testing.T) {
	p := newTestPayment(t)
	_ = p.PopEvents()

	if err := p.MarkPending("GW-TX-001", map[string]any{"token": "abc"}); err != nil {
		t.Fatalf("MarkPending: %v", err)
	}
	if p.Status != payment.StatusPending {
		t.Errorf("status: got %s, want %s", p.Status, payment.StatusPending)
	}
	if p.GatewayTransactionID != "GW-TX-001" {
		t.Errorf("gateway tx id not set")
	}

	evts := p.PopEvents()
	if len(evts) != 1 || evts[0].EventType() != "PaymentPending" {
		t.Errorf("expected PaymentPending event")
	}
}

func TestMarkPending_RejectsNonInitiated(t *testing.T) {
	p := newTestPayment(t)
	_ = p.PopEvents()
	_ = p.MarkPending("GW-TX-001", nil)

	err := p.MarkPending("GW-TX-002", nil)
	if !apperror.IsCode(err, apperror.CodeInvalidState) {
		t.Errorf("expected INVALID_STATE, got %v", err)
	}
}

func TestStatus_IsTerminal(t *testing.T) {
	terminal := []payment.Status{
		payment.StatusCompleted, payment.StatusFailed,
		payment.StatusExpired, payment.StatusRefunded, payment.StatusRefundFailed,
	}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%s should be terminal", s)
		}
	}

	nonTerminal := []payment.Status{
		payment.StatusInitiated, payment.StatusPending,
		payment.StatusProcessing, payment.StatusCancelled,
	}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}
