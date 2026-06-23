package command

import (
	"context"
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
	CustomerName       string
	CustomerEmail      string
	CustomerPhone      string
	CustomerExternalID string
	Description        string
	ExpiredAt          *time.Time
	Metadata           map[string]any
}

type CreatePaymentResult struct {
	Payment             *payment.Payment
	PaymentInstructions map[string]any
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

	p, err := payment.New(
		cmd.ReferenceID,
		cmd.CallerService,
		money,
		payment.GatewayType(cmd.GatewayType),
		cmd.PaymentMethodType,
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

	gw, err := h.factory.Get(cmd.GatewayType)
	if err != nil {
		return nil, apperror.InvalidInput("unsupported gateway: " + cmd.GatewayType)
	}

	log.Info("calling gateway", "gateway", cmd.GatewayType, "payment_id", p.ID)

	gwResp, err := gw.CreateTransaction(ctx, ports.CreateTransactionRequest{
		PaymentID:         p.ID.String(),
		ReferenceID:       cmd.ReferenceID,
		Amount:            cmd.Amount,
		Currency:          cmd.Currency,
		PaymentMethodType: cmd.PaymentMethodType,
		BankCode:          cmd.BankCode,
		CustomerName:      cmd.CustomerName,
		CustomerEmail:     cmd.CustomerEmail,
		CustomerPhone:     cmd.CustomerPhone,
		Description:       cmd.Description,
		ExpiredAt:         cmd.ExpiredAt,
		Metadata:          cmd.Metadata,
	})
	if err != nil {
		p.MarkFailed("GATEWAY_ERROR", err.Error())
		_ = h.repo.Save(ctx, p)
		return nil, err
	}

	if err = p.MarkPending(gwResp.GatewayTransactionID, gwResp.RawResponse); err != nil {
		return nil, err
	}

	if err = h.repo.Save(ctx, p); err != nil {
		return nil, err
	}

	log.Info("payment created", "payment_id", p.ID, "status", p.Status)

	return &CreatePaymentResult{
		Payment:             p,
		PaymentInstructions: gwResp.PaymentInstructions,
	}, nil
}
