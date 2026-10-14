package command

import (
	"context"
	"fmt"
	"time"

	"github.com/Trisentosa/payment-module/internal/application/ports"
	"github.com/Trisentosa/payment-module/internal/domain/payment"
	"github.com/Trisentosa/payment-module/internal/infrastructure/logger"
	"github.com/Trisentosa/payment-module/internal/pkg/apperror"
)

type CreatePaymentCommand struct {
	ReferenceID        string
	CallerService      string
	Amount             int64
	Currency           string
	GatewayType        string
	PaymentMethodType  string
	BankCode           string
	CardToken          string
	ReturnURL          string
	CustomerName       string
	CustomerEmail      string
	CustomerPhone      string
	CustomerExternalID string
	Description        string
	ExpiredAt          *time.Time
	Metadata           map[string]any
}

type CreatePaymentResult struct {
	Payment    *payment.Payment
	NextAction ports.NextAction
}

type CreatePaymentHandler struct {
	repo    payment.Repository
	factory ports.GatewayFactory
	outbox  ports.OutboxWriter
}

func NewCreatePaymentHandler(repo payment.Repository, factory ports.GatewayFactory, outbox ports.OutboxWriter) *CreatePaymentHandler {
	return &CreatePaymentHandler{repo: repo, factory: factory, outbox: outbox}
}

func (h *CreatePaymentHandler) Handle(ctx context.Context, cmd CreatePaymentCommand) (*CreatePaymentResult, error) {
	log := logger.FromContext(ctx)

	money, err := payment.NewMoney(cmd.Amount, cmd.Currency)
	if err != nil {
		return nil, apperror.InvalidInput(err.Error())
	}

	method, err := payment.ParseMethod(cmd.PaymentMethodType)
	if err != nil {
		return nil, err
	}

	p, err := payment.New(
		cmd.ReferenceID,
		cmd.CallerService,
		money,
		payment.GatewayType(cmd.GatewayType),
		method,
		payment.CustomerInfo{
			ExternalID: cmd.CustomerExternalID,
			Name:       cmd.CustomerName,
			Email:      cmd.CustomerEmail,
			Phone:      cmd.CustomerPhone,
		},
		cmd.Description,
		cmd.ExpiredAt,
		cmd.Metadata,
	)
	if err != nil {
		return nil, err
	}

	// Check before calling the gateway, so a reused reference never opens a second transaction.
	// The unique constraint in Save still catches two requests racing past this check.
	existing, err := h.repo.FindByReference(ctx, cmd.ReferenceID, cmd.CallerService)
	switch {
	case err == nil:
		return nil, apperror.AlreadyExists(fmt.Sprintf(
			"payment %s already exists for reference_id %s (status %s)", existing.ID, cmd.ReferenceID, existing.Status))
	case !apperror.IsCode(err, apperror.CodeNotFound):
		return nil, err
	}

	gw, err := h.factory.Get(cmd.GatewayType)
	if err != nil {
		return nil, apperror.InvalidInput("unsupported gateway: " + cmd.GatewayType)
	}
	if !gw.Capability(method).Charge {
		return nil, apperror.InvalidInput(fmt.Sprintf("%s does not support payment_method_type %s", cmd.GatewayType, method))
	}

	log.Info("calling gateway", "gateway", cmd.GatewayType, "payment_id", p.ID)

	gwResp, err := gw.CreateTransaction(ctx, ports.CreateTransactionRequest{
		PaymentID:     p.ID.String(),
		ReferenceID:   cmd.ReferenceID,
		Amount:        cmd.Amount,
		Currency:      cmd.Currency,
		PaymentMethod: method,
		BankCode:      cmd.BankCode,
		CardToken:     cmd.CardToken,
		ReturnURL:     cmd.ReturnURL,
		CustomerName:  cmd.CustomerName,
		CustomerEmail: cmd.CustomerEmail,
		CustomerPhone: cmd.CustomerPhone,
		Description:   cmd.Description,
		ExpiredAt:     cmd.ExpiredAt,
		Metadata:      cmd.Metadata,
	})
	if apperror.IsCode(err, apperror.CodeInvalidInput) {
		// The gateway refused the request's contents before creating anything. Nothing is
		// stored, so the caller can fix the request and retry with the same reference.
		return nil, err
	}
	if err != nil {
		p.MarkFailed("GATEWAY_ERROR", err.Error())
		if saveErr := h.repo.Save(ctx, p); saveErr != nil {
			log.Warn("failed to record gateway failure", "payment_id", p.ID, "err", saveErr)
		}
		return nil, err
	}

	if err = p.MarkPending(gwResp.GatewayTransactionID, gwResp.RawResponse); err != nil {
		return nil, err
	}

	// Some charges finish at once, e.g. a card captured without 3DS, or one declined outright.
	switch gwResp.Status {
	case ports.TransactionSettled:
		if err = p.MarkCompleted(time.Now().UTC()); err != nil {
			return nil, err
		}
	case ports.TransactionFailed:
		p.MarkFailed("DECLINED", "declined by gateway at charge time")
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return nil, err
	}

	log.Info("payment created", "payment_id", p.ID, "status", p.Status)

	return &CreatePaymentResult{
		Payment:    p,
		NextAction: gwResp.NextAction,
	}, nil
}
