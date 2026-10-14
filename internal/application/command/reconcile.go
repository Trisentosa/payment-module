package command

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
)

// ReconcileConfig sets when the reconciler steps in. The zero value uses the defaults.
type ReconcileConfig struct {
	// ChargeGrace is how long an INITIATED payment waits before its gateway is asked.
	// It must exceed the gateway call timeout, so in-flight charges are left alone.
	ChargeGrace time.Duration
	// RefundGrace is how long a PENDING refund waits for the gateway's notification
	// before the reconciler checks the transaction's status.
	RefundGrace time.Duration
	// RefundRetryAfter is when a PENDING refund the gateway doesn't know about is sent
	// again under the same key, which the gateway deduplicates.
	RefundRetryAfter time.Duration
	// RefundGiveUpAfter stops retries; the refund then needs a person. Midtrans accepts
	// retries of a refund_key for 7 days.
	RefundGiveUpAfter time.Duration
	BatchSize         int
}

func (c ReconcileConfig) withDefaults() ReconcileConfig {
	if c.ChargeGrace == 0 {
		c.ChargeGrace = 2 * time.Minute
	}
	if c.RefundGrace == 0 {
		c.RefundGrace = 5 * time.Minute
	}
	if c.RefundRetryAfter == 0 {
		c.RefundRetryAfter = 30 * time.Minute
	}
	if c.RefundGiveUpAfter == 0 {
		c.RefundGiveUpAfter = 6 * 24 * time.Hour
	}
	if c.BatchSize == 0 {
		c.BatchSize = 50
	}
	return c
}

// Reconciler settles what a lost gateway reply left open: charges whose outcome is
// unknown (INITIATED payments) and refunds still PENDING without a notification.
type Reconciler struct {
	repo     payment.Repository
	factory  ports.GatewayFactory
	cache    ports.CachePort
	attempts ports.AttemptRecorder
	cfg      ReconcileConfig
	now      func() time.Time
}

func NewReconciler(repo payment.Repository, factory ports.GatewayFactory, cache ports.CachePort, attempts ports.AttemptRecorder, cfg ReconcileConfig) *Reconciler {
	return &Reconciler{repo: repo, factory: factory, cache: cache, attempts: attempts, cfg: cfg.withDefaults(), now: time.Now}
}

const reconcileLockKey = "lock:reconciler"

// Run reconciles every interval until ctx ends. A cache lock keeps replicas from
// reconciling the same payments at once.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			acquired, err := r.cache.SetNX(ctx, reconcileLockKey, "1", interval)
			if err != nil || !acquired {
				continue
			}
			r.RunOnce(ctx)
			_ = r.cache.Delete(ctx, reconcileLockKey)
		}
	}
}

// RunOnce makes one reconciliation pass. Errors are logged per payment; a payment that
// can't be settled now is picked up again on the next pass.
func (r *Reconciler) RunOnce(ctx context.Context) {
	log := logger.FromContext(ctx)
	now := r.now()

	charges, err := r.repo.FindAwaitingChargeOutcome(ctx, now.Add(-r.cfg.ChargeGrace), r.cfg.BatchSize)
	if err != nil {
		log.Error("reconciler: find charges awaiting outcome", "err", err)
	}
	for _, p := range charges {
		r.reconcileCharge(ctx, p)
	}

	refunds, err := r.repo.FindWithPendingRefunds(ctx, now.Add(-r.cfg.RefundGrace), r.cfg.BatchSize)
	if err != nil {
		log.Error("reconciler: find pending refunds", "err", err)
	}
	for _, p := range refunds {
		r.reconcileRefunds(ctx, p)
	}
}

// reconcileCharge settles an INITIATED payment from the gateway's view of its charge.
func (r *Reconciler) reconcileCharge(ctx context.Context, p *payment.Payment) {
	log := logger.FromContext(ctx).With("payment_id", p.ID)
	gw, err := r.factory.Get(string(p.GatewayType))
	if err != nil {
		log.Error("reconciler: no gateway for payment", "gateway", p.GatewayType, "err", err)
		return
	}

	state, err := r.lookup(ctx, gw, p, nil)
	if err != nil {
		log.Warn("reconciler: charge status unavailable, retrying next pass", "err", err)
		return
	}

	switch {
	case !state.Found:
		p.MarkFailed("CHARGE_NOT_CREATED", "the gateway has no record of the charge")
	case state.Status == ports.TransactionSettled:
		if err = p.MarkPending(state.GatewayTransactionID, state.RawResponse); err == nil {
			err = p.MarkCompleted(r.now().UTC())
		}
	case state.Status == ports.TransactionFailed:
		if err = p.MarkPending(state.GatewayTransactionID, state.RawResponse); err == nil {
			p.MarkFailed(state.GatewayStatus, "gateway status: "+state.GatewayStatus)
		}
	case state.Status == ports.TransactionPending:
		// The caller never received the payment instructions, so nobody can finish this
		// payment. Cancel it at the gateway before failing it here.
		if !r.cancel(ctx, gw, p, state.GatewayTransactionID) {
			return
		}
		if err = p.MarkPending(state.GatewayTransactionID, state.RawResponse); err == nil {
			p.MarkFailed("CHARGE_REPLY_LOST", "the charge reply was lost before the caller got payment instructions; cancelled at the gateway")
		}
	default:
		log.Warn("reconciler: unrecognised gateway status, leaving the payment INITIATED", "gateway_status", state.GatewayStatus)
		return
	}
	if err != nil {
		log.Error("reconciler: apply charge outcome", "err", err)
		return
	}
	r.save(ctx, p, "charge reconciled")
}

// reconcileRefunds settles a payment's PENDING refunds: from the gateway's refund list
// first, then by re-sending refunds the gateway doesn't know about.
func (r *Reconciler) reconcileRefunds(ctx context.Context, p *payment.Payment) {
	log := logger.FromContext(ctx).With("payment_id", p.ID)
	gw, err := r.factory.Get(string(p.GatewayType))
	if err != nil {
		log.Error("reconciler: no gateway for payment", "gateway", p.GatewayType, "err", err)
		return
	}

	state, err := r.lookup(ctx, gw, p, oldestPendingRefund(p))
	if err != nil {
		log.Warn("reconciler: refund status unavailable, retrying next pass", "err", err)
		return
	}
	changed, err := applyRefundNotifications(ctx, p, state.Refunds)
	if err != nil {
		log.Error("reconciler: apply gateway refunds", "err", err)
		return
	}

	now := r.now()
	for _, rf := range p.Refunds {
		if rf.Status != payment.RefundStatusPending {
			continue
		}
		age := now.Sub(rf.CreatedAt)
		switch {
		case age >= r.cfg.RefundGiveUpAfter:
			log.Error("reconciler: refund still PENDING past the retry window; needs manual review",
				"refund_id", rf.ID, "amount", rf.Amount, "age", age.Round(time.Minute))
		case age >= r.cfg.RefundRetryAfter:
			// The gateway's list doesn't include it. Re-send under the same key: if the
			// first request did go through, the gateway won't refund twice.
			res, err := callRefund(ctx, gw, r.attempts, p, rf)
			if err != nil {
				log.Warn("reconciler: refund retry outcome unknown", "refund_id", rf.ID, "err", err)
				continue
			}
			if res.Accepted {
				err = p.CompleteRefund(rf.ID, res.GatewayRefundID)
			} else {
				err = p.FailRefund(rf.ID, res.DeclineReason)
			}
			if err != nil {
				log.Error("reconciler: apply refund retry", "refund_id", rf.ID, "err", err)
				continue
			}
			changed = true
		}
	}

	if changed {
		r.save(ctx, p, "refunds reconciled")
	}
}

// lookup asks the gateway for the payment's transaction and records the call, tied to
// forRefund when the check is about a refund.
func (r *Reconciler) lookup(ctx context.Context, gw ports.GatewayPort, p *payment.Payment, forRefund *payment.Refund) (*ports.TransactionState, error) {
	started := time.Now()
	state, err := gw.GetTransaction(ctx, ports.TransactionRef{PaymentID: p.ID.String(), GatewayTransactionID: p.GatewayTransactionID})

	attempt := ports.Attempt{PaymentID: p.ID, Operation: ports.OperationStatus,
		GatewayTransactionID: p.GatewayTransactionID, Duration: time.Since(started)}
	if forRefund != nil {
		id := forRefund.ID
		attempt.RefundID = &id
	}
	if err != nil {
		attempt = failedAttempt(attempt, err)
	} else {
		attempt.Outcome, attempt.GatewayResponse = ports.AttemptSucceeded, state.RawResponse
		if state.GatewayTransactionID != "" {
			attempt.GatewayTransactionID = state.GatewayTransactionID
		}
	}
	recordAttempt(ctx, r.attempts, attempt)
	return state, err
}

// cancel cancels a pending gateway transaction, records the call, and reports success.
func (r *Reconciler) cancel(ctx context.Context, gw ports.GatewayPort, p *payment.Payment, gatewayTxID string) bool {
	started := time.Now()
	err := gw.CancelTransaction(ctx, gatewayTxID)
	attempt := ports.Attempt{PaymentID: p.ID, Operation: ports.OperationCancel,
		GatewayTransactionID: gatewayTxID, Duration: time.Since(started), Outcome: ports.AttemptSucceeded}
	if err != nil {
		attempt = failedAttempt(attempt, err)
		logger.FromContext(ctx).Warn("reconciler: cancel of orphaned charge failed, retrying next pass", "payment_id", p.ID, "err", err)
	}
	recordAttempt(ctx, r.attempts, attempt)
	return err == nil
}

func (r *Reconciler) save(ctx context.Context, p *payment.Payment, msg string) {
	log := logger.FromContext(ctx)
	if err := r.repo.Save(ctx, p); err != nil {
		// Typically CONFLICT: a webhook got there first. The next pass sees the new state.
		log.Warn("reconciler: save failed, retrying next pass", "payment_id", p.ID, "err", err)
		return
	}
	ports.DropCachedPayment(ctx, r.cache, p)
	log.Info("reconciler: "+msg, "payment_id", p.ID, "status", p.Status)
}

// oldestPendingRefund returns the first PENDING refund, or nil.
func oldestPendingRefund(p *payment.Payment) *payment.Refund {
	for _, rf := range p.Refunds {
		if rf.Status == payment.RefundStatusPending {
			return rf
		}
	}
	return nil
}
