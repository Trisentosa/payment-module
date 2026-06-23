package command

import (
	"context"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/google/uuid"
)

type ForceExpireCommand struct {
	PaymentID uuid.UUID
}

type ForceExpireHandler struct {
	repo  payment.Repository
	cache ports.CachePort
}

func NewForceExpireHandler(repo payment.Repository, cache ports.CachePort) *ForceExpireHandler {
	return &ForceExpireHandler{repo: repo, cache: cache}
}

func (h *ForceExpireHandler) Handle(ctx context.Context, cmd ForceExpireCommand) error {
	log := logger.FromContext(ctx)

	p, err := h.repo.FindByID(ctx, cmd.PaymentID)
	if err != nil {
		return err
	}

	if err = p.MarkExpired(); err != nil {
		return err
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return err
	}

	_ = h.cache.Delete(ctx, "payment:"+p.ID.String())

	log.Info("payment force-expired by admin", "payment_id", p.ID)
	return nil
}
