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

// TransactionStatus is a gateway-neutral view of a transaction's state.
// Each adapter maps its own status vocabulary onto these values.
type TransactionStatus string

const (
	TransactionPending TransactionStatus = "PENDING"
	TransactionSettled TransactionStatus = "SETTLED"
	TransactionFailed  TransactionStatus = "FAILED"
	TransactionUnknown TransactionStatus = "UNKNOWN"
)

// WebhookNotification is a verified, gateway-neutral status notification.
type WebhookNotification struct {
	// PaymentID is PayGate's payment ID as echoed back by the gateway (Midtrans order_id).
	// It may be empty, or not a payment ID for payments created before IDs were sent.
	PaymentID            string
	GatewayTransactionID string
	Status               TransactionStatus
	GatewayStatus        string // the gateway's own status string, kept for logs and failure codes
}

type GatewayPort interface {
	CreateTransaction(ctx context.Context, req CreateTransactionRequest) (*GatewayResponse, error)
	GetTransactionStatus(ctx context.Context, gatewayTxID string) (string, error)
	CancelTransaction(ctx context.Context, gatewayTxID string) error
	CreateRefund(ctx context.Context, gatewayTxID string, amount int64, reason string) (string, error)
	// ParseWebhook authenticates a notification and translates it. headers carries the
	// HTTP request headers for gateways that sign in a header rather than the body.
	// An unauthentic or malformed notification returns an INVALID_INPUT error.
	ParseWebhook(payload []byte, headers map[string][]string) (*WebhookNotification, error)
}

type GatewayFactory interface {
	Get(gatewayType string) (GatewayPort, error)
}
