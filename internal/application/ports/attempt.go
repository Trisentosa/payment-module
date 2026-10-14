package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// GatewayOperation is the kind of outbound gateway call an attempt records.
type GatewayOperation string

const (
	OperationCharge GatewayOperation = "CHARGE"
	OperationCancel GatewayOperation = "CANCEL"
	OperationRefund GatewayOperation = "REFUND"
	OperationStatus GatewayOperation = "STATUS"
)

type AttemptOutcome string

const (
	AttemptSucceeded AttemptOutcome = "SUCCEEDED" // the gateway answered and did what was asked
	AttemptFailed    AttemptOutcome = "FAILED"    // the gateway answered no, or the request was invalid
	AttemptUnknown   AttemptOutcome = "UNKNOWN"   // no usable answer; the call may or may not have taken effect
)

// Attempt is one outbound gateway call made for a payment, kept as an audit trail and as
// evidence when reconciling with the gateway.
type Attempt struct {
	PaymentID            uuid.UUID
	RefundID             *uuid.UUID
	Operation            GatewayOperation
	Outcome              AttemptOutcome
	GatewayTransactionID string
	ErrorCode            string
	ErrorMessage         string
	Duration             time.Duration
	GatewayResponse      map[string]any
}

// AttemptRecorder stores attempts. It writes on its own, outside the payment's
// transaction, so the record survives even if saving the payment afterwards fails.
type AttemptRecorder interface {
	Record(ctx context.Context, a Attempt) error
}
