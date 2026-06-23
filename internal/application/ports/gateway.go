package ports

import (
	"context"
	"time"
)

type CreateTransactionRequest struct {
	PaymentID         string
	ReferenceID       string
	Amount            int64
	Currency          string
	PaymentMethodType string
	BankCode          string
	CustomerName      string
	CustomerEmail     string
	CustomerPhone     string
	Description       string
	ExpiredAt         *time.Time
	Metadata          map[string]any
}

type GatewayResponse struct {
	GatewayTransactionID string
	PaymentInstructions  map[string]any
	RawResponse          map[string]any
}

type GatewayPort interface {
	CreateTransaction(ctx context.Context, req CreateTransactionRequest) (*GatewayResponse, error)
	GetTransactionStatus(ctx context.Context, gatewayTxID string) (string, error)
	CancelTransaction(ctx context.Context, gatewayTxID string) error
	CreateRefund(ctx context.Context, gatewayTxID string, amount int64, reason string) (string, error)
	VerifyWebhookSignature(payload []byte, signature string) error
}

type GatewayFactory interface {
	Get(gatewayType string) (GatewayPort, error)
}
