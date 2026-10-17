package payment

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ListFilter struct {
	Status        string
	CallerService string
	GatewayType   string
	ReferenceID   string // prefix match
	From          *time.Time
	To            *time.Time
	After         *PageKey // last row of the previous page
	Limit         int
}

// PageKey is a row's position in a list sorted newest first. The ID breaks ties
// between payments created at the same instant.
type PageKey struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

type Repository interface {
	Save(ctx context.Context, p *Payment) error
	FindByID(ctx context.Context, id uuid.UUID) (*Payment, error)
	FindByReference(ctx context.Context, referenceID, callerService string) (*Payment, error)
	FindByGatewayTransactionID(ctx context.Context, gatewayTxID string) (*Payment, error)
	FindExpired(ctx context.Context, limit int) ([]*Payment, error)
	ListPaginated(ctx context.Context, f ListFilter) ([]*Payment, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status Status, fields map[string]any) error
	// FindAwaitingChargeOutcome returns INITIATED payments created before cutoff, whose
	// charge outcome is unknown.
	FindAwaitingChargeOutcome(ctx context.Context, cutoff time.Time, limit int) ([]*Payment, error)
	// FindWithPendingRefunds returns payments with a refund PENDING since before cutoff.
	FindWithPendingRefunds(ctx context.Context, cutoff time.Time, limit int) ([]*Payment, error)
}
