package payment

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type RefundStatus string

const (
	// RefundStatusPending: requested, outcome not known yet. Its amount stays reserved, so it
	// can't be refunded twice, until the gateway confirms or declines it.
	RefundStatusPending   RefundStatus = "PENDING"
	RefundStatusSucceeded RefundStatus = "SUCCEEDED"
	RefundStatusFailed    RefundStatus = "FAILED"
)

// Refund returns part or all of a completed payment. It lives inside the Payment
// aggregate so the payment can enforce that refunds never exceed what was paid.
type Refund struct {
	ID              uuid.UUID
	PaymentID       uuid.UUID
	ReferenceID     string // caller's refund reference; unique per payment, used as the idempotency key
	Amount          int64
	Reason          string
	Status          RefundStatus
	GatewayRefundID string
	FailureReason   string
	RequestedBy     string // caller service name, or "admin"
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
}

// RefundedAmount is the total of refunds the gateway has confirmed.
func (p *Payment) RefundedAmount() int64 {
	var sum int64
	for _, r := range p.Refunds {
		if r.Status == RefundStatusSucceeded {
			sum += r.Amount
		}
	}
	return sum
}

// RefundableAmount is what can still be refunded: the amount paid minus refunds that
// succeeded or are still pending.
func (p *Payment) RefundableAmount() int64 {
	reserved := int64(0)
	for _, r := range p.Refunds {
		if r.Status != RefundStatusFailed {
			reserved += r.Amount
		}
	}
	return p.Amount.Amount - reserved
}

// Refund returns the refund with the given ID, or nil.
func (p *Payment) Refund(id uuid.UUID) *Refund {
	for _, r := range p.Refunds {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// RefundByReference returns the refund with the caller's reference, or nil.
func (p *Payment) RefundByReference(referenceID string) *Refund {
	for _, r := range p.Refunds {
		if r.ReferenceID == referenceID {
			return r
		}
	}
	return nil
}

// RequestRefund opens a PENDING refund and reserves its amount.
func (p *Payment) RequestRefund(referenceID string, amount int64, reason, requestedBy string) (*Refund, error) {
	if p.Status != StatusCompleted && p.Status != StatusPartiallyRefunded {
		return nil, apperror.InvalidState("only completed payments can be refunded, current: " + string(p.Status))
	}
	if referenceID == "" {
		return nil, apperror.InvalidInput("refund reference_id is required")
	}
	if p.RefundByReference(referenceID) != nil {
		return nil, apperror.AlreadyExists("refund already exists for reference_id: " + referenceID)
	}
	if amount <= 0 {
		return nil, apperror.InvalidInput("refund amount must be positive")
	}
	if refundable := p.RefundableAmount(); amount > refundable {
		return nil, apperror.InvalidInput(fmt.Sprintf("refund amount %d exceeds refundable amount %d", amount, refundable))
	}

	now := time.Now().UTC()
	r := &Refund{
		ID:          uuid.New(),
		PaymentID:   p.ID,
		ReferenceID: referenceID,
		Amount:      amount,
		Reason:      reason,
		Status:      RefundStatusPending,
		RequestedBy: requestedBy,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	p.Refunds = append(p.Refunds, r)
	p.UpdatedAt = now
	p.raise(RefundRequested{PaymentID: p.ID, RefundID: r.ID, Amount: amount, OccurredAt: now})
	return r, nil
}

// CompleteRefund marks a pending refund as confirmed by the gateway and moves the payment
// to PARTIALLY_REFUNDED or REFUNDED. Completing an already-succeeded refund is a no-op,
// since the gateway may confirm the same refund more than once.
func (p *Payment) CompleteRefund(refundID uuid.UUID, gatewayRefundID string) error {
	r := p.Refund(refundID)
	if r == nil {
		return apperror.NotFound("refund not found: " + refundID.String())
	}
	switch r.Status {
	case RefundStatusSucceeded:
		return nil
	case RefundStatusFailed:
		return apperror.InvalidState("refund " + refundID.String() + " already failed")
	}

	now := time.Now().UTC()
	r.Status = RefundStatusSucceeded
	r.GatewayRefundID = gatewayRefundID
	r.CompletedAt = &now
	r.UpdatedAt = now

	if p.RefundedAmount() >= p.Amount.Amount {
		p.Status = StatusRefunded
	} else {
		p.Status = StatusPartiallyRefunded
	}
	p.UpdatedAt = now
	p.raise(RefundCompleted{
		PaymentID: p.ID, RefundID: r.ID, Amount: r.Amount,
		RefundedAmount: p.RefundedAmount(), PaymentStatus: p.Status, OccurredAt: now,
	})
	return nil
}

// FailRefund marks a pending refund as declined and releases its reserved amount.
func (p *Payment) FailRefund(refundID uuid.UUID, reason string) error {
	r := p.Refund(refundID)
	if r == nil {
		return apperror.NotFound("refund not found: " + refundID.String())
	}
	if r.Status != RefundStatusPending {
		return apperror.InvalidState("can only fail a PENDING refund, current: " + string(r.Status))
	}

	now := time.Now().UTC()
	r.Status = RefundStatusFailed
	r.FailureReason = reason
	r.UpdatedAt = now
	p.UpdatedAt = now
	p.raise(RefundFailed{PaymentID: p.ID, RefundID: r.ID, Reason: reason, OccurredAt: now})
	return nil
}
