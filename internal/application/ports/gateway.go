package ports

import (
	"context"
	"time"

	"github.com/Trisentosa/payment-module/internal/domain/payment"
)

type CreateTransactionRequest struct {
	PaymentID     string
	ReferenceID   string
	Amount        int64
	Currency      string
	PaymentMethod payment.Method
	BankCode      string // VIRTUAL_ACCOUNT: which bank issues the account number
	CardToken     string // CARD: one-time token from the gateway's client-side tokenizer; never raw card data
	ReturnURL     string // where the customer lands after an e-wallet app or 3DS redirect
	CustomerName  string
	CustomerEmail string
	CustomerPhone string
	Description   string
	ExpiredAt     *time.Time
	Metadata      map[string]any
}

// NextActionType tells the caller what the customer must do to finish paying.
type NextActionType string

const (
	NextActionDisplayVA NextActionType = "DISPLAY_VA" // transfer to VANumber at Bank
	NextActionDisplayQR NextActionType = "DISPLAY_QR" // scan QRString / QRImageURL
	NextActionRedirect  NextActionType = "REDIRECT"   // open RedirectURL (app deeplink or 3DS page)
	NextActionNone      NextActionType = "NONE"       // nothing left to do, e.g. a card captured at once
)

type NextAction struct {
	Type        NextActionType
	VANumber    string
	Bank        string
	QRString    string
	QRImageURL  string // also set for REDIRECT when the gateway offers a QR for desktop users
	RedirectURL string
	ExpiresAt   *time.Time
}

type GatewayResponse struct {
	GatewayTransactionID string
	// Status is where the transaction stands right after the charge. Most methods are
	// PENDING until the customer acts; a card can be SETTLED (captured) or FAILED (declined).
	Status      TransactionStatus
	NextAction  NextAction
	RawResponse map[string]any
}

// MethodCapability says what a gateway can do for one payment method.
type MethodCapability struct {
	Charge        bool
	Refund        bool // refundable through the gateway's API
	PartialRefund bool
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
	// Capability reports what this gateway supports for a payment method.
	Capability(method payment.Method) MethodCapability
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
