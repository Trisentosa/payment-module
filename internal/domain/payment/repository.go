package payment

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type ListFilter struct {
	Status        string
	CallerService string
	From          *time.Time
	To            *time.Time
	Cursor        *time.Time // created_at of last item in previous page
	Limit         int
}

type Repository interface {
	Save(ctx context.Context, p *Payment) error
	FindByID(ctx context.Context, id uuid.UUID) (*Payment, error)
	FindByReference(ctx context.Context, referenceID, callerService string) (*Payment, error)
	FindByGatewayTransactionID(ctx context.Context, gatewayTxID string) (*Payment, error)
	FindExpired(ctx context.Context, limit int) ([]*Payment, error)
	ListPaginated(ctx context.Context, f ListFilter) ([]*Payment, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status Status, fields map[string]any) error
}
