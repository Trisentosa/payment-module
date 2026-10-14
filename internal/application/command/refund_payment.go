package command

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type RefundPaymentCommand struct {
	PaymentID     uuid.UUID
	ReferenceID   string // caller's refund reference; repeating it replays the original refund
	Amount        int64  // 0 refunds everything still refundable
	Reason        string
	CallerService string // must own the payment unless Admin is set
	Admin         bool
}

type RefundPaymentResult struct {
	Payment  *payment.Payment
	Refund   *payment.Refund
	Replayed bool // the reference was already used; Refund is the earlier one
}

type RefundPaymentHandler struct {
	repo     payment.Repository
	factory  ports.GatewayFactory
	cache    ports.CachePort
	attempts ports.AttemptRecorder
}

func NewRefundPaymentHandler(repo payment.Repository, factory ports.GatewayFactory, cache ports.CachePort, attempts ports.AttemptRecorder) *RefundPaymentHandler {
	return &RefundPaymentHandler{repo: repo, factory: factory, cache: cache, attempts: attempts}
}

// Handle refunds a completed payment through its gateway. The refund is stored as PENDING
// before the gateway is called, so its amount is reserved and a crash mid-call leaves a
// record that the gateway's refund notification can settle.
func (h *RefundPaymentHandler) Handle(ctx context.Context, cmd RefundPaymentCommand) (*RefundPaymentResult, error) {
	log := logger.FromContext(ctx)

	p, err := h.repo.FindByID(ctx, cmd.PaymentID)
	if err != nil {
		return nil, err
	}
	if !cmd.Admin && p.CallerService != cmd.CallerService {
		// Same answer as a missing payment, so callers can't probe each other's payments.
		return nil, apperror.NotFound("payment not found: " + cmd.PaymentID.String())
	}

	if cmd.ReferenceID == "" {
		return nil, apperror.InvalidInput("reference_id is required")
	}
	if existing := p.RefundByReference(cmd.ReferenceID); existing != nil {
		if cmd.Amount != 0 && cmd.Amount != existing.Amount {
			return nil, apperror.IdempotencyConflict("refund reference_id was already used with a different amount")
		}
		return &RefundPaymentResult{Payment: p, Refund: existing, Replayed: true}, nil
	}

	gw, err := h.factory.Get(string(p.GatewayType))
	if err != nil {
		return nil, apperror.Internal("no gateway configured for "+string(p.GatewayType), err)
	}
	capability := gw.Capability(p.PaymentMethodType)
	if !capability.Refund {
		return nil, apperror.InvalidState(fmt.Sprintf(
			"%s payments can't be refunded through %s; refund the customer outside PayGate", p.PaymentMethodType, p.GatewayType))
	}

	amount := cmd.Amount
	if amount == 0 {
		amount = p.RefundableAmount()
	}
	if !capability.PartialRefund && (amount != p.Amount.Amount || p.RefundableAmount() != p.Amount.Amount) {
		return nil, apperror.InvalidInput(fmt.Sprintf("%s payments can only be refunded in full", p.PaymentMethodType))
	}

	requestedBy := cmd.CallerService
	if cmd.Admin {
		requestedBy = "admin"
	}
	r, err := p.RequestRefund(cmd.ReferenceID, amount, cmd.Reason, requestedBy)
	if err != nil {
		return nil, err
	}
	// Reserve the amount. A concurrent refund on the same payment loses here with CONFLICT.
	if err = h.repo.Save(ctx, p); err != nil {
		return nil, err
	}
	ports.DropCachedPayment(ctx, h.cache, p)

	res, err := callRefund(ctx, gw, h.attempts, p, r)
	if err != nil {
		log.Warn("refund outcome unknown; left PENDING for the gateway's refund notification",
			"payment_id", p.ID, "refund_id", r.ID, "err", err)
		return &RefundPaymentResult{Payment: p, Refund: r}, nil
	}

	refundID := r.ID
	apply := func(p *payment.Payment) error {
		if res.Accepted {
			return p.CompleteRefund(refundID, res.GatewayRefundID)
		}
		return p.FailRefund(refundID, res.DeclineReason)
	}
	if p, err = h.saveOutcome(ctx, p, apply); err != nil {
		// The gateway has decided but PayGate couldn't record it. The refund stays PENDING
		// in storage until the gateway's refund notification arrives.
		log.Error("refund outcome not recorded", "payment_id", cmd.PaymentID, "refund_id", refundID, "accepted", res.Accepted, "err", err)
		return nil, err
	}
	ports.DropCachedPayment(ctx, h.cache, p)

	log.Info("refund processed", "payment_id", p.ID, "refund_id", refundID, "accepted", res.Accepted, "payment_status", p.Status)
	return &RefundPaymentResult{Payment: p, Refund: p.Refund(refundID)}, nil
}

// saveOutcome applies fn and saves. If a concurrent write got there first (typically the
// gateway's own refund notification), it reloads the payment and applies fn once more.
func (h *RefundPaymentHandler) saveOutcome(ctx context.Context, p *payment.Payment, fn func(*payment.Payment) error) (*payment.Payment, error) {
	if err := fn(p); err != nil {
		return nil, err
	}
	err := h.repo.Save(ctx, p)
	if !apperror.IsCode(err, apperror.CodeConflict) {
		return p, err
	}

	fresh, err := h.repo.FindByID(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	if err = fn(fresh); err != nil {
		return nil, err
	}
	return fresh, h.repo.Save(ctx, fresh)
}

// callRefund asks the gateway to refund r, keyed by r's ID, and records the call.
func callRefund(ctx context.Context, gw ports.GatewayPort, rec ports.AttemptRecorder, p *payment.Payment, r *payment.Refund) (*ports.RefundResult, error) {
	started := time.Now()
	res, err := gw.CreateRefund(ctx, ports.RefundRequest{
		GatewayTransactionID: p.GatewayTransactionID,
		RefundKey:            r.ID.String(),
		Amount:               r.Amount,
		Reason:               r.Reason,
	})

	refundID := r.ID
	attempt := ports.Attempt{PaymentID: p.ID, RefundID: &refundID, Operation: ports.OperationRefund,
		GatewayTransactionID: p.GatewayTransactionID, Duration: time.Since(started)}
	switch {
	case err != nil:
		// The port's contract: any CreateRefund error leaves the outcome unknown.
		attempt = failedAttempt(attempt, err)
		attempt.Outcome = ports.AttemptUnknown
	case res.Accepted:
		attempt.Outcome, attempt.GatewayResponse = ports.AttemptSucceeded, res.RawResponse
	default:
		attempt.Outcome, attempt.GatewayResponse = ports.AttemptFailed, res.RawResponse
		attempt.ErrorCode, attempt.ErrorMessage = "DECLINED", res.DeclineReason
	}
	recordAttempt(ctx, rec, attempt)
	return res, err
}
