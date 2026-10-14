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

	if p.Status.IsTerminal() {
		log.Info("webhook skipped — payment already terminal", "payment_id", p.ID, "status", p.Status)
		return nil
	}

	switch n.Status {
	case ports.TransactionSettled:
		if err = p.MarkCompleted(time.Now().UTC()); err != nil {
			return err
		}
	case ports.TransactionFailed:
		p.MarkFailed(n.GatewayStatus, "gateway status: "+n.GatewayStatus)
	default:
		log.Info("webhook status needs no transition", "payment_id", p.ID, "gateway_status", n.GatewayStatus)
		return nil
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return err
	}

	_ = h.cache.Delete(ctx, "payment:"+p.ID.String())

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
