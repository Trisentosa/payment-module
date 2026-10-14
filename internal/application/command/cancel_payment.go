package command

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
	"github.com/google/uuid"
)

type CancelPaymentCommand struct {
	PaymentID     uuid.UUID
	CallerService string
}

type CancelPaymentHandler struct {
	repo     payment.Repository
	factory  ports.GatewayFactory
	cache    ports.CachePort
	attempts ports.AttemptRecorder
}

func NewCancelPaymentHandler(repo payment.Repository, factory ports.GatewayFactory, cache ports.CachePort, attempts ports.AttemptRecorder) *CancelPaymentHandler {
	return &CancelPaymentHandler{repo: repo, factory: factory, cache: cache, attempts: attempts}
}

func (h *CancelPaymentHandler) Handle(ctx context.Context, cmd CancelPaymentCommand) error {
	log := logger.FromContext(ctx)

	p, err := h.repo.FindByID(ctx, cmd.PaymentID)
	if err != nil {
		return err
	}
	if p.CallerService != cmd.CallerService {
		// Same answer as a missing payment, so callers can't probe each other's payments.
		return apperror.NotFound("payment not found: " + cmd.PaymentID.String())
	}

	if err = p.Cancel(); err != nil {
		return err
	}

	gw, err := h.factory.Get(string(p.GatewayType))
	if err == nil && p.GatewayTransactionID != "" {
		started := time.Now()
		cancelErr := gw.CancelTransaction(ctx, p.GatewayTransactionID)
		attempt := ports.Attempt{PaymentID: p.ID, Operation: ports.OperationCancel,
			GatewayTransactionID: p.GatewayTransactionID, Duration: time.Since(started), Outcome: ports.AttemptSucceeded}
		if cancelErr != nil {
			attempt = failedAttempt(attempt, cancelErr)
			log.Warn("gateway cancel failed, continuing with local cancel", "payment_id", p.ID, "err", cancelErr)
		}
		recordAttempt(ctx, h.attempts, attempt)
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return err
	}
	ports.DropCachedPayment(ctx, h.cache, p)

	log.Info("payment cancelled", "payment_id", p.ID)
	return nil
}
