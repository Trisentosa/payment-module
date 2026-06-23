package command

import (
	"context"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/google/uuid"
)

type CancelPaymentCommand struct {
	PaymentID     uuid.UUID
	CallerService string
}

type CancelPaymentHandler struct {
	repo    payment.Repository
	factory ports.GatewayFactory
}

func NewCancelPaymentHandler(repo payment.Repository, factory ports.GatewayFactory) *CancelPaymentHandler {
	return &CancelPaymentHandler{repo: repo, factory: factory}
}

func (h *CancelPaymentHandler) Handle(ctx context.Context, cmd CancelPaymentCommand) error {
	log := logger.FromContext(ctx)

	p, err := h.repo.FindByID(ctx, cmd.PaymentID)
	if err != nil {
		return err
	}

	if err = p.Cancel(); err != nil {
		return err
	}

	gw, err := h.factory.Get(string(p.GatewayType))
	if err == nil && p.GatewayTransactionID != "" {
		if cancelErr := gw.CancelTransaction(ctx, p.GatewayTransactionID); cancelErr != nil {
			log.Warn("gateway cancel failed, continuing with local cancel", "payment_id", p.ID, "err", cancelErr)
		}
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return err
	}

	log.Info("payment cancelled", "payment_id", p.ID)
	return nil
}
