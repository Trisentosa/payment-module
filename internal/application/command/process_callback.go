package command

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type ProcessCallbackCommand struct {
	GatewayType string
	RawPayload  []byte
	Headers     map[string][]string
}

type ProcessCallbackHandler struct {
	repo    payment.Repository
	factory ports.GatewayFactory
	cache   ports.CachePort
}

func NewProcessCallbackHandler(repo payment.Repository, factory ports.GatewayFactory, cache ports.CachePort) *ProcessCallbackHandler {
	return &ProcessCallbackHandler{repo: repo, factory: factory, cache: cache}
}

func (h *ProcessCallbackHandler) Handle(ctx context.Context, cmd ProcessCallbackCommand) error {
	log := logger.FromContext(ctx)

	gw, err := h.factory.Get(cmd.GatewayType)
	if err != nil {
		return apperror.InvalidInput("unknown gateway: " + cmd.GatewayType)
	}

	n, err := gw.ParseWebhook(cmd.RawPayload, cmd.Headers)
	if err != nil {
		log.Warn("webhook rejected", "gateway", cmd.GatewayType, "err", err)
		return err
	}

	p, err := h.findPayment(ctx, n)
	if err != nil {
		return err
	}

	changed, err := applyRefundNotifications(ctx, p, n.Refunds)
	if err != nil {
		return err
	}

	// Refund notifications arrive for COMPLETED payments, which the status switch below
	// must leave alone: a "refund" status is not a payment failure.
	if !p.Status.IsTerminal() {
		switch n.Status {
		case ports.TransactionSettled:
			if err = p.MarkCompleted(time.Now().UTC()); err != nil {
				return err
			}
			changed = true
		case ports.TransactionFailed:
			p.MarkFailed(n.GatewayStatus, "gateway status: "+n.GatewayStatus)
			changed = true
		}
	}

	if !changed {
		log.Info("webhook needs no change", "payment_id", p.ID, "status", p.Status, "gateway_status", n.GatewayStatus)
		return nil
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return err
	}

	ports.DropCachedPayment(ctx, h.cache, p)

	log.Info("webhook processed", "payment_id", p.ID, "new_status", p.Status)
	return nil
}

// findPayment resolves the notification by the payment ID the gateway echoed back. Payments
// created before IDs were sent as the order ID carry the caller's reference there instead,
// so those fall back to the gateway's transaction ID.
func (h *ProcessCallbackHandler) findPayment(ctx context.Context, n *ports.WebhookNotification) (*payment.Payment, error) {
	if id, err := uuid.Parse(n.PaymentID); err == nil {
		p, err := h.repo.FindByID(ctx, id)
		if !apperror.IsCode(err, apperror.CodeNotFound) {
			return p, err
		}
	}
	return h.repo.FindByGatewayTransactionID(ctx, n.GatewayTransactionID)
}

// applyRefundNotifications completes the PENDING refunds the gateway reports as done.
// It matches on the refund key PayGate sent, which is the refund's ID.
func applyRefundNotifications(ctx context.Context, p *payment.Payment, refunds []ports.RefundNotification) (bool, error) {
	log := logger.FromContext(ctx)
	changed := false
	for _, rn := range refunds {
		var r *payment.Refund
		if id, err := uuid.Parse(rn.RefundKey); err == nil {
			r = p.Refund(id)
		}
		switch {
		case r == nil:
			// e.g. a refund made in the gateway's dashboard; PayGate has no record to update.
			log.Warn("gateway reports a refund PayGate did not request", "payment_id", p.ID,
				"refund_key", rn.RefundKey, "gateway_refund_id", rn.GatewayRefundID, "amount", rn.Amount)
			continue
		case r.Status != payment.RefundStatusPending:
			continue
		case rn.Amount != 0 && rn.Amount != r.Amount:
			log.Error("gateway refund amount differs from the requested amount; left PENDING for review",
				"payment_id", p.ID, "refund_id", r.ID, "requested", r.Amount, "reported", rn.Amount)
			continue
		}
		if err := p.CompleteRefund(r.ID, rn.GatewayRefundID); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}
