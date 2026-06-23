package command

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type ProcessCallbackCommand struct {
	GatewayType string
	RawPayload  []byte
	Signature   string
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

	if err = gw.VerifyWebhookSignature(cmd.RawPayload, cmd.Signature); err != nil {
		log.Warn("webhook signature invalid", "gateway", cmd.GatewayType)
		return err
	}

	var n map[string]string
	_ = json.Unmarshal(cmd.RawPayload, &n)
	gatewayTxID := n["transaction_id"]
	gatewayStatus := n["transaction_status"]

	p, err := h.repo.FindByGatewayTransactionID(ctx, gatewayTxID)
	if err != nil {
		return err
	}

	if p.Status.IsTerminal() {
		log.Info("webhook skipped — payment already terminal", "payment_id", p.ID, "status", p.Status)
		return nil
	}

	switch gatewayStatus {
	case "settlement", "capture":
		if err = p.MarkCompleted(time.Now().UTC()); err != nil {
			return err
		}
	case "deny", "cancel", "expire", "failure":
		p.MarkFailed(gatewayStatus, "gateway status: "+gatewayStatus)
	default:
		log.Warn("unhandled gateway status", "status", gatewayStatus)
		return nil
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return err
	}

	_ = h.cache.Delete(ctx, "payment:"+p.ID.String())

	log.Info("webhook processed", "payment_id", p.ID, "new_status", p.Status)
	return nil
}
